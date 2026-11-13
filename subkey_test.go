package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"
)

// testKey is a key pair in a toy signature scheme: sig = sha256(secret ||
// hashInput). Only the key holder can produce a valid signature.
type testKey struct {
	pub    *PublicKey
	secret []byte
}

func newTestKey(name string) *testKey {
	secret := sha256.Sum256([]byte("secret:" + name))
	return &testKey{
		pub: &PublicKey{
			Fingerprint: []byte("fpr:" + name),
			Body:        []byte("body:" + name),
		},
		secret: secret[:],
	}
}

func (k *testKey) sign(hashInput []byte) []byte {
	h := sha256.New()
	h.Write(k.secret)
	h.Write(hashInput)
	return h.Sum(nil)
}

// verifier builds a VerifyFunc from the test keys' public verification
// path.
func verifier(keys ...*testKey) VerifyFunc {
	secrets := map[string][]byte{}
	for _, k := range keys {
		secrets[string(k.pub.Fingerprint)] = k.secret
	}
	return func(key *PublicKey, hashInput, sig []byte) bool {
		secret, ok := secrets[string(key.Fingerprint)]
		if !ok {
			return false
		}
		h := sha256.New()
		h.Write(secret)
		h.Write(hashInput)
		return bytes.Equal(h.Sum(nil), sig)
	}
}

var testNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func timePtr(t time.Time) *time.Time { return &t }

// makeBinding creates a subkey binding of sub to primary with a valid
// 0x18 binding signature and, for signing-capable flags, a valid
// embedded 0x19 back-signature.
func makeBinding(primary, sub *testKey, flags byte, created time.Time) *SubkeyBinding {
	hashInput := KeyHashInput(primary.pub, sub.pub)
	bs := &BindingSignature{
		CreationTime:      created,
		KeyFlags:          flags,
		IssuerFingerprint: primary.pub.Fingerprint,
		Signature:         primary.sign(hashInput),
	}
	if requiresBackSignature(flags) {
		bs.Embedded = &EmbeddedSignature{
			CreationTime:      created,
			IssuerFingerprint: sub.pub.Fingerprint,
			Signature:         sub.sign(hashInput),
		}
	}
	return &SubkeyBinding{
		Primary: primary.pub,
		Subkey:  sub.pub,
		Binding: bs,
	}
}

func TestValidSigningSubkey(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	b := makeBinding(primary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	if err := VerifyBinding(b, verifier(primary, sub), testNow); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if err := b.AcceptsMessageSignature(testNow.Add(-time.Minute)); err != nil {
		t.Fatalf("valid message signature rejected: %v", err)
	}
}

func TestMissingBackSignature(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	b := makeBinding(primary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	b.Binding.Embedded = nil
	if err := VerifyBinding(b, verifier(primary, sub), testNow); !errors.Is(err, ErrBackSignatureMissing) {
		t.Fatalf("got %v, want ErrBackSignatureMissing", err)
	}
}

func TestAuthenticationSubkeyRequiresBackSignature(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	b := makeBinding(primary, sub, KeyFlagAuthenticate, testNow.Add(-time.Hour))
	b.Binding.Embedded = nil
	if err := VerifyBinding(b, verifier(primary, sub), testNow); !errors.Is(err, ErrBackSignatureMissing) {
		t.Fatalf("got %v, want ErrBackSignatureMissing", err)
	}
}

func TestForgedBackSignature(t *testing.T) {
	primary, sub, attacker := newTestKey("primary"), newTestKey("sub"), newTestKey("attacker")
	b := makeBinding(primary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	// Attacker forges the embedded 0x19 with their own key.
	b.Binding.Embedded = &EmbeddedSignature{
		CreationTime:      testNow.Add(-time.Hour),
		IssuerFingerprint: sub.pub.Fingerprint,
		Signature:         attacker.sign(KeyHashInput(primary.pub, sub.pub)),
	}
	if err := VerifyBinding(b, verifier(primary, sub, attacker), testNow); !errors.Is(err, ErrBackSignatureVerify) {
		t.Fatalf("got %v, want ErrBackSignatureVerify", err)
	}
}

// TestAttackerRebinding: the attacker binds the victim's public signing
// subkey to the attacker's own primary key. The attacker can self-sign
// the 0x18 but cannot produce the victim subkey's 0x19 back-signature.
func TestAttackerRebinding(t *testing.T) {
	victimPrimary, victimSub := newTestKey("victim-primary"), newTestKey("victim-sub")
	attackerPrimary := newTestKey("attacker-primary")

	hashInput := KeyHashInput(attackerPrimary.pub, victimSub.pub)
	evil := &SubkeyBinding{
		Primary: attackerPrimary.pub,
		Subkey:  victimSub.pub,
		Binding: &BindingSignature{
			CreationTime:      testNow.Add(-time.Hour),
			KeyFlags:          KeyFlagSign,
			IssuerFingerprint: attackerPrimary.pub.Fingerprint,
			Signature:         attackerPrimary.sign(hashInput),
			// No back-signature: the attacker lacks the victim's subkey
			// secret.
		},
	}
	verify := verifier(victimPrimary, victimSub, attackerPrimary)
	if err := VerifyBinding(evil, verify, testNow); !errors.Is(err, ErrBackSignatureMissing) {
		t.Fatalf("got %v, want ErrBackSignatureMissing", err)
	}

	// Even if the attacker embeds a self-made 0x19, it cannot verify
	// under the victim's subkey.
	evil.Binding.Embedded = &EmbeddedSignature{
		CreationTime:      testNow.Add(-time.Hour),
		IssuerFingerprint: victimSub.pub.Fingerprint,
		Signature:         attackerPrimary.sign(hashInput),
	}
	if err := VerifyBinding(evil, verify, testNow); !errors.Is(err, ErrBackSignatureVerify) {
		t.Fatalf("got %v, want ErrBackSignatureVerify", err)
	}
}

func TestEncryptionOnlySubkeyNeedsNoBackSignature(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	for _, flags := range []byte{KeyFlagEncryptComms, KeyFlagEncryptStore, KeyFlagEncryptComms | KeyFlagEncryptStore} {
		b := makeBinding(primary, sub, flags, testNow.Add(-time.Hour))
		if b.Binding.Embedded != nil {
			t.Fatalf("flags %#x: unexpected embedded signature", flags)
		}
		if err := VerifyBinding(b, verifier(primary, sub), testNow); err != nil {
			t.Fatalf("flags %#x: encryption-only subkey rejected: %v", flags, err)
		}
	}
}

func TestRevocation(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	revokedAt := testNow.Add(-time.Hour)
	b := makeBinding(primary, sub, KeyFlagSign, revokedAt.Add(-24*time.Hour))
	b.Revocation = &RevocationSignature{
		CreationTime:      revokedAt,
		IssuerFingerprint: primary.pub.Fingerprint,
		Signature:         primary.sign(KeyHashInput(primary.pub, sub.pub)),
	}
	verify := verifier(primary, sub)
	if err := VerifyBinding(b, verify, testNow); err != nil {
		t.Fatalf("authentic revocation rejected: %v", err)
	}
	if err := b.AcceptsMessageSignature(revokedAt.Add(-time.Minute)); err != nil {
		t.Fatalf("pre-revocation message signature rejected: %v", err)
	}
	if err := b.AcceptsMessageSignature(revokedAt.Add(time.Minute)); !errors.Is(err, ErrSubkeyRevoked) {
		t.Fatalf("got %v, want ErrSubkeyRevoked", err)
	}

	// A forged revocation must not stand.
	attacker := newTestKey("attacker")
	b.Revocation.Signature = attacker.sign(KeyHashInput(primary.pub, sub.pub))
	if err := VerifyBinding(b, verifier(primary, sub, attacker), testNow); !errors.Is(err, ErrRevocationVerify) {
		t.Fatalf("got %v, want ErrRevocationVerify", err)
	}
}

func TestExpiry(t *testing.T) {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	verify := verifier(primary, sub)

	// Subkey expiration.
	expiresAt := testNow.Add(-time.Hour)
	b := makeBinding(primary, sub, KeyFlagSign, expiresAt.Add(-48*time.Hour))
	b.SubkeyExpiration = timePtr(expiresAt)
	if err := b.AcceptsMessageSignature(expiresAt.Add(-time.Minute)); err != nil {
		t.Fatalf("pre-expiry message signature rejected: %v", err)
	}
	if err := b.AcceptsMessageSignature(expiresAt.Add(time.Minute)); !errors.Is(err, ErrSubkeyExpired) {
		t.Fatalf("got %v, want ErrSubkeyExpired", err)
	}

	// Binding signature expiration.
	b2 := makeBinding(primary, sub, KeyFlagSign, expiresAt.Add(-48*time.Hour))
	b2.Binding.ExpirationTime = timePtr(expiresAt)
	if err := b2.AcceptsMessageSignature(expiresAt.Add(-time.Minute)); err != nil {
		t.Fatalf("pre-expiry message signature rejected: %v", err)
	}
	if err := b2.AcceptsMessageSignature(expiresAt.Add(time.Minute)); !errors.Is(err, ErrBindingExpired) {
		t.Fatalf("got %v, want ErrBindingExpired", err)
	}

	// Binding signature created in the future relative to verification.
	b3 := makeBinding(primary, sub, KeyFlagSign, testNow.Add(time.Hour))
	if err := VerifyBinding(b3, verify, testNow); !errors.Is(err, ErrBindingNotYetValid) {
		t.Fatalf("got %v, want ErrBindingNotYetValid", err)
	}
}

// TestMultiplePrimaries: the same subkey is bound to two primary keys;
// only the binding whose issuer fingerprint matches the message
// signature and whose back-signature verifies is accepted.
func TestMultiplePrimaries(t *testing.T) {
	honestPrimary, sub := newTestKey("honest-primary"), newTestKey("sub")
	attackerPrimary := newTestKey("attacker-primary")
	verify := verifier(honestPrimary, sub, attackerPrimary)

	honest := makeBinding(honestPrimary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	// Attacker rebinding without a valid back-signature.
	evil := &SubkeyBinding{
		Primary: attackerPrimary.pub,
		Subkey:  sub.pub,
		Binding: &BindingSignature{
			CreationTime:      testNow.Add(-time.Hour),
			KeyFlags:          KeyFlagSign,
			IssuerFingerprint: attackerPrimary.pub.Fingerprint,
			Signature:         attackerPrimary.sign(KeyHashInput(attackerPrimary.pub, sub.pub)),
		},
	}
	bindings := []*SubkeyBinding{evil, honest}

	got, err := SelectBinding(bindings, honestPrimary.pub.Fingerprint, verify, testNow)
	if err != nil {
		t.Fatalf("honest binding not selected: %v", err)
	}
	if got != honest {
		t.Fatalf("selected wrong binding")
	}

	// The attacker's binding fails verification, so their issuer
	// fingerprint resolves to nothing.
	if _, err := SelectBinding(bindings, attackerPrimary.pub.Fingerprint, verify, testNow); !errors.Is(err, ErrNoMatchingBinding) {
		t.Fatalf("got %v, want ErrNoMatchingBinding", err)
	}

	// An issuer fingerprint that matches no primary resolves to nothing.
	if _, err := SelectBinding(bindings, []byte("fpr:stranger"), verify, testNow); !errors.Is(err, ErrNoMatchingBinding) {
		t.Fatalf("got %v, want ErrNoMatchingBinding", err)
	}
}

func TestBindingIssuerMustBePrimary(t *testing.T) {
	primary, sub, other := newTestKey("primary"), newTestKey("sub"), newTestKey("other")
	b := makeBinding(primary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	// Binding signed by someone else but claiming the primary as issuer.
	b.Binding.Signature = other.sign(KeyHashInput(primary.pub, sub.pub))
	if err := VerifyBinding(b, verifier(primary, sub, other), testNow); !errors.Is(err, ErrBindingVerify) {
		t.Fatalf("got %v, want ErrBindingVerify", err)
	}
	// Binding honestly issued by the other key does not match the
	// claimed primary.
	b.Binding.IssuerFingerprint = other.pub.Fingerprint
	if err := VerifyBinding(b, verifier(primary, sub, other), testNow); !errors.Is(err, ErrBindingIssuerMismatch) {
		t.Fatalf("got %v, want ErrBindingIssuerMismatch", err)
	}
}

func ExampleSelectBinding() {
	primary, sub := newTestKey("primary"), newTestKey("sub")
	b := makeBinding(primary, sub, KeyFlagSign, testNow.Add(-time.Hour))
	got, err := SelectBinding([]*SubkeyBinding{b}, primary.pub.Fingerprint, verifier(primary, sub), testNow)
	fmt.Println(err == nil && got.Subkey == sub.pub)
	// Output: true
}
