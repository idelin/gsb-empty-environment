package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
)

func newEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func newBoundRecord(t *testing.T, publicKey crypto.PublicKey, signer crypto.Signer, userID string) *KeyRecord {
	t.Helper()
	record, err := NewBoundKey(publicKey, signer, userID)
	if err != nil {
		t.Fatalf("NewBoundKey: %v", err)
	}
	return record
}

func mustFingerprint(t *testing.T, publicKey crypto.PublicKey) string {
	t.Helper()
	fingerprint, err := Fingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func assertPublicKey(t *testing.T, record *KeyRecord, want crypto.PublicKey) {
	t.Helper()
	if record == nil {
		t.Fatal("expected a record, got nil")
	}
	if !PublicKeysEqual(record.PublicKey, want) {
		t.Fatal("directory returned a different public key")
	}
}

func TestIdentifierCollisionCannotRedirectLookup(t *testing.T) {
	victimPublic, victimPrivate := newEd25519(t)
	attackerPublic, attackerPrivate := newEd25519(t)
	const userID = "alice@example.com"
	const collidingHint = "DEADBEEF"

	victim := newBoundRecord(t, victimPublic, victimPrivate, userID)
	victim.ShortID = collidingHint
	attacker := newBoundRecord(t, attackerPublic, attackerPrivate, userID)
	attacker.ShortID = collidingHint

	directory := NewDirectory()
	if err := directory.Import(victim); err != nil {
		t.Fatalf("import victim: %v", err)
	}
	if err := directory.Import(attacker); err != nil {
		t.Fatalf("import attacker: %v", err)
	}

	got, err := directory.Lookup(Query{
		Fingerprint: victim.Fingerprint,
		UserID:      userID,
		ShortID:     collidingHint,
	})
	if err != nil {
		t.Fatalf("lookup victim: %v", err)
	}
	assertPublicKey(t, got, victimPublic)

	got, err = directory.Lookup(attacker.Fingerprint, userID, collidingHint)
	if err != nil {
		t.Fatalf("lookup attacker: %v", err)
	}
	assertPublicKey(t, got, attackerPublic)

	if err := directory.LookupByShortID(collidingHint, userID); !errors.Is(err, ErrShortIDCollision) {
		t.Fatalf("short-only collision: got %v, want %v", err, ErrShortIDCollision)
	}
	if _, err := directory.Lookup(Query{UserID: userID, ShortID: collidingHint}); !errors.Is(err, ErrShortIDCollision) {
		t.Fatalf("query with only a colliding short ID: got %v, want %v", err, ErrShortIDCollision)
	}
	if _, err := directory.Lookup(Query{Fingerprint: victim.Fingerprint, UserID: userID, ShortID: "ABCD1234"}); !errors.Is(err, ErrShortIDMismatch) {
		t.Fatalf("wrong hint: got %v, want %v", err, ErrShortIDMismatch)
	}
}

func TestFingerprintSubstitutionIsRejected(t *testing.T) {
	victimPublic, _ := newEd25519(t)
	attackerPublic, attackerPrivate := newEd25519(t)
	const userID = "alice@example.com"

	victimFingerprint := mustFingerprint(t, victimPublic)
	victimShort, err := DerivedShortID(victimFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	// The attacker signs a perfectly valid statement, but it is a statement
	// about Alice's fingerprint using Mallory's key.
	forged, err := NewKeyRecord(attackerPublic, userID)
	if err != nil {
		t.Fatal(err)
	}
	forged.Fingerprint = victimFingerprint
	forged.ShortID = victimShort
	if err := forged.SignBinding(attackerPrivate, userID); err != nil {
		t.Fatal(err)
	}

	directory := NewDirectory()
	if err := directory.Import(forged); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("import substituted fingerprint: got %v, want %v", err, ErrFingerprintMismatch)
	}
	if _, err := directory.Lookup(victimFingerprint, userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("victim lookup after substitution attempt: got %v, want %v", err, ErrNotFound)
	}
}

func TestFingerprintCollisionDoesNotReplaceExistingKey(t *testing.T) {
	victimPublic, victimPrivate := newEd25519(t)
	attackerPublic, attackerPrivate := newEd25519(t)
	const userID = "alice@example.com"

	victim := newBoundRecord(t, victimPublic, victimPrivate, userID)
	forged, err := NewKeyRecord(attackerPublic, userID)
	if err != nil {
		t.Fatal(err)
	}
	forged.Fingerprint = victim.Fingerprint
	forged.ShortID = victim.ShortID
	if err := forged.SignBinding(attackerPrivate, userID); err != nil {
		t.Fatal(err)
	}

	directory := NewDirectory()
	if err := directory.Import(victim); err != nil {
		t.Fatal(err)
	}
	err = directory.Import(forged)
	if !errors.Is(err, ErrFingerprintCollision) || !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("collision: got %v, want ErrFingerprintCollision and ErrFingerprintMismatch", err)
	}

	got, err := directory.Lookup(victim.Fingerprint, userID)
	if err != nil {
		t.Fatalf("victim lookup after collision: %v", err)
	}
	assertPublicKey(t, got, victimPublic)
}

func TestUnverifiedBindingsAreRejected(t *testing.T) {
	public, private := newEd25519(t)
	const userID = "alice@example.com"

	unsigned, err := NewKeyRecord(public, userID)
	if err != nil {
		t.Fatal(err)
	}
	directory := NewDirectory()
	if err := directory.Import(unsigned); !errors.Is(err, ErrUnverifiedBinding) {
		t.Fatalf("unsigned binding: got %v, want %v", err, ErrUnverifiedBinding)
	}

	badSignature, err := NewKeyRecord(public, userID)
	if err != nil {
		t.Fatal(err)
	}
	badSignature.BindingSignature = make([]byte, ed25519.SignatureSize)
	if err := directory.Import(badSignature); !errors.Is(err, ErrUnverifiedBinding) {
		t.Fatalf("zero signature: got %v, want %v", err, ErrUnverifiedBinding)
	}

	// A valid signature over a different user identifier must not authorize
	// this one.
	valid := newBoundRecord(t, public, private, "bob@example.com")
	valid.UserID = userID
	if err := directory.Import(valid); !errors.Is(err, ErrUnverifiedBinding) {
		t.Fatalf("signature transplanted to another user: got %v, want %v", err, ErrUnverifiedBinding)
	}

	if _, err := directory.Lookup(mustFingerprint(t, public), userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unverified key became visible: got %v, want %v", err, ErrNotFound)
	}
}

func TestLegitimateQueryRequiresFingerprintAndUserID(t *testing.T) {
	public, private := newEd25519(t)
	const userID = "alice@example.com"
	record := newBoundRecord(t, public, private, userID)

	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatalf("import: %v", err)
	}

	displayFingerprint := strings.ToLower(record.Fingerprint)
	displayFingerprint = displayFingerprint[0:8] + ":" + displayFingerprint[8:16] + ":" + displayFingerprint[16:]
	got, err := directory.Lookup(Query{
		Fingerprint: displayFingerprint,
		UserID:      "  " + userID + " ",
		ShortID:     record.ShortID,
	})
	if err != nil {
		t.Fatalf("legitimate lookup: %v", err)
	}
	assertPublicKey(t, got, public)
	if got.Fingerprint != record.Fingerprint {
		t.Fatalf("fingerprint = %s, want %s", got.Fingerprint, record.Fingerprint)
	}
	if got.UserID != userID {
		t.Fatalf("user ID = %q, want %q", got.UserID, userID)
	}

	if _, err := directory.Lookup(record.Fingerprint, "bob@example.com"); !errors.Is(err, ErrUserMismatch) {
		t.Fatalf("wrong user: got %v, want %v", err, ErrUserMismatch)
	}
	otherPublic, _ := newEd25519(t)
	if _, err := directory.Lookup(mustFingerprint(t, otherPublic), userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong fingerprint: got %v, want %v", err, ErrNotFound)
	}
	if _, err := directory.Lookup(Query{UserID: userID, ShortID: record.ShortID}); !errors.Is(err, ErrFingerprintRequired) {
		t.Fatalf("short-only lookup: got %v, want %v", err, ErrFingerprintRequired)
	}
	if _, err := directory.Lookup(Query{UserID: userID}); !errors.Is(err, ErrFingerprintRequired) {
		t.Fatalf("user-only lookup: got %v, want %v", err, ErrFingerprintRequired)
	}
}

func TestMultipleKeysForOneUserAreAmbiguous(t *testing.T) {
	const userID = "alice@example.com"
	firstPublic, firstPrivate := newEd25519(t)
	secondPublic, secondPrivate := newEd25519(t)

	directory := NewDirectory()
	if err := directory.Import(newBoundRecord(t, firstPublic, firstPrivate, userID)); err != nil {
		t.Fatal(err)
	}
	if err := directory.Import(newBoundRecord(t, secondPublic, secondPrivate, userID)); err != nil {
		t.Fatal(err)
	}

	if _, err := directory.Lookup(Query{UserID: userID}); !errors.Is(err, ErrMultipleResults) {
		t.Fatalf("ambiguous user lookup: got %v, want %v", err, ErrMultipleResults)
	}
	if _, err := directory.LookupByUserID(userID); !errors.Is(err, ErrMultipleResults) {
		t.Fatalf("LookupByUserID: got %v, want %v", err, ErrMultipleResults)
	}
	found, err := directory.FindByUserID(userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("FindByUserID returned %d keys, want 2", len(found))
	}
}

func TestRevocationIsEnforcedAndCannotBeUndoneByReimport(t *testing.T) {
	public, private := newEd25519(t)
	const userID = "alice@example.com"
	record := newBoundRecord(t, public, private, userID)

	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatal(err)
	}

	signature, algorithm, err := SignMessage(private, RevocationStatement(record.Fingerprint, userID))
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.Revoke(Revocation{
		Fingerprint: record.Fingerprint,
		UserID:      userID,
		Algorithm:   algorithm,
		Signature:   signature,
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := directory.Lookup(record.Fingerprint, userID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("lookup revoked key: got %v, want %v", err, ErrRevoked)
	}
	if !directory.IsBindingRevoked(record.Fingerprint, userID) {
		t.Fatal("IsBindingRevoked = false")
	}

	// Replaying the old, valid, non-revoked record must not resurrect it.
	if err := directory.Import(record); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if _, err := directory.Lookup(record.Fingerprint, userID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked key was resurrected: got %v, want %v", err, ErrRevoked)
	}
}

func TestInvalidRevocationCertificateIsRejected(t *testing.T) {
	public, private := newEd25519(t)
	otherPublic, otherPrivate := newEd25519(t)
	const userID = "alice@example.com"
	record := newBoundRecord(t, public, private, userID)

	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatal(err)
	}

	// Signed by a different key.
	badSignature, algorithm, err := SignMessage(otherPrivate, RevocationStatement(record.Fingerprint, userID))
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.Revoke(record.Fingerprint, userID, badSignature, algorithm); !errors.Is(err, ErrInvalidRevocation) {
		t.Fatalf("revocation by other key: got %v, want %v", err, ErrInvalidRevocation)
	}
	if _, err := directory.Lookup(record.Fingerprint, userID); err != nil {
		t.Fatalf("invalid revocation affected the key: %v", err)
	}

	// A tampered certificate is also rejected.
	goodSignature, goodAlgorithm, err := SignMessage(private, RevocationStatement(record.Fingerprint, userID))
	if err != nil {
		t.Fatal(err)
	}
	goodSignature[0] ^= 0xFF
	if err := directory.Revoke(record.Fingerprint, userID, goodSignature, goodAlgorithm); !errors.Is(err, ErrInvalidRevocation) {
		t.Fatalf("tampered revocation: got %v, want %v", err, ErrInvalidRevocation)
	}
	if _, err := directory.Lookup(record.Fingerprint, userID); err != nil {
		t.Fatalf("tampered revocation affected the key: %v", err)
	}
	_ = otherPublic
}

func TestImportedRevocationCertificateIsEnforced(t *testing.T) {
	public, private := newEd25519(t)
	const userID = "alice@example.com"
	record := newBoundRecord(t, public, private, userID)
	if err := record.SignRevocation(private, userID); err != nil {
		t.Fatal(err)
	}

	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatalf("import revoked record: %v", err)
	}
	if _, err := directory.Lookup(record.Fingerprint, userID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("imported revoked key: got %v, want %v", err, ErrRevoked)
	}
}

func TestKeyWideRevocationCoversEveryBinding(t *testing.T) {
	public, private := newEd25519(t)
	const firstUser = "alice@example.com"
	const secondUser = "alice@work.example"
	fingerprint := mustFingerprint(t, public)

	newBinding := func(userID string) Binding {
		signature, algorithm, err := SignMessage(private, BindingStatement(fingerprint, userID))
		if err != nil {
			t.Fatal(err)
		}
		return Binding{UserID: userID, Fingerprint: fingerprint, Algorithm: algorithm, Signature: signature}
	}
	record := KeyRecord{
		Fingerprint: fingerprint,
		PublicKey:   public,
		Bindings:    []Binding{newBinding(firstUser), newBinding(secondUser)},
	}
	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Lookup(fingerprint, firstUser); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Lookup(fingerprint, secondUser); err != nil {
		t.Fatal(err)
	}

	signature, algorithm, err := SignMessage(private, RevocationStatement(fingerprint, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.Revoke(fingerprint, "", signature, algorithm); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{firstUser, secondUser} {
		if _, err := directory.Lookup(fingerprint, userID); !errors.Is(err, ErrRevoked) {
			t.Fatalf("%s after key-wide revocation: got %v, want %v", userID, err, ErrRevoked)
		}
	}

	// A key-wide certificate carried in the imported record has the same
	// effect.
	revokedRecord := record
	if err := revokedRecord.SignRevocation(private, ""); err != nil {
		t.Fatal(err)
	}
	imported := NewDirectory()
	if err := imported.Import(revokedRecord); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{firstUser, secondUser} {
		if _, err := imported.Lookup(fingerprint, userID); !errors.Is(err, ErrRevoked) {
			t.Fatalf("%s after imported key-wide revocation: got %v, want %v", userID, err, ErrRevoked)
		}
	}
}

func TestRSAAndECDSABindings(t *testing.T) {
	rsaPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		public crypto.PublicKey
		signer crypto.Signer
	}{
		{name: "rsa", public: &rsaPrivate.PublicKey, signer: rsaPrivate},
		{name: "ecdsa", public: &ecdsaPrivate.PublicKey, signer: ecdsaPrivate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := newBoundRecord(t, test.public, test.signer, "alice@example.com")
			directory := NewDirectory()
			if err := directory.Import(record); err != nil {
				t.Fatal(err)
			}
			got, err := directory.Lookup(record.Fingerprint, "alice@example.com")
			if err != nil {
				t.Fatal(err)
			}
			assertPublicKey(t, got, test.public)
		})
	}
}

func TestFortyDigitFullFingerprint(t *testing.T) {
	public, private := newEd25519(t)
	set, err := FingerprintSet(public)
	if err != nil {
		t.Fatal(err)
	}
	var sha1Fingerprint string
	for candidate := range set {
		if len(candidate) == 40 {
			sha1Fingerprint = candidate
			break
		}
	}
	if sha1Fingerprint == "" {
		t.Fatal("SHA-1 fingerprint form not available")
	}

	record, err := NewKeyRecord(public, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	record.Fingerprint = sha1Fingerprint
	shortID, err := DerivedShortID(sha1Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	record.ShortID = shortID
	if err := record.SignBinding(private, "alice@example.com"); err != nil {
		t.Fatal(err)
	}

	directory := NewDirectory()
	if err := directory.Import(record); err != nil {
		t.Fatal(err)
	}
	got, err := directory.Lookup(sha1Fingerprint, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	assertPublicKey(t, got, public)
	if got.Fingerprint != sha1Fingerprint {
		t.Fatalf("fingerprint = %s, want %s", got.Fingerprint, sha1Fingerprint)
	}
}

func TestZeroValueDirectory(t *testing.T) {
	public, private := newEd25519(t)
	record := newBoundRecord(t, public, private, "alice@example.com")

	var directory Directory
	if err := directory.Add(record); err != nil {
		t.Fatal(err)
	}
	got, err := directory.Lookup(record.Fingerprint, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	assertPublicKey(t, got, public)
}
