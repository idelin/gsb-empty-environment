package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  PublicKey
}

func newTestSigner(t *testing.T, keyNum [8]byte) testSigner {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	configured, err := NewPublicKey(keyNum[:], publicKey)
	if err != nil {
		t.Fatalf("construct public key: %v", err)
	}
	return testSigner{privateKey: privateKey, publicKey: configured}
}

func (signer testSigner) sign(body []byte) []byte {
	record := append([]byte(algorithmEd), signer.publicKey.KeyNum[:]...)
	record = append(record, ed25519.Sign(signer.privateKey, body)...)
	encoded := base64.StdEncoding.EncodeToString(record)
	return []byte("untrusted comment: verify with release key\n" + encoded + "\n" + string(body))
}

func manifestLine(name string, content []byte) string {
	digest := sha256.Sum256(content)
	return fmt.Sprintf("SHA256 (%s) = %x\n", name, digest)
}

func verifyBody(t *testing.T, signer testSigner, body []byte) (*Manifest, error) {
	t.Helper()
	return verifyBodyBytes(t, signer, signer.sign(body))
}

func TestVerifierRejectsDifferentKey(t *testing.T) {
	trusted := newTestSigner(t, [8]byte{1})
	attacker := newTestSigner(t, [8]byte{2})
	body := []byte(manifestLine("update.bin", []byte("payload")))

	_, err := verifyBodyBytes(t, trusted, attacker.sign(body))
	if !errors.Is(err, ErrKeyNumMismatch) {
		t.Fatalf("different key number: got %v, want ErrKeyNumMismatch", err)
	}

	// A signature can carry the trusted key number while being made by
	// another key. The key number check must not replace signature checking.
	other := newTestSigner(t, trusted.publicKey.KeyNum)
	_, err = verifyBodyBytes(t, trusted, other.sign(body))
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("same key number, different key: got %v, want ErrInvalidSignature", err)
	}
}

func TestVerifierRejectsTamperedBody(t *testing.T) {
	signer := newTestSigner(t, [8]byte{9})
	body := []byte(manifestLine("update.bin", []byte("payload")))
	signed := signer.sign(body)
	tampered := []byte(strings.Replace(string(signed), "update.bin", "../update.bin", 1))

	_, err := verifyBodyBytes(t, signer, tampered)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered authenticated body: got %v, want ErrInvalidSignature", err)
	}
}

func TestVerifierIgnoresUntrustedComment(t *testing.T) {
	signer := newTestSigner(t, [8]byte{3})
	body := []byte(manifestLine("update.bin", []byte("payload")))
	signed := signer.sign(body)

	firstNewline := strings.IndexByte(string(signed), '\n')
	tampered := "untrusted comment: attacker-controlled wording, key 999\n" + string(signed[firstNewline+1:])

	manifest, err := verifyBodyBytes(t, signer, []byte(tampered))
	if err != nil {
		t.Fatalf("tampered comment rejected: %v", err)
	}
	digest, ok := manifest.Digest("update.bin")
	if !ok || digest != sha256.Sum256([]byte("payload")) {
		t.Fatalf("manifest entry was not parsed after comment change")
	}
}

func verifyBodyBytes(t *testing.T, signer testSigner, signed []byte) (*Manifest, error) {
	t.Helper()
	verifier, err := NewVerifier(signer.publicKey)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return verifier.Verify(signed)
}

func TestManifestRejectsUnsafeNames(t *testing.T) {
	signer := newTestSigner(t, [8]byte{4})
	body := []byte("payload")

	unsafeNames := []string{
		"../escape",
		"/absolute",
		"dir/file",
		`dir\file`,
		".",
		"..",
		"nul\x00name",
		"",
	}
	for _, name := range unsafeNames {
		t.Run(fmt.Sprintf("name_%q", name), func(t *testing.T) {
			manifestBody := []byte(manifestLine(name, body))
			_, err := verifyBody(t, signer, manifestBody)
			if !errors.Is(err, ErrMalformedManifest) || !errors.Is(err, ErrUnsafeFileName) {
				t.Fatalf("unsafe name %q: got %v, want malformed manifest / unsafe name", name, err)
			}
		})
	}
}

func TestManifestRejectsConflictingDuplicate(t *testing.T) {
	signer := newTestSigner(t, [8]byte{5})
	body := []byte(
		manifestLine("update.bin", []byte("first")) +
			manifestLine("update.bin", []byte("second")),
	)

	_, err := verifyBody(t, signer, body)
	if !errors.Is(err, ErrConflictingEntry) {
		t.Fatalf("conflicting duplicate: got %v, want ErrConflictingEntry", err)
	}
}

func TestManifestAllowsIdenticalDuplicate(t *testing.T) {
	signer := newTestSigner(t, [8]byte{6})
	entry := manifestLine("update.bin", []byte("same"))
	body := []byte(entry + entry)

	manifest, err := verifyBody(t, signer, body)
	if err != nil {
		t.Fatalf("identical duplicate rejected: %v", err)
	}
	if manifest.Len() != 1 {
		t.Fatalf("manifest has %d entries, want 1", manifest.Len())
	}
}

func TestDownloadChecksOnlyListedNames(t *testing.T) {
	signer := newTestSigner(t, [8]byte{7})
	allowedContent := []byte("allowed payload")
	body := []byte(manifestLine("allowed.bin", allowedContent))

	manifest, err := verifyBody(t, signer, body)
	if err != nil {
		t.Fatalf("verify manifest: %v", err)
	}

	if err := manifest.VerifyDownload("allowed.bin", allowedContent); err != nil {
		t.Fatalf("listed file rejected: %v", err)
	}
	if err := manifest.VerifyDownload("unlisted.bin", allowedContent); !errors.Is(err, ErrFileNotListed) {
		t.Fatalf("unlisted file: got %v, want ErrFileNotListed", err)
	}
	if err := manifest.VerifyDownload("../allowed.bin", allowedContent); !errors.Is(err, ErrUnsafeFileName) {
		t.Fatalf("path-like download name: got %v, want ErrUnsafeFileName", err)
	}
	if err := manifest.VerifyDownload("allowed.bin", []byte("tampered")); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("bad checksum: got %v, want ErrChecksumMismatch", err)
	}

	path := filepath.Join(t.TempDir(), "downloaded.bin")
	if err := os.WriteFile(path, allowedContent, 0o600); err != nil {
		t.Fatalf("write download: %v", err)
	}
	if err := manifest.VerifyDownloadFile("allowed.bin", path); err != nil {
		t.Fatalf("listed download file rejected: %v", err)
	}
	if err := manifest.VerifyDownloadFile("unlisted.bin", path); !errors.Is(err, ErrFileNotListed) {
		t.Fatalf("unlisted download file: got %v, want ErrFileNotListed", err)
	}
}

func TestParsePublicKey(t *testing.T) {
	signer := newTestSigner(t, [8]byte{8})
	record := append([]byte(algorithmEd), signer.publicKey.KeyNum[:]...)
	record = append(record, signer.publicKey.Key...)
	data := []byte("untrusted comment: public key\n" + base64.StdEncoding.EncodeToString(record) + "\n")

	parsed, err := ParsePublicKey(data)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if parsed.KeyNum != signer.publicKey.KeyNum || !bytes.Equal(parsed.Key, signer.publicKey.Key) {
		t.Fatalf("parsed public key does not match source")
	}
}
