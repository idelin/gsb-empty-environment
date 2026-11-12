package netsec

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func randKey(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, keySize)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	return buf
}

func newEngineWithKey(t *testing.T, name string) *Engine {
	t.Helper()
	e := NewEngine()
	if err := e.CreateKey(KeyConfig{Name: name}, randKey(t)); err != nil {
		t.Fatalf("create key: %v", err)
	}
	return e
}

func ciphertextVersion(t *testing.T, ciphertext string) uint32 {
	t.Helper()
	version, _, err := parseCiphertext(ciphertext)
	if err != nil {
		t.Fatalf("parse ciphertext: %v", err)
	}
	return version
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	plaintext := []byte("card=4111-1111-1111-1111")

	ciphertext, err := e.Encrypt("orders", plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "vault:v1:") {
		t.Fatalf("unexpected ciphertext format: %q", ciphertext)
	}
	got, err := e.Decrypt("orders", ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestTamperedVersionPrefix(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	if _, err := e.Rotate("orders", randKey(t)); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	ciphertext, err := e.Encrypt("orders", []byte("secret"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if v := ciphertextVersion(t, ciphertext); v != 2 {
		t.Fatalf("expected v2 ciphertext, got v%d", v)
	}

	// Attacker downgrades the prefix v2 -> v1 to force the deprecated key.
	downgraded := strings.Replace(ciphertext, "vault:v2:", "vault:v1:", 1)
	if _, err := e.Decrypt("orders", downgraded, nil); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("downgraded prefix: want ErrDecryptionFailed, got %v", err)
	}

	// Attacker bumps the prefix to a version that does not exist.
	bumped := strings.Replace(ciphertext, "vault:v2:", "vault:v3:", 1)
	if _, err := e.Decrypt("orders", bumped, nil); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("bumped prefix: want ErrVersionNotFound, got %v", err)
	}

	// The untouched ciphertext still decrypts.
	if _, err := e.Decrypt("orders", ciphertext, nil); err != nil {
		t.Fatalf("decrypt original: %v", err)
	}
}

func TestCrossKeyNameSwap(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	if err := e.CreateKey(KeyConfig{Name: "payments"}, randKey(t)); err != nil {
		t.Fatalf("create key: %v", err)
	}
	ciphertext, err := e.Encrypt("orders", []byte("secret"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Ciphertext minted for "orders" must not decrypt under "payments",
	// because the key name is bound into the AEAD additional data.
	if _, err := e.Decrypt("payments", ciphertext, nil); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("cross-key decrypt: want ErrDecryptionFailed, got %v", err)
	}
	if _, err := e.Decrypt("orders", ciphertext, nil); err != nil {
		t.Fatalf("same-key decrypt: %v", err)
	}
}

func TestMinDecryptionVersion(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	oldCiphertext, err := e.Encrypt("orders", []byte("old-secret"), nil)
	if err != nil {
		t.Fatalf("encrypt v1: %v", err)
	}
	if _, err := e.Rotate("orders", randKey(t)); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := e.SetMinVersions("orders", 2, 0); err != nil {
		t.Fatalf("set min versions: %v", err)
	}

	// v1 ciphertext is now below min_decryption_version and rejected even
	// though it is authentic.
	if _, err := e.Decrypt("orders", oldCiphertext, nil); !errors.Is(err, ErrBelowMinDecryptionVersion) {
		t.Fatalf("deprecated version: want ErrBelowMinDecryptionVersion, got %v", err)
	}

	// New v2 ciphertext still decrypts.
	newCiphertext, err := e.Encrypt("orders", []byte("new-secret"), nil)
	if err != nil {
		t.Fatalf("encrypt v2: %v", err)
	}
	got, err := e.Decrypt("orders", newCiphertext, nil)
	if err != nil {
		t.Fatalf("decrypt v2: %v", err)
	}
	if string(got) != "new-secret" {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestMinEncryptionVersion(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	if _, err := e.Rotate("orders", randKey(t)); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := e.SetMinVersions("orders", 0, 2); err != nil {
		t.Fatalf("set min versions: %v", err)
	}
	ciphertext, err := e.Encrypt("orders", []byte("secret"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if v := ciphertextVersion(t, ciphertext); v < 2 {
		t.Fatalf("min_encryption_version violated: encrypted at v%d", v)
	}
}

func TestRewrap(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	plaintext := []byte("rewrap-me")
	oldCiphertext, err := e.Encrypt("orders", plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := e.Rotate("orders", randKey(t)); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Rewrap to the latest version; plaintext never leaves the engine.
	newCiphertext, err := e.Rewrap("orders", oldCiphertext, nil, 0)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if v := ciphertextVersion(t, newCiphertext); v != 2 {
		t.Fatalf("rewrap version: want v2, got v%d", v)
	}
	if newCiphertext == oldCiphertext {
		t.Fatal("rewrap returned the original ciphertext")
	}
	got, err := e.Decrypt("orders", newCiphertext, nil)
	if err != nil {
		t.Fatalf("decrypt rewrapped: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("rewrap round trip mismatch: %q", got)
	}

	// Rewrap to an explicit target below min_encryption_version is refused.
	if err := e.SetMinVersions("orders", 0, 2); err != nil {
		t.Fatalf("set min versions: %v", err)
	}
	if _, err := e.Rewrap("orders", newCiphertext, nil, 1); !errors.Is(err, ErrBelowMinEncryptionVersion) {
		t.Fatalf("rewrap below min_encryption_version: want ErrBelowMinEncryptionVersion, got %v", err)
	}

	// Rewrap of a deprecated ciphertext is refused like a decryption.
	if err := e.SetMinVersions("orders", 2, 0); err != nil {
		t.Fatalf("set min versions: %v", err)
	}
	if _, err := e.Rewrap("orders", oldCiphertext, nil, 0); !errors.Is(err, ErrBelowMinDecryptionVersion) {
		t.Fatalf("rewrap deprecated: want ErrBelowMinDecryptionVersion, got %v", err)
	}
}

func TestConvergentEncryptionContext(t *testing.T) {
	e := NewEngine()
	if err := e.CreateKey(KeyConfig{Name: "ssn", Convergent: true}, randKey(t)); err != nil {
		t.Fatalf("create key: %v", err)
	}
	plaintext := []byte("123-45-6789")
	ctxAlice := []byte("user:alice")
	ctxBob := []byte("user:bob")

	ct1, err := e.Encrypt("ssn", plaintext, ctxAlice)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct2, err := e.Encrypt("ssn", plaintext, ctxAlice)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if ct1 != ct2 {
		t.Fatal("convergent encryption not deterministic for same context")
	}

	// The context participates in subkey derivation: a different context
	// yields a different ciphertext and cannot decrypt Alice's ciphertext.
	ctBob, err := e.Encrypt("ssn", plaintext, ctxBob)
	if err != nil {
		t.Fatalf("encrypt bob: %v", err)
	}
	if ctBob == ct1 {
		t.Fatal("different contexts produced identical ciphertexts")
	}
	if _, err := e.Decrypt("ssn", ct1, ctxBob); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("wrong context: want ErrDecryptionFailed, got %v", err)
	}

	got, err := e.Decrypt("ssn", ct1, ctxAlice)
	if err != nil {
		t.Fatalf("decrypt with context: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("convergent round trip mismatch: %q", got)
	}

	// Convergent keys require a context.
	if _, err := e.Encrypt("ssn", plaintext, nil); !errors.Is(err, ErrConvergentRequiresContext) {
		t.Fatalf("missing context: want ErrConvergentRequiresContext, got %v", err)
	}
}

func TestMalformedCiphertext(t *testing.T) {
	e := newEngineWithKey(t, "orders")
	for _, ciphertext := range []string{
		"",
		"vault",
		"vault:v1",
		"vault:v0:AAAA",
		"vault:vX:AAAA",
		"vault:v1:not-base64!!!",
		"other:v1:AAAA",
	} {
		if _, err := e.Decrypt("orders", ciphertext, nil); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("decrypt %q: want ErrInvalidCiphertext, got %v", ciphertext, err)
		}
	}
}
