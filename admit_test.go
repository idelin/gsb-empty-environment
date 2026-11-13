package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fixedNow is frozen well inside the generated certificates' validity window.
var fixedNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// ed25519Verifier is the caller-injected Verifier backed by the standard
// library. A production relay would implement the same interface on top of
// its own key store.
type ed25519Verifier struct{}

func (ed25519Verifier) VerifyDescriptorCert(cert *SigningCertificate, blindedKey ed25519.PublicKey) error {
	if !ed25519.Verify(blindedKey, cert.CertSignedPart(), cert.Signature) {
		return errors.New("certificate signature mismatch")
	}
	return nil
}

func (ed25519Verifier) VerifyDescriptorSignature(signingKey ed25519.PublicKey, message, signature []byte) error {
	if !ed25519.Verify(signingKey, message, signature) {
		return errors.New("descriptor signature mismatch")
	}
	return nil
}

type testKeys struct {
	blindedPublic  ed25519.PublicKey
	blindedPrivate ed25519.PrivateKey
	signPublic     ed25519.PublicKey
	signPrivate    ed25519.PrivateKey
}

func mustGenerateKeys(t *testing.T) testKeys {
	t.Helper()
	blindedPublic, blindedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate blinded key: %v", err)
	}
	signPublic, signPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return testKeys{
		blindedPublic:  blindedPublic,
		blindedPrivate: blindedPrivate,
		signPublic:     signPublic,
		signPrivate:    signPrivate,
	}
}

// buildCertificate constructs a binary Tor Ed25519 signing-key certificate
// signed by the blinded key, mirroring cert-spec.txt section 4.
func buildCertificate(keys testKeys, expiration time.Time, certType CertType, keyType CertKeyType, extensions []CertExtension) []byte {
	var body bytes.Buffer
	body.WriteByte(1) // certificate version
	body.WriteByte(byte(certType))
	_ = binary.Write(&body, binary.BigEndian, uint32(expiration.Unix()/3600))
	body.WriteByte(byte(keyType))
	_ = binary.Write(&body, binary.BigEndian, uint16(ed25519.PublicKeySize))
	body.Write(keys.signPublic)
	body.WriteByte(byte(len(extensions)))
	for _, extension := range extensions {
		body.WriteByte(extension.Type)
		body.WriteByte(extension.Flags)
		_ = binary.Write(&body, binary.BigEndian, uint16(len(extension.Value)))
		body.Write(extension.Value)
	}
	signature := ed25519.Sign(keys.blindedPrivate, body.Bytes())
	return append(body.Bytes(), signature...)
}

// descriptorSpec controls buildDescriptor.
type descriptorSpec struct {
	version         uint
	revisionCounter uint64
	lifetime        uint64
	certDER         []byte
	tamperBody      bool
	tamperSignature bool
}

// buildDescriptor renders a signed textual v3 descriptor and returns its raw
// bytes. The signature always covers SignaturePrefix plus the exact body text.
func buildDescriptor(t *testing.T, keys testKeys, spec descriptorSpec) []byte {
	t.Helper()

	var body bytes.Buffer
	fmt.Fprintf(&body, "hs-descriptor %d\n", spec.version)
	fmt.Fprintf(&body, "descriptor-lifetime %d\n", spec.lifetime)
	fmt.Fprintf(&body, "descriptor-end-time %s\n",
		fixedNow.Add(time.Duration(spec.lifetime)*time.Second).UTC().Format("2006-01-02 15:04:05"))
	body.WriteString("descriptor-signing-key-cert\n")
	body.WriteString("-----BEGIN ED25519 CERT-----\n")
	encodedCert := base64.StdEncoding.EncodeToString(spec.certDER)
	for len(encodedCert) > 0 {
		chunk := encodedCert
		if len(chunk) > 64 {
			chunk = encodedCert[:64]
			encodedCert = encodedCert[64:]
		} else {
			encodedCert = ""
		}
		body.WriteString(chunk + "\n")
	}
	body.WriteString("-----END ED25519 CERT-----\n")
	fmt.Fprintf(&body, "revision-counter %d\n", spec.revisionCounter)

	bodyForSignature := body.Bytes()
	if spec.tamperBody {
		bodyForSignature = bytes.Clone(bodyForSignature)
		bodyForSignature = bytes.Replace(
			bodyForSignature,
			[]byte(fmt.Sprintf("revision-counter %d", spec.revisionCounter)),
			[]byte(fmt.Sprintf("revision-counter %d", spec.revisionCounter+1000)),
			1,
		)
	}

	message := append([]byte(SignaturePrefix), bodyForSignature...)
	signature := ed25519.Sign(keys.signPrivate, message)
	if spec.tamperSignature {
		signature[0] ^= 0xFF
	}

	var descriptor bytes.Buffer
	descriptor.Write(body.Bytes())
	descriptor.WriteString("descriptor-signing-key-signature\n")
	descriptor.WriteString(base64.StdEncoding.EncodeToString(signature) + "\n")
	descriptor.WriteString("superencrypted\n")
	descriptor.WriteString("-----BEGIN MESSAGE-----\n")
	descriptor.WriteString(base64.StdEncoding.EncodeToString([]byte("encrypted inner section")) + "\n")
	descriptor.WriteString("-----END MESSAGE-----\n")
	return descriptor.Bytes()
}

func newTestAdmitter() *Admitter {
	return NewAdmitter(ed25519Verifier{}, WithClock(func() time.Time { return fixedNow }))
}

func validSpec(keys testKeys) descriptorSpec {
	return descriptorSpec{
		version:         DescriptorVersion,
		revisionCounter: 10,
		lifetime:        10800,
		certDER: buildCertificate(
			keys,
			fixedNow.Add(30*time.Hour),
			CertTypeSigningKey,
			CertKeyTypeEd25519,
			[]CertExtension{{Type: 4, Flags: 0, Value: []byte("signing-key-extension")}},
		),
	}
}

func admitParsed(t *testing.T, admitter *Admitter, raw []byte, blindedKey ed25519.PublicKey) (*Descriptor, error) {
	t.Helper()
	desc, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatalf("ParseDescriptor failed: %v", err)
	}
	return desc, admitter.Admit(desc, blindedKey)
}

func TestAdmitAcceptsFreshDescriptor(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	desc, err := admitParsed(t, admitter, buildDescriptor(t, keys, validSpec(keys)), keys.blindedPublic)
	if err != nil {
		t.Fatalf("expected admission, got %v", err)
	}
	if desc.Version != DescriptorVersion {
		t.Fatalf("parsed version = %d", desc.Version)
	}
	if desc.RevisionCounter != 10 {
		t.Fatalf("parsed revision = %d", desc.RevisionCounter)
	}
	if cached, ok := admitter.CachedRevision(keys.blindedPublic); !ok || cached != 10 {
		t.Fatalf("cached revision = (%d, %v), want (10, true)", cached, ok)
	}

	next := validSpec(keys)
	next.revisionCounter = 11
	if _, err := admitParsed(t, admitter, buildDescriptor(t, keys, next), keys.blindedPublic); err != nil {
		t.Fatalf("expected higher revision to be admitted, got %v", err)
	}
	if cached, _ := admitter.CachedRevision(keys.blindedPublic); cached != 11 {
		t.Fatalf("cached revision = %d, want 11", cached)
	}
}

func TestAdmitRejectsTamperedVersion(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	for _, version := range []uint{2, 4} {
		spec := validSpec(keys)
		spec.version = version
		_, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("version %d error = %v, want ErrUnsupportedVersion", version, err)
		}
	}

	if _, ok := admitter.CachedRevision(keys.blindedPublic); ok {
		t.Fatal("rejected descriptor must not populate the revision cache")
	}
}

func TestAdmitRejectsExpiredCertificate(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	// One hour before the frozen clock: structurally valid but stale.
	spec := validSpec(keys)
	spec.certDER = buildCertificate(keys, fixedNow.Add(-time.Hour), CertTypeSigningKey, CertKeyTypeEd25519, nil)
	_, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
	if !errors.Is(err, ErrCertificateExpired) {
		t.Fatalf("error = %v, want ErrCertificateExpired", err)
	}

	// Boundary: expiration exactly at "now" is rejected as well (strictly after).
	spec.certDER = buildCertificate(keys, fixedNow, CertTypeSigningKey, CertKeyTypeEd25519, nil)
	_, err = admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
	if !errors.Is(err, ErrCertificateExpired) {
		t.Fatalf("boundary error = %v, want ErrCertificateExpired", err)
	}
}

func TestAdmitRejectsTamperedBody(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	// The signed body is modified after signing while the signature stays valid
	// looking: verification over SignaturePrefix+body must fail.
	spec := validSpec(keys)
	spec.tamperBody = true
	_, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want ErrBadSignature", err)
	}

	// A flipped signature byte over an intact body must fail too, proving the
	// injected verifier actually checks the detached signature.
	spec = validSpec(keys)
	spec.tamperSignature = true
	_, err = admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("signature error = %v, want ErrBadSignature", err)
	}
}

func TestAdmitRejectsRevisionRollback(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	spec := validSpec(keys)
	spec.revisionCounter = 100
	if _, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic); err != nil {
		t.Fatalf("seed admission failed: %v", err)
	}

	for _, rolledBack := range []uint64{99, 100} {
		spec.revisionCounter = rolledBack
		_, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
		if !errors.Is(err, ErrRevisionRollback) {
			t.Fatalf("counter %d error = %v, want ErrRevisionRollback", rolledBack, err)
		}
	}

	cached, ok := admitter.CachedRevision(keys.blindedPublic)
	if !ok || cached != 100 {
		t.Fatalf("cache after rollback = (%d, %v), want (100, true)", cached, ok)
	}

	spec.revisionCounter = 101
	if _, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic); err != nil {
		t.Fatalf("higher revision should be admitted: %v", err)
	}
}

func TestAdmitRejectsWrongPeriodBlindedKey(t *testing.T) {
	keys := mustGenerateKeys(t)
	other := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	// The certificate is signed by keys.blindedPublic; admitting against the
	// other key (e.g. a previous/next time-period blinded key) must fail.
	_, err := admitParsed(t, admitter, buildDescriptor(t, keys, validSpec(keys)), other.blindedPublic)
	if !errors.Is(err, ErrCertificateSignature) {
		t.Fatalf("error = %v, want ErrCertificateSignature", err)
	}
}

func TestAdmitRejectsWrongCertificateType(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	spec := validSpec(keys)
	spec.certDER = buildCertificate(keys, fixedNow.Add(30*time.Hour), CertType(7), CertKeyTypeEd25519, nil)
	_, err := admitParsed(t, admitter, buildDescriptor(t, keys, spec), keys.blindedPublic)
	if !errors.Is(err, ErrMalformedDescriptor) {
		t.Fatalf("error = %v, want ErrMalformedDescriptor", err)
	}
}

func TestRevisionCacheIsKeyedPerBlindedKey(t *testing.T) {
	serviceA := mustGenerateKeys(t)
	serviceB := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	specA := validSpec(serviceA)
	specA.revisionCounter = 5
	if _, err := admitParsed(t, admitter, buildDescriptor(t, serviceA, specA), serviceA.blindedPublic); err != nil {
		t.Fatalf("service A admission failed: %v", err)
	}

	// Counter 5 is a rollback for A but the first descriptor for B, so it must
	// be accepted because caches are keyed per blinded key.
	specB := validSpec(serviceB)
	specB.revisionCounter = 5
	if _, err := admitParsed(t, admitter, buildDescriptor(t, serviceB, specB), serviceB.blindedPublic); err != nil {
		t.Fatalf("service B admission failed: %v", err)
	}
}

func TestAdmitRejectsMalformedInputs(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()
	raw := buildDescriptor(t, keys, validSpec(keys))

	t.Run("nil descriptor", func(t *testing.T) {
		if err := admitter.Admit(nil, keys.blindedPublic); err == nil {
			t.Fatal("expected error for nil descriptor")
		}
	})

	t.Run("bad blinded key length", func(t *testing.T) {
		desc, err := ParseDescriptor(raw)
		if err != nil {
			t.Fatalf("ParseDescriptor failed: %v", err)
		}
		if err := admitter.Admit(desc, []byte{1, 2, 3}); !errors.Is(err, ErrInvalidBlindedKey) {
			t.Fatalf("error = %v, want ErrInvalidBlindedKey", err)
		}
	})

	t.Run("truncated text", func(t *testing.T) {
		if _, err := ParseDescriptor(raw[:len(raw)/2]); !errors.Is(err, ErrMalformedDescriptor) {
			t.Fatalf("error = %v, want ErrMalformedDescriptor", err)
		}
	})

	t.Run("truncated certificate", func(t *testing.T) {
		if _, err := ParseCertificate(raw[:20]); !errors.Is(err, ErrMalformedDescriptor) {
			t.Fatalf("error = %v, want ErrMalformedDescriptor", err)
		}
	})
}

func TestSignaturePrefixIsBoundToMessage(t *testing.T) {
	keys := mustGenerateKeys(t)
	admitter := newTestAdmitter()

	// Forge a signature over the body without the mandated domain-separation
	// prefix; even though the signing key is the certified one, admission must
	// reject it because the fixed prefix is part of the signed message.
	spec := validSpec(keys)
	raw := buildDescriptor(t, keys, spec)
	desc, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatalf("ParseDescriptor failed: %v", err)
	}
	forged := ed25519.Sign(keys.signPrivate, desc.SignedBody)
	desc.Signature = forged
	if err := admitter.Admit(desc, keys.blindedPublic); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want ErrBadSignature", err)
	}
}

func TestCertificateRoundTrip(t *testing.T) {
	keys := mustGenerateKeys(t)
	der := buildCertificate(keys, fixedNow.Add(time.Hour), CertTypeSigningKey, CertKeyTypeEd25519,
		[]CertExtension{{Type: 4, Flags: 1, Value: []byte("ext")}})

	cert, err := ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate failed: %v", err)
	}
	if cert.Type != CertTypeSigningKey || cert.KeyType != CertKeyTypeEd25519 {
		t.Fatalf("unexpected cert type/key type: %d/%d", cert.Type, cert.KeyType)
	}
	if !cert.Expiration.Equal(fixedNow.Add(time.Hour).Truncate(time.Hour)) {
		t.Fatalf("expiration = %s", cert.Expiration)
	}
	if !bytes.Equal(cert.SigningKey, keys.signPublic) {
		t.Fatal("certified signing key mismatch")
	}
	if len(cert.Extensions) != 1 || cert.Extensions[0].Type != 4 ||
		string(cert.Extensions[0].Value) != "ext" {
		t.Fatalf("extensions round trip failed: %+v", cert.Extensions)
	}
	if !ed25519.Verify(keys.blindedPublic, cert.CertSignedPart(), cert.Signature) {
		t.Fatal("parsed certificate signature does not verify")
	}
}

func TestNewAdmitterRequiresVerifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil verifier")
		}
	}()
	NewAdmitter(nil)
}
