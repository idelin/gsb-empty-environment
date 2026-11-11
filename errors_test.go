package knownhosts

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorStringsAndWrapping(t *testing.T) {
	if got := (&RevokedError{Line: 7, KeyType: "ssh-rsa"}).Error(); !strings.Contains(got, "revoked ssh-rsa host key (line 7)") {
		t.Fatalf("unexpected revoked error: %q", got)
	}
	if got := (&KeyError{}).Error(); got != "knownhosts: host key verification failed" {
		t.Fatalf("unexpected default key error: %q", got)
	}

	parseErr := &ParseError{Line: 3, Err: errMalformed}
	if !strings.Contains(parseErr.Error(), "malformed line 3") {
		t.Fatalf("unexpected parse error: %q", parseErr.Error())
	}
	if !errors.Is(parseErr, errMalformed) {
		t.Fatal("ParseError must unwrap to the underlying error")
	}

	keyErr := &KeyError{Reason: "host key mismatch for h"}
	var target *KeyError
	if !errors.As(fmt.Errorf("wrap: %w", keyErr), &target) {
		t.Fatal("KeyError should support errors.As")
	}
}

func TestPaddedBase64HashAccepted(t *testing.T) {
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}

	kh, err := NewFromReader(strings.NewReader(encodeLine("pad.example.com", key)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Rebuild a hashed entry with standard (padded) base64 to exercise the
	// tolerant decoder.
	host := normalizeHost("pad.example.com")
	salt := []byte("01234567890123456789")
	digest := hashHost(salt, canonicalAddr(host, 22))
	token := "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" +
		base64.StdEncoding.EncodeToString(digest)
	hashedKH, err := NewFromReader(strings.NewReader(token + " " + encodeB64(key)))
	if err != nil {
		t.Fatalf("parse padded hash: %v", err)
	}
	if err := hashedKH.HostKeyCallback("pad.example.com", 22, key); err != nil {
		t.Fatalf("padded hash should match: %v", err)
	}
	if err := kh.HostKeyCallback("pad.example.com", 22, key); err != nil {
		t.Fatalf("plain line should still match: %v", err)
	}
}

func TestMalformedCertificateRejected(t *testing.T) {
	caPubRaw, caPriv := ed25519KeyPair()
	caKey := testPublicKey{"ssh-ed25519", marshalEd25519(caPubRaw)}

	kh, err := NewFromReader(strings.NewReader("@cert-authority broken.example.com " + encodeB64(caKey)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Truncated certificate blob: parse fails inside the callback, which then
	// falls back to plain-key matching and must not accept.
	truncated := testPublicKey{keyType: "ssh-ed25519-cert-v01@openssh.com", blob: []byte{0x00, 0x00, 0x00, 0x04}}
	err = kh.HostKeyCallback("broken.example.com", 22, truncated)
	var keyError *KeyError
	if !errors.As(err, &keyError) {
		t.Fatalf("expected KeyError, got %v", err)
	}

	// A well-formed certificate whose recorded CA entry is missing.
	hostPub, _ := ed25519KeyPair()
	cert := buildEd25519Certificate(hostPub, caPriv, caPubRaw, []string{"broken.example.com"}, false)
	otherPub, _ := ed25519KeyPair()
	wrongCA := testPublicKey{"ssh-ed25519", marshalEd25519(otherPub)}
	wrongKH, err := NewFromReader(strings.NewReader("@cert-authority broken.example.com " + encodeB64(wrongCA)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := wrongKH.HostKeyCallback("broken.example.com", 22, cert); err == nil {
		t.Fatal("certificate from another CA must be rejected")
	}
}

func TestRSAAndECDSASignatureFailures(t *testing.T) {
	rsaCA := rsaKeyPair()
	rsaCAKey := testPublicKey{"ssh-rsa", marshalRSA(&rsaCA.PublicKey)}
	otherRSA := rsaKeyPair()

	kh, err := NewFromReader(strings.NewReader("@cert-authority rsa-host.example.com " + encodeB64(rsaCAKey)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Build a cert signed by a different CA but stored against rsaCAKey.
	cert := buildRSACertificate(&otherRSA.PublicKey, otherRSA, []string{"rsa-host.example.com"})
	err = kh.HostKeyCallback("rsa-host.example.com", 22, cert)
	if err == nil || !strings.Contains(err.Error(), "certificate authority") {
		t.Fatalf("expected CA mismatch error, got %v", err)
	}

	ecdsaCA := ecdsaKeyPair()
	ecdsaCAKey := testPublicKey{"ecdsa-sha2-nistp256", marshalECDSA(&ecdsaCA.PublicKey)}
	otherECDSA := ecdsaKeyPair()
	kh2, err := NewFromReader(strings.NewReader("@cert-authority ecdsa-host.example.com " + encodeB64(ecdsaCAKey)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cert2 := buildECDSACertificate(&otherECDSA.PublicKey, otherECDSA, []string{"ecdsa-host.example.com"})
	if err := kh2.HostKeyCallback("ecdsa-host.example.com", 22, cert2); err == nil {
		t.Fatal("ECDSA cert signed by another CA must be rejected")
	}
}

func TestLineErrors(t *testing.T) {
	if _, err := Line("onlyhost"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := Line("host AAAA"); err == nil {
		t.Fatal("expected invalid blob error")
	}

	// Raw key accessors round-trip through the KnownKey view.
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
	got, err := Line(encodeLine("h.example.com", key))
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if got.Key.Type() != "ssh-ed25519" || string(got.Key.Marshal()) != string(key.blob) {
		t.Fatal("KnownKey does not round-trip the wire blob")
	}
}

func TestUnsupportedSignatureFormat(t *testing.T) {
	ca := rsaKeyPair()

	cert := buildRSACertificate(&ca.PublicKey, ca, []string{"u.example.com"})
	// Corrupt the signature algorithm name inside the blob so verification
	// reports an unsupported format rather than a signature mismatch.
	corrupt := rebuildSigFormat(t, cert.blob, "rsa-sha2-999")

	parsed, err := parseCertificate(corrupt)
	if err != nil {
		t.Fatalf("parseCertificate: %v", err)
	}
	if err := verifyCertSignature(parsed); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}

	// ECDSA point that cannot be parsed.
	bad := buildUnknownCurveBlob()
	if _, _, err := parseECDSAPublicKey(bad); err == nil {
		t.Fatal("expected ECDSA parse error for unknown curve")
	}
}

// rebuildSigFormat replaces the signature algorithm name inside a certificate
// blob, rebuilding its containing string lengths correctly.
func rebuildSigFormat(t *testing.T, blob []byte, format string) []byte {
	t.Helper()
	parsed, err := parseCertificate(blob)
	if err != nil {
		t.Fatalf("parseCertificate: %v", err)
	}

	// Re-encode the signature field with the new algorithm name; the signed
	// prefix of the certificate is unchanged.
	var sigField []byte
	sigField = putString(sigField, []byte(format))
	sigField = putString(sigField, parsed.sigBlob)

	out := append([]byte(nil), parsed.signed...)
	out = putString(out, sigField)
	return out
}

func buildUnknownCurveBlob() []byte {
	var blob []byte
	blob = putString(blob, []byte("ecdsa-sha2-nistp256"))
	blob = putString(blob, []byte("nistbogus"))
	blob = putString(blob, []byte("not-a-point"))
	return blob
}
