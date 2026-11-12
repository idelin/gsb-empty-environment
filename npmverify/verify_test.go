package npmverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"testing"
)

const (
	pkgName       = "left-pad"
	pkgVersion    = "1.4.2"
	trustedKeyID  = "registry-2026"
	attackerKeyID = "attacker-key"
)

type fixture struct {
	doc      *Packument
	tarball  []byte
	ring     *ECDSAKeyring
	trusted  *ecdsa.PrivateKey
	attacker *ecdsa.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	trusted := generateKey(t)
	attacker := generateKey(t)

	ring, err := NewECDSAKeyring(map[string][]byte{
		trustedKeyID: pemPublic(t, &trusted.PublicKey),
	})
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	tarball := makeTarball(t, pkgName, pkgVersion)
	doc := &Packument{
		Name:     pkgName,
		DistTags: map[string]string{"latest": pkgVersion},
		Versions: map[string]*PkgVersion{
			pkgVersion: signedVersion(t, pkgName, pkgVersion, tarball, trusted, trustedKeyID, "sha512"),
		},
	}

	return &fixture{
		doc:      doc,
		tarball:  tarball,
		ring:     ring,
		trusted:  trusted,
		attacker: attacker,
	}
}

func generateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func pemPublic(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func signMessage(t *testing.T, key *ecdsa.PrivateKey, keyID, message string) Signature {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	der, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return Signature{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(der)}
}

func signedVersion(t *testing.T, name, version string, tarball []byte, key *ecdsa.PrivateKey, keyID, algo string) *PkgVersion {
	t.Helper()
	integrity, err := ComputeIntegrity(algo, tarball)
	if err != nil {
		t.Fatalf("compute integrity: %v", err)
	}
	message := SignatureMessage(name, version, integrity)
	return &PkgVersion{
		Name:    name,
		Version: version,
		Dist: Dist{
			Tarball:    fmt.Sprintf("https://mirror.example/%s/-/%s-%s.tgz", name, name, version),
			Integrity:  integrity,
			Signatures: []Signature{signMessage(t, key, keyID, message)},
		},
	}
}

func makeTarball(t *testing.T, name, version string) []byte {
	t.Helper()
	return makeTarballWith(t, map[string]string{
		"package/package.json": fmt.Sprintf(`{"name":%q,"version":%q}`, name, version),
		"package/index.js":     "module.exports = function () { return true; };\n",
	})
}

func makeTarballWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		header := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyHappyPath(t *testing.T) {
	f := newFixture(t)

	result, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.ResolvedVersion != pkgVersion {
		t.Errorf("resolved version = %q, want %q", result.ResolvedVersion, pkgVersion)
	}
	if result.Algorithm != "sha512" {
		t.Errorf("algorithm = %q, want sha512", result.Algorithm)
	}
	if result.AcceptedKeyID != trustedKeyID {
		t.Errorf("accepted key = %q, want %q", result.AcceptedKeyID, trustedKeyID)
	}
}

func TestVerifySwappedTagUntrustedSignature(t *testing.T) {
	// The mirror repoints "latest" at an attacker release. The attacker's
	// metadata is internally consistent (integrity matches their tarball),
	// but it is signed by a key the proxy does not trust.
	f := newFixture(t)

	evilVersion := "9.9.9"
	evilTarball := makeTarball(t, pkgName, evilVersion)
	evil := signedVersion(t, pkgName, evilVersion, evilTarball, f.attacker, attackerKeyID, "sha512")

	f.doc.Versions[evilVersion] = evil
	f.doc.DistTags["latest"] = evilVersion

	_, err := Verify(context.Background(), f.doc, "latest", evilTarball, f.ring.Verify, nil)
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("err = %v, want ErrSignatureFailed", err)
	}
}

func TestVerifySwappedTagMissingVersion(t *testing.T) {
	// The tag points at a version that was never published in the packument.
	f := newFixture(t)
	f.doc.DistTags["latest"] = "4.2.42"

	_, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil)
	if !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("err = %v, want ErrTagNotFound", err)
	}
}

func TestVerifyTamperedTarball(t *testing.T) {
	// Metadata stays legitimate, but the served bytes were modified: the SRI
	// digest must no longer match, and the failure must surface before any
	// signature or manifest work.
	f := newFixture(t)

	tampered := bytes.Clone(f.tarball)
	tampered[len(tampered)-1] ^= 0xFF

	_, err := Verify(context.Background(), f.doc, "latest", tampered, f.ring.Verify, nil)
	if !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("err = %v, want ErrIntegrityMismatch", err)
	}
}

func TestVerifyWrongVersionInTarball(t *testing.T) {
	// The tarball's package.json claims a different version. Integrity and
	// signature are rebuilt consistently so only the identity check fails.
	f := newFixture(t)

	mismatched := makeTarball(t, pkgName, "1.4.1")
	f.doc.Versions[pkgVersion] = signedVersion(t, pkgName, pkgVersion, mismatched, f.trusted, trustedKeyID, "sha512")

	_, err := Verify(context.Background(), f.doc, "latest", mismatched, f.ring.Verify, nil)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
}

func TestVerifyWrongNameInTarball(t *testing.T) {
	f := newFixture(t)

	mismatched := makeTarball(t, "left-pad-evil", pkgVersion)
	f.doc.Versions[pkgVersion] = signedVersion(t, pkgName, pkgVersion, mismatched, f.trusted, trustedKeyID, "sha512")

	_, err := Verify(context.Background(), f.doc, "latest", mismatched, f.ring.Verify, nil)
	if !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("err = %v, want ErrNameMismatch", err)
	}
}

func TestVerifyPicksStrongestDigest(t *testing.T) {
	// Multiple SRI algorithms: the strongest (sha512) is the one matched and
	// reported, and the signature covers the full combined SRI string.
	f := newFixture(t)

	sha512Integrity, err := ComputeIntegrity("sha512", f.tarball)
	if err != nil {
		t.Fatalf("sha512: %v", err)
	}
	sha256Integrity, err := ComputeIntegrity("sha256", f.tarball)
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}
	combined := sha512Integrity + " " + sha256Integrity

	f.doc.Versions[pkgVersion].Dist.Integrity = combined
	f.doc.Versions[pkgVersion].Dist.Signatures = []Signature{
		signMessage(t, f.trusted, trustedKeyID, SignatureMessage(pkgName, pkgVersion, combined)),
	}

	result, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Algorithm != "sha512" {
		t.Errorf("algorithm = %q, want sha512", result.Algorithm)
	}

	// Mutating the bytes invalidates even the strongest digest.
	tampered := bytes.Clone(f.tarball)
	tampered[10] ^= 0x01
	if _, err := Verify(context.Background(), f.doc, "latest", tampered, f.ring.Verify, nil); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("tampered err = %v, want ErrIntegrityMismatch", err)
	}
}

func TestVerifySignatureRequiredByDefault(t *testing.T) {
	f := newFixture(t)
	f.doc.Versions[pkgVersion].Dist.Signatures = nil

	if _, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil); !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("default err = %v, want ErrSignatureFailed", err)
	}

	off := false
	if _, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, &Options{RequireSignature: &off}); err != nil {
		t.Fatalf("RequireSignature=false: %v", err)
	}
}

func TestVerifyRejectsBadInputs(t *testing.T) {
	f := newFixture(t)

	if _, err := Verify(context.Background(), nil, "latest", f.tarball, f.ring.Verify, nil); !errors.Is(err, ErrNoPackument) {
		t.Fatalf("nil doc: %v", err)
	}
	if _, err := Verify(context.Background(), f.doc, "latest", f.tarball, nil, nil); !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("nil verifier: %v", err)
	}
	if _, err := Verify(context.Background(), f.doc, "nonexistent-tag", f.tarball, f.ring.Verify, nil); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("missing tag: %v", err)
	}

	f.doc.Versions[pkgVersion].Dist.Integrity = "not-an-sri"
	if _, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil); !errors.Is(err, ErrMalformedIntegrity) {
		t.Fatalf("bad integrity: %v", err)
	}
}

func TestResolveTag(t *testing.T) {
	f := newFixture(t)

	version, pkg, err := ResolveTag(f.doc, "latest")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	if version != pkgVersion || pkg.Version != pkgVersion {
		t.Fatalf("resolved %q / %+v", version, pkg)
	}

	if _, _, err := ResolveTag(nil, "latest"); !errors.Is(err, ErrNoPackument) {
		t.Fatalf("nil doc: %v", err)
	}
	if _, _, err := ResolveTag(f.doc, "beta"); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("unknown tag: %v", err)
	}

	f.doc.DistTags["latest"] = "ghost"
	if _, _, err := ResolveTag(f.doc, "latest"); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("dangling tag: %v", err)
	}
}
