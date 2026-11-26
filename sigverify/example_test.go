package sigverify_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"testing"
	"time"

	"formal.local/w27-3ba2725682/sigverify"
)

// TestExampleOfflineVerify drives the public API end to end without any
// network access or external module, demonstrating the intended host
// integration path.
func TestExampleOfflineVerify(t *testing.T) {
	// Trust anchors provisioned out-of-band by the host.
	rootKey, rootDER := selfSignedRoot(t)
	logPub, logPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	artifact := []byte("container-image-layer:example@sha256:deadbeef")
	digest := sha256.Sum256(artifact)

	// A short-lived Fulcio-style leaf (10 minute window) with CodeSigning EKU.
	notBefore := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	leaf, leafKey, leafDER := issueLeaf(t, rootKey, rootDER, notBefore, notBefore.Add(10*time.Minute))
	signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	entry := sigverify.LogEntry{
		ArtifactDigest: sigverify.Digest{Algorithm: "SHA-256", Value: digest[:]},
		Signature:      signature,
		CertificateDER: leafDER,
		SignatureAlgo:  "ECDSA-P256-SHA256",
		LogEntryType:   "hashedrekord",
	}
	canonical, err := entry.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	leafHash := sigverify.LeafHash(canonical)

	// Single-leaf tree: the leaf hash is also the root.
	treeSize := uint64(1)
	checkpoint := signCheckpoint(t, "rekor.example.test", treeSize, leafHash, logPriv)

	bundle := &sigverify.Bundle{
		Version:            sigverify.BundleVersion,
		ArtifactDigest:     sigverify.Digest{Algorithm: "SHA-256", Value: digest[:]},
		ArtifactSignature:  signature,
		LeafCertificateDER: leafDER,
		LogEntry:           entry,
		IntegratedTime:     notBefore.Add(5 * time.Minute).Unix(),
		InclusionProof: sigverify.InclusionProof{
			LogIndex: 0,
			TreeSize: treeSize,
		},
		Checkpoint: checkpoint,
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sigverify.ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}

	trust := sigverify.TrustedMaterial{
		FulcioRoots:              [][]byte{rootDER},
		RekorPublicKey:           logPub,
		ExpectedCheckpointOrigin: "rekor.example.test",
	}
	policy := sigverify.Policy{
		AllowedOIDCIssuers: []string{"https://accounts.example.com"},
		AllowedSubjects:    []string{"spiffe://example.com/builder"},
	}

	result, err := sigverify.Verify(parsed, artifact, trust, policy)
	if err != nil {
		t.Fatalf("offline verification failed: %v", err)
	}
	fmt.Printf("verified issuer=%s subject=%s notAfter=%s\n",
		result.OIDCIssuer, result.Subjects[0], leaf.NotAfter.Format(time.RFC3339))
	// Output: verified issuer=https://accounts.example.com subject=spiffe://example.com/builder notAfter=2026-11-27T09:10:00Z
}

func selfSignedRoot(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Example Fulcio Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

func issueLeaf(t *testing.T, rootKey *ecdsa.PrivateKey, rootDER []byte, notBefore, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("spiffe://example.com/builder")
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "example-leaf"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		URIs:         []*url.URL{uri},
		ExtraExtensions: []pkix.Extension{{
			Id:    sigverify.OIDIssuerV1,
			Value: mustMarshalString(t, "https://accounts.example.com"),
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, der
}

func signCheckpoint(t *testing.T, origin string, size uint64, root []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	body := []byte(fmt.Sprintf("%s\n%d\n%s\n\n", origin, size, base64Encode(root)))
	signature := ed25519.Sign(key, body)
	return append(append(body, []byte("\u2014 \u2014 \u2014 example-key "+base64Encode(signature))...), '\n')
}

func mustMarshalString(t *testing.T, value string) []byte {
	t.Helper()
	data, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
