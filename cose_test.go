package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
)

var testPayload = []byte(`{"sub":"alice","iat":1731700000}`)

// buildSign1 assembles a COSE_Sign1 message from raw parts and signs it.
// protectedRaw is used verbatim as the protected bstr content, so tests can
// inject non-canonical encodings; the signature is computed over whatever
// bytes are supplied, proving rejection comes from header policy rather
// than from a broken signature.
func buildSign1(t *testing.T, protectedRaw []byte, unprotected Map, payload []byte, sign func(toSign []byte) []byte) []byte {
	t.Helper()
	toSign := sigStructure(protectedRaw, nil, payload)
	sig := sign(toSign)

	var buf bytes.Buffer
	err := encodeCanonical(&buf, []any{protectedRaw, unprotected, payload, sig})
	if err != nil {
		t.Fatalf("encode Sign1: %v", err)
	}
	return buf.Bytes()
}

func ed25519Keys(t *testing.T) (ed25519.PublicKey, func([]byte) []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, func(toSign []byte) []byte {
		return ed25519.Sign(priv, toSign)
	}
}

// canonicalProtectedAlg returns the canonical protected header {1: alg}.
func canonicalProtectedAlg(alg int64) []byte {
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, Map{{Key: int64(1), Value: alg}}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestVerifySign1_AcceptsCanonicalProtectedAlg(t *testing.T) {
	pub, sign := ed25519Keys(t)
	protected := canonicalProtectedAlg(AlgorithmEdDSA) // a1 01 27
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	payload, err := VerifySign1(msg, pub, nil)
	if err != nil {
		t.Fatalf("VerifySign1 rejected a valid message: %v", err)
	}
	if !bytes.Equal(payload, testPayload) {
		t.Fatalf("payload mismatch: got %x", payload)
	}
}

func TestVerifySign1_IgnoresUnprotectedAlg(t *testing.T) {
	pub, sign := ed25519Keys(t)
	protected := canonicalProtectedAlg(AlgorithmEdDSA)
	// Unprotected header falsely claims ES256; it must be ignored.
	unprotected := Map{{Key: int64(1), Value: AlgorithmES256}}
	msg := buildSign1(t, protected, unprotected, testPayload, sign)

	if _, err := VerifySign1(msg, pub, nil); err != nil {
		t.Fatalf("VerifySign1 consulted the unprotected alg: %v", err)
	}
}

func TestVerifySign1_RejectsAlgOnlyInUnprotected(t *testing.T) {
	pub, sign := ed25519Keys(t)
	// Attack: alg moved to the unprotected header; protected is empty.
	protected := []byte{0xa0} // empty map
	unprotected := Map{{Key: int64(1), Value: AlgorithmEdDSA}}
	msg := buildSign1(t, protected, unprotected, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrAlgNotProtected) {
		t.Fatalf("want ErrAlgNotProtected, got %v", err)
	}
}

func TestVerifySign1_RejectsEmptyProtected(t *testing.T) {
	pub, sign := ed25519Keys(t)
	msg := buildSign1(t, []byte{}, Map{{Key: int64(1), Value: AlgorithmEdDSA}}, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrAlgNotProtected) {
		t.Fatalf("want ErrAlgNotProtected, got %v", err)
	}
}

func TestVerifySign1_RejectsNonMinimalMapLength(t *testing.T) {
	pub, sign := ed25519Keys(t)
	// Attack: same map {1: -8} but the map head uses the 1-byte length
	// form (0xb8 0x01) instead of the canonical short form (0xa1).
	protected := []byte{0xb8, 0x01, 0x01, 0x27}
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrProtectedNotCanonical) {
		t.Fatalf("want ErrProtectedNotCanonical, got %v", err)
	}
}

func TestVerifySign1_RejectsNonMinimalAlgInt(t *testing.T) {
	pub, sign := ed25519Keys(t)
	// Attack: alg value -8 encoded in 2-byte form (0x38 0x07) instead of
	// the canonical single byte (0x27).
	protected := []byte{0xa1, 0x01, 0x38, 0x07}
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrProtectedNotCanonical) {
		t.Fatalf("want ErrProtectedNotCanonical, got %v", err)
	}
}

func TestVerifySign1_RejectsIndefiniteLengthMap(t *testing.T) {
	pub, sign := ed25519Keys(t)
	// Attack: indefinite-length map with break stop code.
	protected := []byte{0xbf, 0x01, 0x27, 0xff}
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrProtectedNotCanonical) {
		t.Fatalf("want ErrProtectedNotCanonical, got %v", err)
	}
}

func TestVerifySign1_RejectsUnsortedMapKeys(t *testing.T) {
	pub, sign := ed25519Keys(t)
	// {4: "x", 1: -8}: keys are not in canonical order, re-encoding
	// reorders them and the bytes no longer match.
	protected := []byte{0xa2, 0x04, 0x61, 0x78, 0x01, 0x27}
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	_, err := VerifySign1(msg, pub, nil)
	if !errors.Is(err, ErrProtectedNotCanonical) {
		t.Fatalf("want ErrProtectedNotCanonical, got %v", err)
	}
}

func TestVerifySign1_RejectsTamperedPayload(t *testing.T) {
	pub, sign := ed25519Keys(t)
	protected := canonicalProtectedAlg(AlgorithmEdDSA)
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	tampered := append([]byte(nil), msg...)
	// Flip a byte inside the payload region of the encoded message.
	idx := bytes.Index(tampered, testPayload)
	if idx < 0 {
		t.Fatal("payload not found in encoded message")
	}
	tampered[idx] ^= 0x01

	_, err := VerifySign1(tampered, pub, nil)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("want ErrSignatureMismatch, got %v", err)
	}
}

func TestVerifySign1_RejectsWrongKey(t *testing.T) {
	_, sign := ed25519Keys(t)
	otherPub, _ := ed25519Keys(t)
	protected := canonicalProtectedAlg(AlgorithmEdDSA)
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	_, err := VerifySign1(msg, otherPub, nil)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("want ErrSignatureMismatch, got %v", err)
	}
}

func TestVerifySign1_AcceptsES256(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	protected := canonicalProtectedAlg(AlgorithmES256) // a1 01 26
	sign := func(toSign []byte) []byte {
		digest := sha256.Sum256(toSign)
		r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
		if err != nil {
			t.Fatalf("ecdsa.Sign: %v", err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return sig
	}
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	payload, err := VerifySign1(msg, &priv.PublicKey, nil)
	if err != nil {
		t.Fatalf("VerifySign1 rejected a valid ES256 message: %v", err)
	}
	if !bytes.Equal(payload, testPayload) {
		t.Fatalf("payload mismatch: got %x", payload)
	}
}

func TestVerifySign1_RejectsAlgKeyMismatch(t *testing.T) {
	// Protected header says EdDSA but an ECDSA key is supplied.
	_, sign := ed25519Keys(t)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	protected := canonicalProtectedAlg(AlgorithmEdDSA)
	msg := buildSign1(t, protected, Map{}, testPayload, sign)

	if _, err := VerifySign1(msg, &priv.PublicKey, nil); err == nil {
		t.Fatal("VerifySign1 accepted a key that does not match the protected alg")
	}
}

func TestCanonicalize_RoundTrip(t *testing.T) {
	canonical := canonicalProtectedAlg(AlgorithmEdDSA)
	out, err := Canonicalize(canonical)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if !bytes.Equal(out, canonical) {
		t.Fatalf("canonical bytes changed: got %x want %x", out, canonical)
	}

	for name, in := range map[string][]byte{
		"non-minimal map length": {0xb8, 0x01, 0x01, 0x27},
		"non-minimal int":        {0xa1, 0x01, 0x38, 0x07},
		"indefinite map":         {0xbf, 0x01, 0x27, 0xff},
		"duplicate keys":         {0xa2, 0x01, 0x27, 0x01, 0x26},
		"unsorted keys":          {0xa2, 0x04, 0x61, 0x78, 0x01, 0x27},
		"trailing data":          {0xa1, 0x01, 0x27, 0x00},
	} {
		out, err := Canonicalize(in)
		if err == nil && bytes.Equal(out, in) {
			t.Errorf("%s: %x unexpectedly canonical", name, in)
		}
	}
}

func TestSigStructureEncoding(t *testing.T) {
	// RFC 9052-style sanity check on the Sig_structure encoding shape.
	protected := canonicalProtectedAlg(AlgorithmEdDSA)
	got := sigStructure(protected, nil, []byte("payload"))
	wantPrefix := []byte{0x84, 0x6a}
	wantPrefix = append(wantPrefix, "Signature1"...)
	if !bytes.HasPrefix(got, wantPrefix) {
		t.Fatalf("Sig_structure prefix = %x, want prefix %x", got, wantPrefix)
	}
}
