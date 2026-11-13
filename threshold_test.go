package netsec

import (
	"errors"
	"math/big"
	"testing"
)

func scalar(t *testing.T, value int64) Scalar {
	t.Helper()
	out, err := ScalarFromBig(big.NewInt(value))
	if err != nil {
		t.Fatalf("ScalarFromBig(%d): %v", value, err)
	}
	return out
}

func pointForScalar(t *testing.T, value int64) Point {
	t.Helper()
	point, err := ScalarBaseMult(scalar(t, value))
	if err != nil {
		t.Fatalf("ScalarBaseMult(%d): %v", value, err)
	}
	return point
}

func dealerFixture(t *testing.T, threshold int, ids ...uint32) (Manifest, []SecretShare) {
	t.Helper()
	manifest, shares, err := GenerateDealer(scalar(t, 123456789), threshold, ids, nil)
	if err != nil {
		t.Fatalf("GenerateDealer: %v", err)
	}
	return manifest, shares
}

func ciphertextFixture(t *testing.T, manifest Manifest, messageScalar int64) (Ciphertext, Point) {
	t.Helper()
	message := pointForScalar(t, messageScalar)
	ciphertext, err := Encrypt(manifest, message, nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return ciphertext, message
}

func decryptionShares(t *testing.T, manifest Manifest, ciphertext Ciphertext, secrets []SecretShare) []DecryptionShare {
	t.Helper()
	out := make([]DecryptionShare, 0, len(secrets))
	for _, secret := range secrets {
		share, err := secret.Decrypt(manifest, ciphertext, nil)
		if err != nil {
			t.Fatalf("member %d Decrypt: %v", secret.ID, err)
		}
		out = append(out, share)
	}
	return out
}

func TestHonestRecoveryChecksEveryThresholdSubset(t *testing.T) {
	manifest, secrets := dealerFixture(t, 3, 1, 2, 3, 4, 5, 9)
	ciphertext, message := ciphertextFixture(t, manifest, 424242)
	shares := decryptionShares(t, manifest, ciphertext, secrets)

	for _, share := range shares {
		if err := VerifyShare(manifest, ciphertext, share); err != nil {
			t.Fatalf("VerifyShare(%d): %v", share.ID, err)
		}
	}

	result, err := Decrypt(manifest, ciphertext, shares, Budget{})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if result.Plaintext != message {
		t.Fatal("recovered plaintext does not match")
	}
	if result.ValidShares != len(shares) {
		t.Fatalf("ValidShares = %d, want %d", result.ValidShares, len(shares))
	}
	wantSubsets := binomial(uint64(len(shares)), uint64(manifest.Threshold))
	if result.SubsetsChecked != wantSubsets {
		t.Fatalf("SubsetsChecked = %d, want %d", result.SubsetsChecked, wantSubsets)
	}
	if result.RejectedShares != 0 {
		t.Fatalf("RejectedShares = %d, want 0", result.RejectedShares)
	}
}

func TestManifestAndEncodingValidation(t *testing.T) {
	manifest, _ := dealerFixture(t, 3, 1, 2, 3, 4)

	badAggregate := manifest
	badAggregate.Y = BasePoint()
	if err := ValidateManifest(badAggregate); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("tampered aggregate error = %v, want ErrInvalidManifest", err)
	}

	badMember := manifest
	badMember.Members = append([]PublicShare(nil), manifest.Members...)
	badMember.Members[2].Y = BasePoint()
	if err := ValidateManifest(badMember); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("tampered member error = %v, want ErrInvalidManifest", err)
	}

	duplicateID := manifest
	duplicateID.Members = append([]PublicShare(nil), manifest.Members...)
	duplicateID.Members[1].ID = duplicateID.Members[0].ID
	if err := ValidateManifest(duplicateID); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("duplicate identifier error = %v, want ErrInvalidManifest", err)
	}

	zeroID := manifest
	zeroID.Members = append([]PublicShare(nil), manifest.Members...)
	zeroID.Members[0].ID = 0
	if err := ValidateManifest(zeroID); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("zero identifier error = %v, want ErrInvalidManifest", err)
	}

	if _, _, err := GenerateDealer(scalar(t, 1), 3, []uint32{1, 2}, nil); err == nil {
		t.Fatal("GenerateDealer accepted fewer members than threshold")
	}
	if _, _, err := GenerateDealer(scalar(t, 1), 3, []uint32{1, 2, 2}, nil); err == nil {
		t.Fatal("GenerateDealer accepted duplicate identifiers")
	}
	if _, _, err := GenerateDealer(Scalar{}, 1, []uint32{1}, nil); err == nil {
		t.Fatal("GenerateDealer accepted the zero secret")
	}

	if _, err := ParsePoint(nil); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("ParsePoint(nil) error = %v", err)
	}
	if _, err := ParsePoint(BasePoint().Bytes()); err != nil {
		t.Fatalf("ParsePoint(generator): %v", err)
	}
	zeroPoint := make([]byte, PointSize)
	zeroPoint[0] = 4
	if _, err := ParsePoint(zeroPoint); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("ParsePoint(infinity) error = %v", err)
	}

	orderBytes := curveOrder.Bytes()
	if _, err := ParseScalar(orderBytes); !errors.Is(err, ErrInvalidScalar) {
		t.Fatalf("ParseScalar(order) error = %v", err)
	}
	if _, err := ScalarBaseMult(Scalar{}); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("ScalarBaseMult(0) error = %v, want ErrInvalidPoint", err)
	}
}

func TestProofRejectsWrongDAndCrossCiphertextReplay(t *testing.T) {
	manifest, secrets := dealerFixture(t, 3, 1, 2, 3, 4)
	ciphertext, _ := ciphertextFixture(t, manifest, 77)
	shares := decryptionShares(t, manifest, ciphertext, secrets[:3])
	original := shares[0]

	wrongD := original
	badD, err := AddPoints(original.D, original.D)
	if err != nil {
		t.Fatal(err)
	}
	wrongD.D = badD
	if err := VerifyShare(manifest, ciphertext, wrongD); err == nil {
		t.Fatal("proof accepted a substituted D_i")
	}

	replayed := original
	otherCiphertext, _ := ciphertextFixture(t, manifest, 77)
	if err := VerifyShare(manifest, otherCiphertext, replayed); err == nil {
		t.Fatal("proof was accepted under a different ciphertext")
	}

	crossMember := original
	crossMember.ID = shares[1].ID
	if err := VerifyShare(manifest, ciphertext, crossMember); err == nil {
		t.Fatal("proof was accepted under another member identifier")
	}

	nonCanonicalZ := original
	curveOrder.FillBytes(nonCanonicalZ.Proof.Z[:])
	if err := VerifyShare(manifest, ciphertext, nonCanonicalZ); !errors.Is(err, ErrInvalidScalar) {
		t.Fatalf("non-canonical response error = %v, want ErrInvalidScalar", err)
	}

	infinityA := original
	infinityA.Proof.A = Point{}
	if err := VerifyShare(manifest, ciphertext, infinityA); err == nil {
		t.Fatal("proof accepted the identity commitment")
	}
}

func TestInvalidSharesAreIgnoredButDoNotCount(t *testing.T) {
	manifest, secrets := dealerFixture(t, 3, 1, 2, 3, 4, 5)
	ciphertext, message := ciphertextFixture(t, manifest, 88)
	valid := decryptionShares(t, manifest, ciphertext, secrets)

	tampered := append([]DecryptionShare(nil), valid...)
	tampered[0].D, _ = AddPoints(tampered[0].D, BasePoint())

	result, err := Decrypt(manifest, ciphertext, tampered, Budget{})
	if err != nil {
		t.Fatalf("Decrypt with one invalid share: %v", err)
	}
	if result.Plaintext != message {
		t.Fatal("plaintext mismatch after dropping invalid share")
	}
	if result.ValidShares != len(valid)-1 || result.RejectedShares != 1 {
		t.Fatalf("valid/rejected = %d/%d, want %d/1", result.ValidShares, result.RejectedShares, len(valid)-1)
	}

	if _, err := Decrypt(manifest, ciphertext, tampered[:3], Budget{}); !errors.Is(err, ErrInsufficientShares) {
		t.Fatalf("three shares with one invalid error = %v, want ErrInsufficientShares", err)
	}
}

func TestDuplicateIdentifierIsDeduplicatedAndDoubleDeliveryIsStable(t *testing.T) {
	manifest, secrets := dealerFixture(t, 3, 1, 2, 3, 4, 5, 6)
	ciphertext, message := ciphertextFixture(t, manifest, 99)
	shares := decryptionShares(t, manifest, ciphertext, secrets)

	withDuplicate := append([]DecryptionShare(nil), shares...)
	withDuplicate = append(withDuplicate, shares[0])
	result, err := Decrypt(manifest, ciphertext, withDuplicate, Budget{})
	if err != nil {
		t.Fatalf("Decrypt with duplicate identifier: %v", err)
	}
	if result.Plaintext != message || result.ValidShares != len(shares) {
		t.Fatalf("duplicate identifier changed recovery: plaintext ok=%v valid=%d", result.Plaintext == message, result.ValidShares)
	}

	firstCoordinator := []DecryptionShare{shares[0], shares[1], shares[2]}
	secondCoordinator := []DecryptionShare{shares[2], shares[3], shares[4]}
	first, err := Decrypt(manifest, ciphertext, firstCoordinator, Budget{})
	if err != nil {
		t.Fatalf("first coordinator: %v", err)
	}
	second, err := Decrypt(manifest, ciphertext, secondCoordinator, Budget{})
	if err != nil {
		t.Fatalf("second coordinator: %v", err)
	}
	if first.Plaintext != message || second.Plaintext != message {
		t.Fatal("a share delivered to two coordinators produced inconsistent plaintext")
	}
}

func TestSubsetBudgetWithholdsPlaintext(t *testing.T) {
	manifest, secrets := dealerFixture(t, 3, 1, 2, 3, 4, 5, 6)
	ciphertext, _ := ciphertextFixture(t, manifest, 123)
	shares := decryptionShares(t, manifest, ciphertext, secrets)
	total := binomial(uint64(len(shares)), 3)

	result, err := Decrypt(manifest, ciphertext, shares, Budget{MaxSubsets: total - 1})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("short budget error = %v, want ErrBudgetExhausted", err)
	}
	if result.Plaintext != (Point{}) || result.SubsetsChecked != 0 {
		t.Fatal("short budget leaked partial plaintext or subset count")
	}

	result, err = Decrypt(manifest, ciphertext, shares, Budget{MaxSubsets: total})
	if err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	if result.SubsetsChecked != total {
		t.Fatalf("SubsetsChecked = %d, want %d", result.SubsetsChecked, total)
	}
}

func TestConflictReturnsParticipantEvidence(t *testing.T) {
	xOne, xTwo := scalar(t, 2), scalar(t, 3)
	yOne, err := ScalarBaseMult(xOne)
	if err != nil {
		t.Fatal(err)
	}
	yTwo, err := ScalarBaseMult(xTwo)
	if err != nil {
		t.Fatal(err)
	}
	members := []PublicShare{{ID: 1, Y: yOne}, {ID: 2, Y: yTwo}}

	aggregate, err := AggregatePublicKey(Manifest{Threshold: 1, Members: members, Y: yOne})
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Threshold: 1, Members: members, Y: aggregate}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("inconsistent fixture manifest should still satisfy aggregate check: %v", err)
	}

	ciphertext, _ := ciphertextFixture(t, manifest, 321)
	secrets := []SecretShare{{ID: 1, X: xOne}, {ID: 2, X: xTwo}}
	shares := decryptionShares(t, manifest, ciphertext, secrets)

	result, err := Decrypt(manifest, ciphertext, shares, Budget{})
	if err == nil {
		t.Fatal("conflicting threshold subsets were accepted")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if len(conflict.Evidence.First.IDs) != 1 || len(conflict.Evidence.Second.IDs) != 1 {
		t.Fatalf("unexpected evidence IDs: %+v", conflict.Evidence)
	}
	if conflict.Evidence.First.IDs[0] == conflict.Evidence.Second.IDs[0] {
		t.Fatal("conflict evidence uses the same participant subset twice")
	}
	if conflict.Evidence.First.M == conflict.Evidence.Second.M {
		t.Fatal("conflict evidence does not contain distinct plaintexts")
	}
	if result.Plaintext != (Point{}) {
		t.Fatal("conflict returned a plaintext")
	}
}

func TestCiphertextRejectsIdentity(t *testing.T) {
	manifest, secrets := dealerFixture(t, 2, 1, 2)
	ciphertext, _ := ciphertextFixture(t, manifest, 10)
	shares := decryptionShares(t, manifest, ciphertext, secrets)

	bad := ciphertext
	bad.C1 = Point{}
	if _, err := Decrypt(manifest, bad, shares, Budget{}); err == nil {
		t.Fatal("C1 = identity was accepted")
	}

	bad = ciphertext
	bad.C2 = Point{}
	if _, err := Decrypt(manifest, bad, shares, Budget{}); err == nil {
		t.Fatal("C2 = identity was accepted")
	}
}

func TestMaximumBounds(t *testing.T) {
	ids := make([]uint32, MaxMembers)
	for i := range ids {
		ids[i] = uint32(i + 1)
	}
	manifest, secrets := dealerFixture(t, MaxThreshold, ids...)
	ciphertext, message := ciphertextFixture(t, manifest, 654321)
	shares := decryptionShares(t, manifest, ciphertext, secrets[:MaxThreshold])
	result, err := Decrypt(manifest, ciphertext, shares, Budget{MaxSubsets: 1})
	if err != nil {
		t.Fatalf("maximum-size manifest: %v", err)
	}
	if result.Plaintext != message || result.SubsetsChecked != 1 {
		t.Fatalf("maximum-size recovery failed: plaintext ok=%v subsets=%d", result.Plaintext == message, result.SubsetsChecked)
	}

	if _, _, err := GenerateDealer(scalar(t, 1), MaxThreshold, append(ids, MaxMembers+1), nil); err == nil {
		t.Fatal("GenerateDealer accepted more than 16 members")
	}
	if _, _, err := GenerateDealer(scalar(t, 1), MaxThreshold+1, ids, nil); err == nil {
		t.Fatal("GenerateDealer accepted a threshold above 8")
	}
}

func TestManifestDigestIncludesAggregateKey(t *testing.T) {
	manifest, _ := dealerFixture(t, 2, 1, 2, 3)
	first, err := ManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	other := manifest
	other.Y = BasePoint()
	second, err := ManifestDigest(other)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("manifest digest did not bind the aggregate public key")
	}
}
