package npmverify

import (
	"context"
	"errors"
	"testing"
)

func TestECDSAKeyringAcceptsTrustedSignature(t *testing.T) {
	f := newFixture(t)
	message := SignatureMessage(pkgName, pkgVersion, f.doc.Versions[pkgVersion].Dist.Integrity)
	sig := signMessage(t, f.trusted, trustedKeyID, message)

	if err := f.ring.Verify(context.Background(), message, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestECDSAKeyringRejectsUnknownKey(t *testing.T) {
	f := newFixture(t)
	message := "name@version:integrity"
	sig := signMessage(t, f.attacker, attackerKeyID, message)

	err := f.ring.Verify(context.Background(), message, sig)
	if err == nil {
		t.Fatal("expected rejection for unknown keyid")
	}
}

func TestECDSAKeyringRejectsTamperedMessage(t *testing.T) {
	f := newFixture(t)
	sig := signMessage(t, f.trusted, trustedKeyID, "original message")

	if err := f.ring.Verify(context.Background(), "tampered message", sig); err == nil {
		t.Fatal("expected signature mismatch for altered message")
	}
}

func TestECDSAKeyringRejectsBadBase64(t *testing.T) {
	f := newFixture(t)
	err := f.ring.Verify(context.Background(), "msg", Signature{KeyID: trustedKeyID, Sig: "%%%"})
	if err == nil {
		t.Fatal("expected base64 error")
	}
}

func TestNewECDSAKeyringRejectsBadPEM(t *testing.T) {
	if _, err := NewECDSAKeyring(map[string][]byte{"k": []byte("not pem")}); err == nil {
		t.Fatal("expected error for non-PEM material")
	}

	if _, err := NewECDSAKeyring(map[string][]byte{}); err != nil {
		t.Fatalf("empty keyring: %v", err)
	}
}

func TestVerifyAcceptsAnyValidCallbackSignature(t *testing.T) {
	// Multiple signatures, first untrusted, second trusted: verification
	// succeeds and reports the accepted key id.
	f := newFixture(t)
	pkg := f.doc.Versions[pkgVersion]
	integrity := pkg.Dist.Integrity
	message := SignatureMessage(pkgName, pkgVersion, integrity)

	pkg.Dist.Signatures = []Signature{
		signMessage(t, f.attacker, attackerKeyID, message),
		signMessage(t, f.trusted, trustedKeyID, message),
	}

	result, err := Verify(context.Background(), f.doc, "latest", f.tarball, f.ring.Verify, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.AcceptedKeyID != trustedKeyID {
		t.Fatalf("accepted key = %q, want %q", result.AcceptedKeyID, trustedKeyID)
	}
}

func TestVerifyCallbackErrorDoesNotPanic(t *testing.T) {
	f := newFixture(t)
	alwaysFail := func(_ context.Context, _ string, _ Signature) error {
		return errors.New("key store unavailable")
	}

	_, err := Verify(context.Background(), f.doc, "latest", f.tarball, alwaysFail, nil)
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("err = %v, want ErrSignatureFailed", err)
	}
}
