package hive

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// rfc6979Nonce generates a deterministic RFC6979 nonce for (digest, secret).
// extraEntropy (may be nil) is hashed into the seed so that retries produce
// fresh nonces.
func rfc6979Nonce(digest, secret, extraEntropy []byte) []byte {
	h1 := append([]byte(nil), digest...)
	if len(extraEntropy) > 0 {
		seed := append(append([]byte(nil), digest...), extraEntropy...)
		sum := sha256.Sum256(seed)
		h1 = sum[:]
	}
	// bits2octets(h1): reduce mod N and serialize as 32 bytes.
	z := pad32(new(big.Int).Mod(new(big.Int).SetBytes(h1), curveN))

	V := bytes.Repeat([]byte{0x01}, 32)
	K := bytes.Repeat([]byte{0x00}, 32)

	mac := func(msg []byte) []byte {
		m := hmac.New(sha256.New, K)
		m.Write(msg)
		return m.Sum(nil)
	}
	concat := func(vs ...[]byte) []byte {
		var out []byte
		for _, v := range vs {
			out = append(out, v...)
		}
		return out
	}
	one := []byte{0x00}
	two := []byte{0x01}

	K = mac(concat(V, one, secret, z))
	K = mac(concat(V, two, secret, z))
	V = mac(V)

	for {
		K = mac(concat(V, one))
		V = mac(V)
		k := new(big.Int).SetBytes(V)
		if k.Sign() > 0 && k.Cmp(curveN) < 0 {
			return pad32(k)
		}
		K = mac(concat(V, two))
		V = mac(V)
	}
}

// graphenCanonical reports whether a 32-byte big-endian signature component
// passes graphene's canonicality check.
func graphenCanonical(b []byte) bool {
	return !(b[0]&0x80 != 0) &&
		!(b[0] == 0 && b[1]&0x80 == 0)
}

// signDigestHive produces a Hive-compatible recoverable ECDSA signature:
// hex of [27+4+recovery_id, r(32), s(32)] with a canonical (low-R, low-S)
// signature as required by the graphene chain. It self-verifies by recovering
// the public key before returning.
func signDigestHive(digest, secret []byte) (string, error) {
	_, expectedPub := btcec.PrivKeyFromBytes(secret)

	d := new(big.Int).SetBytes(secret)
	e := new(big.Int).Mod(new(big.Int).SetBytes(digest), curveN)

	for attempt := 0; attempt < 32; attempt++ {
		var extra []byte
		if attempt > 0 {
			extra = []byte{byte(attempt)}
		}
		k := new(big.Int).SetBytes(rfc6979Nonce(digest, secret, extra))

		// R = k*G; r = x-coordinate (mod N).
		_, pubK := btcec.PrivKeyFromBytes(pad32(k))
		pubSer := pubK.SerializeCompressed()
		r := new(big.Int).SetBytes(pubSer[1:33])
		overflow := false
		if r.Cmp(curveN) >= 0 {
			r.Sub(r, curveN)
			overflow = true
		}

		// s = k^-1 * (digest + r*d) mod N
		rd := new(big.Int).Mul(r, d)
		rd.Mod(rd, curveN)
		t := new(big.Int).Add(e, rd)
		t.Mod(t, curveN)
		kInv := new(big.Int).ModInverse(k, curveN)
		if kInv == nil {
			continue
		}
		s := new(big.Int).Mul(kInv, t)
		s.Mod(s, curveN)
		if s.Sign() == 0 {
			continue
		}

		// Normalize to low-S (which also satisfies the canonical top-bit check).
		flipped := false
		if s.Cmp(halfN()) > 0 {
			s.Sub(curveN, s)
			flipped = true
		}

		r32 := pad32(r)
		s32 := pad32(s)
		if !graphenCanonical(r32) || !graphenCanonical(s32) {
			continue // retry with a fresh nonce
		}

		// Recovery id: y-parity of k*G, flipped when s was negated,
		// plus 2 when the x-coordinate wrapped past N.
		i := 0
		if pubSer[0] == 0x03 {
			i = 1
		}
		if flipped {
			i ^= 1
		}
		if overflow {
			i += 2
		}

		sig65 := make([]byte, 65)
		sig65[0] = byte(27 + 4 + i)
		copy(sig65[1:], r32)
		copy(sig65[33:], s32)

		// Self-verify: recover the public key from the compact signature and
		// confirm it matches the signer's public key.
		recovered, _, err := ecdsa.RecoverCompact(sig65, digest)
		if err != nil {
			return "", fmt.Errorf("signature recovery check failed: %v", err)
		}
		if !bytes.Equal(recovered.SerializeCompressed(), expectedPub.SerializeCompressed()) {
			continue // wrong recovery id; try again
		}

		return hex.EncodeToString(sig65), nil
	}

	return "", errors.New("could not produce a canonical signature")
}

// halfN returns N/2 for the curve order.
func halfN() *big.Int {
	return new(big.Int).Rsh(curveN, 1)
}
