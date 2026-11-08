package hive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// ChainIDHIVE is the Hive mainnet chain id.
const ChainIDHIVE = "beeab0de00000000000000000000000000000000000000000000000000000000"

var (
	base58Alphabet = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")
	curveN         = btcecS256N()
)

// btcecS256N returns the secp256k1 group order.
func btcecS256N() *big.Int {
	n, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	return n
}

// base58Decode decodes a base58 string to raw bytes (preserving leading zeros).
func base58Decode(s string) ([]byte, error) {
	n := big.NewInt(0)
	leadingZeros := 0
	for i := 0; i < len(s); i++ {
		idx := bytes.IndexByte(base58Alphabet, s[i])
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", s[i])
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(idx)))
		if n.Sign() == 0 {
			leadingZeros++
		}
	}
	raw := n.Bytes()
	out := make([]byte, leadingZeros+len(raw))
	copy(out[leadingZeros:], raw)
	return out, nil
}

// base58Encode encodes raw bytes to a base58 string (preserving leading zeros).
func base58Encode(b []byte) string {
	n := new(big.Int).SetBytes(b)
	leadingZeros := 0
	for _, c := range b {
		if c != 0 {
			break
		}
		leadingZeros++
	}
	out := make([]byte, 0, len(b)*137/100+1)
	mod := new(big.Int)
	for n.Sign() > 0 {
		n.DivMod(n, big.NewInt(58), mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	res := make([]byte, 0, leadingZeros+len(out))
	for i := 0; i < leadingZeros; i++ {
		res = append(res, base58Alphabet[0])
	}
	return string(append(res, out...))
}

func doubleSHA256(b []byte) []byte {
	s := sha256.Sum256(b)
	s2 := sha256.Sum256(s[:])
	return s2[:]
}

// wifToSecret decodes a WIF-encoded private key (Hive "5J..." style) to the
// raw 32-byte secret: base58check with a double-SHA256 checksum.
func wifToSecret(wif string) ([]byte, error) {
	raw, err := base58Decode(wif)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %v", err)
	}
	if len(raw) < 5 {
		return nil, errors.New("invalid private key: too short")
	}
	payload := raw[:len(raw)-4]
	checksum := raw[len(raw)-4:]
	if !bytes.Equal(doubleSHA256(payload)[:4], checksum) {
		return nil, errors.New("invalid private key: checksum mismatch")
	}
	if len(payload) < 1 {
		return nil, errors.New("invalid private key: empty payload")
	}
	secret := payload[1:] // strip version byte
	if len(wif) > 0 && (wif[0] == 'K' || wif[0] == 'L') {
		// Bitcoin compressed-WIF: an extra compression flag byte follows the
		// secret; strip it.
		if len(secret) < 33 {
			return nil, errors.New("invalid private key: missing compression flag")
		}
		secret = secret[:32]
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("invalid private key: expected 32 bytes, got %d", len(secret))
	}
	return secret, nil
}

// secretToWIF encodes a 32-byte secret as a mainnet WIF key (0x80 version).
func secretToWIF(secret []byte) string {
	payload := append([]byte{0x80}, secret...)
	payload = append(payload, doubleSHA256(payload)[:4]...)
	return base58Encode(payload)
}

// pad32 serializes a big.Int as a fixed 32-byte big-endian slice.
func pad32(n *big.Int) []byte {
	b := make([]byte, 32)
	n.FillBytes(b)
	return b
}

// mustHex decodes a hex string, panicking on invalid input (test helper only).
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
