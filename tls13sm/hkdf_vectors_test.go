package tls13sm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func assertHex(t *testing.T, name string, got []byte, wantHex string) {
	t.Helper()
	want := mustHex(t, wantHex)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s mismatch:\n got=%x\nwant=%s", name, got, wantHex)
	}
}

// TestRFC5869TC1 pins HKDF-Extract/HKDF-Expand against RFC 5869 Test Case 1
// (SHA-256), the primitive underlying every TLS 1.3 derivation.
func TestRFC5869TC1(t *testing.T) {
	ikm := mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")

	prk := hkdfExtract(sha256.New, salt, ikm)
	assertHex(t, "PRK", prk, "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5")

	okm := hkdfExpand(sha256.New, prk, info, 42)
	assertHex(t, "OKM", okm, "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")
}

// TestRFC8446A1EarlyAndHandshake pins the zero-PSK Early Secret, the first
// "derived" value and the Handshake Secret chain from RFC 8446 Appendix A.1.
func TestRFC8446A1EarlyAndHandshake(t *testing.T) {
	suite := SuiteAES128GCM
	schedule := newKeySchedule(suite)
	schedule.initEarlySecret(nil)

	assertHex(t, "Early Secret", schedule.earlySecret,
		"33ad0a1c607ec03b09e6cd9893680ce210adf300aa1f2660e1b22e10f170f92a")

	emptyHash := sha256.New().Sum(nil)
	assertHex(t, "Derived(ES)",
		deriveSecret(suite.NewHash, schedule.earlySecret, labelDerived, emptyHash),
		"6f2615a108c702c5678f54fc9dbab69716c076189c48250cebeac3576c3611ba")
}
