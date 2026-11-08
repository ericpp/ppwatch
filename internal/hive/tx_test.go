package hive

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// A well-known test WIF (Bitcoin test vector secret 1 in Hive WIF form).
// secret = 0x0000...0001
const testWIF = "KwDiBf89QgGbjEhKnhXJuH7LrciVrZi3qYjgd9M7rFU73sVHnoWn"

// hiveStyleWIF is the same secret encoded with the legacy "5" prefix
// (uncompressed WIF), which Hive posting keys use.
func hiveStyleWIF(t *testing.T, secret []byte) string {
	t.Helper()
	return secretToWIF(secret)
}

func TestWIFRoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	secret[31] = 0x01

	wif := hiveStyleWIF(t, secret)
	got, err := wifToSecret(wif)
	if err != nil {
		t.Fatalf("wifToSecret(%s) failed: %v", wif, err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(secret) {
		t.Fatalf("round trip mismatch: got %x want %x", got, secret)
	}

	// The compressed-form WIF must decode to the same secret.
	got2, err := wifToSecret(testWIF)
	if err != nil {
		t.Fatalf("wifToSecret(%s) failed: %v", testWIF, err)
	}
	if len(got2) != 32 || got2[31] != 0x01 {
		t.Fatalf("unexpected secret from compressed WIF: %x", got2)
	}
}

func TestWIFBadChecksum(t *testing.T) {
	if _, err := wifToSecret("5JZLzW26ps4GQSG8Ya6cdHQzy1Jmx1Ze4wzepGNRbd5k5EGepEGx"); err == nil {
		t.Fatal("expected error for garbage WIF")
	}
}

func TestBase58Vectors(t *testing.T) {
	cases := []struct{ raw, enc string }{
		{"", ""},
		{"\x00", "1"},
		{"hello", "Cn8eVZg"},
		{"hello world", "StV1DL6CwTryKyV"},
	}
	for _, c := range cases {
		got := base58Encode([]byte(c.raw))
		if got != c.enc {
			t.Errorf("base58Encode(%q) = %q, want %q", c.raw, got, c.enc)
		}
		dec, err := base58Decode(c.enc)
		if err != nil {
			t.Fatalf("base58Decode(%q) failed: %v", c.enc, err)
		}
		if string(dec) != c.raw {
			t.Errorf("base58Decode(%q) = %q, want %q", c.enc, dec, c.raw)
		}
	}
}

func TestSignDigestHiveVerifiesAndIsCanonical(t *testing.T) {
	secret, err := wifToSecret(hiveStyleWIF(t, mustSecret(0x2A)))
	if err != nil {
		t.Fatal(err)
	}
	_, pub := btcec.PrivKeyFromBytes(secret)

	digest := sha256.Sum256([]byte("podping test digest"))

	sigHex, err := signDigestHive(digest[:], secret)
	if err != nil {
		t.Fatalf("signDigestHive failed: %v", err)
	}

	sig65, err := hex.DecodeString(sigHex)
	if err != nil || len(sig65) != 65 {
		t.Fatalf("bad signature encoding: %v", err)
	}

	// Header must be in the compressed range 31..34 (27+4+i).
	if sig65[0] < 31 || sig65[0] > 34 {
		t.Fatalf("unexpected recovery header byte %d", sig65[0])
	}

	// Graphene canonicality: low-R and low-S byte encodings.
	if !graphenCanonical(sig65[1:33]) || !graphenCanonical(sig65[33:65]) {
		t.Fatal("signature is not graphene-canonical")
	}

	// The recovered public key must match the signer.
	recovered, compressed, err := ecdsa.RecoverCompact(sig65, digest[:])
	if err != nil {
		t.Fatalf("RecoverCompact failed: %v", err)
	}
	if !compressed {
		t.Fatal("expected compressed recovery")
	}
	if hex.EncodeToString(recovered.SerializeCompressed()) != hex.EncodeToString(pub.SerializeCompressed()) {
		t.Fatal("recovered pubkey does not match signer pubkey")
	}
}

func TestSignDigestHiveDeterministic(t *testing.T) {
	secret, _ := wifToSecret(hiveStyleWIF(t, mustSecret(0x7B)))
	sum := sha256.Sum256([]byte("another digest"))
	sig1, err := signDigestHive(sum[:], secret)
	if err != nil {
		t.Fatal(err)
	}
	sig2, err := signDigestHive(sum[:], secret)
	if err != nil {
		t.Fatal(err)
	}
	if sig1 != sig2 {
		t.Fatal("signing is not deterministic for the same digest/key")
	}
}

func TestRFC6979NonceBounds(t *testing.T) {
	secret := mustSecret(0x11)
	sum := sha256.Sum256([]byte("nonce test"))
	for attempt := 0; attempt < 8; attempt++ {
		var extra []byte
		if attempt > 0 {
			extra = []byte{byte(attempt)}
		}
		k := new(big.Int).SetBytes(rfc6979Nonce(sum[:], secret, extra))
		if k.Sign() <= 0 || k.Cmp(curveN) >= 0 {
			t.Fatalf("nonce %d out of range: %s", attempt, k)
		}
	}
}

// mustSecret returns a 32-byte secret filled with the given byte value.
func mustSecret(fill byte) []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = fill
	}
	return s
}
