package netsec

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestRootVotesUseDistinctTrustedIDs(t *testing.T) {
	fixture := newTestFixture()
	manifest := fixture.manifest(5)
	fixture.signRoots(&manifest, 0)

	duplicate := manifest
	duplicate.RootSignatures = append([]RootSignature(nil), manifest.RootSignatures...)
	duplicate.RootSignatures = append(duplicate.RootSignatures, duplicate.RootSignatures[0])
	if _, err := VerifyRootSignatures(duplicate, fixture.roots, 2); !errors.Is(err, ErrRootThreshold) {
		t.Fatalf("duplicate identical root signature counted twice: %v", err)
	}

	unknownKey := testPrivateKey(0x99)
	canonical, err := manifest.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := manifest
	withUnknown.RootSignatures = append([]RootSignature(nil), manifest.RootSignatures...)
	withUnknown.RootSignatures = append(withUnknown.RootSignatures, RootSignature{
		RootID:    "unknown-root",
		Signature: ed25519.Sign(unknownKey, canonical),
	})
	if _, err := VerifyRootSignatures(withUnknown, fixture.roots, 2); !errors.Is(err, ErrRootThreshold) {
		t.Fatalf("unknown root cast a vote: %v", err)
	}

	conflicting := fixture.manifest(5)
	fixture.signRoots(&conflicting, 0, 1)
	conflicting.RootSignatures = append(conflicting.RootSignatures, RootSignature{
		RootID:    "root-1",
		Signature: ed25519.Sign(fixture.rootKeys[1], canonical),
	})
	if _, err := VerifyRootSignatures(conflicting, fixture.roots, 2); !errors.Is(err, ErrRootSignatureConflict) {
		t.Fatalf("conflicting signatures for one root ID were not rejected: %v", err)
	}
}

func TestCanonicalEncodingIsOrderIndependent(t *testing.T) {
	fixture := newTestFixture()
	first := fixture.manifest(5)
	second := fixture.manifest(5)

	first.Keys = []FirmwareKey{
		{KeyID: "fw-1", PublicKey: testPublicKey(fixture.firmware["fw-1"]), Usages: []string{"recovery", UsageFirmware}, StartGeneration: 1},
		{KeyID: "fw-2", PublicKey: testPublicKey(fixture.firmware["fw-2"]), Usages: []string{UsageFirmware}, StartGeneration: 1},
		{KeyID: "fw-3", PublicKey: testPublicKey(fixture.firmware["fw-3"]), Usages: []string{UsageFirmware}, StartGeneration: 1},
	}
	second.Keys = []FirmwareKey{
		{KeyID: "fw-3", PublicKey: testPublicKey(fixture.firmware["fw-3"]), Usages: []string{UsageFirmware}, StartGeneration: 1},
		{KeyID: "fw-1", PublicKey: testPublicKey(fixture.firmware["fw-1"]), Usages: []string{"recovery", UsageFirmware}, StartGeneration: 1},
		{KeyID: "fw-2", PublicKey: testPublicKey(fixture.firmware["fw-2"]), Usages: []string{UsageFirmware}, StartGeneration: 1},
	}
	first.RevokedKeyIDs = []string{"unused-a", "unused-z"}
	second.RevokedKeyIDs = []string{"unused-z", "unused-a"}

	firstCanonical, err := first.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := second.CanonicalEncoding()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstCanonical) != string(secondCanonical) {
		t.Fatal("canonical encoding depends on input slice order")
	}
}

func TestValidityWindowIsLeftClosedRightOpen(t *testing.T) {
	manifest := newTestFixture().manifest(5)
	if !manifest.ValidAt(manifest.ValidFrom) {
		t.Fatal("valid-from boundary must be inclusive")
	}
	if manifest.ValidAt(manifest.ValidUntil) {
		t.Fatal("valid-until boundary must be exclusive")
	}
}
