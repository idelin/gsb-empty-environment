package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestRealSignaturesAcceptAndAdmit(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())
	manifest := fixture.manifest(5)
	fixture.signRoots(&manifest)

	acceptance, err := host.AcceptManifestAt(manifest, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if acceptance.Generation != 5 || len(acceptance.RootSigners) != 2 {
		t.Fatalf("unexpected acceptance: %+v", acceptance)
	}

	attestation := fixture.attestation(manifest, "1.0.0", "fw-1", "fw-2")
	decision, err := host.AdmitFirmwareAt(attestation, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.UniqueSigners != 2 || len(decision.CountedKeyIDs) != 2 {
		t.Fatalf("unexpected firmware decision: %+v", decision)
	}

	weak := fixture.attestation(manifest, "1.0.0", "fw-1")
	if _, err := host.AdmitFirmwareAt(weak, testNow); !errors.Is(err, ErrFirmwareThreshold) {
		t.Fatalf("single firmware signature met threshold 2: %v", err)
	}
}

func TestOldManifestReplayRejected(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())

	current := fixture.manifest(5)
	fixture.signRoots(&current)
	if _, err := host.AcceptManifestAt(current, testNow); err != nil {
		t.Fatal(err)
	}

	replay := fixture.manifest(4)
	replay.ValidFrom = testNow.Add(-2 * time.Hour)
	replay.ValidUntil = testNow.Add(-time.Hour)
	fixture.signRoots(&replay)
	if _, err := host.AcceptManifestAt(replay, testNow); !errors.Is(err, ErrGenerationRollback) {
		t.Fatalf("old manifest replay was accepted: %v", err)
	}

	accepted, ok, err := host.CurrentManifest()
	if err != nil || !ok || accepted.Generation != 5 {
		t.Fatalf("rollback changed accepted state: manifest=%+v accepted=%v err=%v", accepted, ok, err)
	}
}

func TestAliasFirmwareKeysCountOnce(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())
	aliasKey := testPrivateKey(0xAA)
	distinctKey := fixture.firmware["fw-1"]

	manifest := fixture.manifest(5)
	manifest.FirmwareThreshold = 2
	manifest.Keys = []FirmwareKey{
		{KeyID: "alias-a", PublicKey: testPublicKey(aliasKey), Usages: []string{UsageFirmware}, StartGeneration: 1},
		{KeyID: "alias-b", PublicKey: testPublicKey(aliasKey), Usages: []string{UsageFirmware}, StartGeneration: 1},
		{KeyID: "distinct", PublicKey: testPublicKey(distinctKey), Usages: []string{UsageFirmware}, StartGeneration: 1},
	}
	fixture.signRoots(&manifest)
	if _, err := host.AcceptManifestAt(manifest, testNow); err != nil {
		t.Fatal(err)
	}

	aliases := fixture.newAttestation(manifest, "1.0.0")
	if err := aliases.Sign("alias-a", aliasKey); err != nil {
		t.Fatal(err)
	}
	if err := aliases.Sign("alias-b", aliasKey); err != nil {
		t.Fatal(err)
	}
	decision, err := host.AdmitFirmwareAt(aliases, testNow)
	if !errors.Is(err, ErrFirmwareThreshold) {
		t.Fatalf("alias keys met threshold 2: decision=%+v err=%v", decision, err)
	}
	if decision.UniqueSigners != 1 {
		t.Fatalf("aliases produced %d signers", decision.UniqueSigners)
	}

	withDistinct := aliases
	withDistinct.Signatures = append([]FirmwareSignature(nil), aliases.Signatures...)
	if err := withDistinct.Sign("distinct", distinctKey); err != nil {
		t.Fatal(err)
	}
	decision, err = host.AdmitFirmwareAt(withDistinct, testNow)
	if err != nil {
		t.Fatalf("distinct second signer did not meet threshold: %v", err)
	}
	if decision.UniqueSigners != 2 {
		t.Fatalf("unexpected unique signer count: %d", decision.UniqueSigners)
	}
}

func TestDuplicateFirmwareSignatureCountsOnce(t *testing.T) {
	fixture := newTestFixture()
	manifest := fixture.manifest(5)
	fixture.signRoots(&manifest)

	attestation := fixture.attestation(manifest, "1.0.0", "fw-1")
	attestation.Signatures = append(attestation.Signatures, attestation.Signatures[0])
	decision, err := manifest.VerifyFirmware(attestation)
	if !errors.Is(err, ErrFirmwareThreshold) {
		t.Fatalf("duplicate firmware signature met threshold 2: decision=%+v err=%v", decision, err)
	}
	if decision.UniqueSigners != 1 {
		t.Fatalf("duplicate firmware signature produced %d signers", decision.UniqueSigners)
	}
}

func TestRevocationRollbackRejected(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())

	oldManifest := fixture.manifest(5)
	oldManifest.RevokedKeyIDs = []string{"fw-3"}
	fixture.signRoots(&oldManifest)
	if _, err := host.AcceptManifestAt(oldManifest, testNow); err != nil {
		t.Fatal(err)
	}

	rollback := fixture.manifest(6)
	fixture.signRoots(&rollback)
	if _, err := host.AcceptManifestAt(rollback, testNow); !errors.Is(err, ErrRevocationRollback) {
		t.Fatalf("revocation rollback was accepted: %v", err)
	}

	current, ok, err := host.CurrentManifest()
	if err != nil || !ok || current.Generation != 5 {
		t.Fatalf("rollback changed accepted state: manifest=%+v accepted=%v err=%v", current, ok, err)
	}

	advanced := fixture.manifest(6)
	advanced.RevokedKeyIDs = []string{"fw-3"}
	fixture.signRoots(&advanced)
	if _, err := host.AcceptManifestAt(advanced, testNow); err != nil {
		t.Fatal(err)
	}

	revokedVote := fixture.attestation(advanced, "2.0.0", "fw-1", "fw-3")
	if _, err := host.AdmitFirmwareAt(revokedVote, testNow); !errors.Is(err, ErrFirmwareThreshold) {
		t.Fatalf("revoked key still voted: %v", err)
	}
}

func TestThresholdReductionRejected(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())

	current := fixture.manifest(5)
	fixture.signRoots(&current)
	if _, err := host.AcceptManifestAt(current, testNow); err != nil {
		t.Fatal(err)
	}

	reduced := fixture.manifest(6)
	reduced.FirmwareThreshold = 1
	fixture.signRoots(&reduced)
	if _, err := host.AcceptManifestAt(reduced, testNow); !errors.Is(err, ErrFirmwareThresholdPolicy) {
		t.Fatalf("threshold below host minimum was accepted: %v", err)
	}

	accepted, ok, err := host.CurrentManifest()
	if err != nil || !ok || accepted.Generation != 5 {
		t.Fatalf("threshold reduction changed accepted state: manifest=%+v accepted=%v err=%v", accepted, ok, err)
	}
}

func TestActiveKeyMaterialChangeRejected(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())

	current := fixture.manifest(5)
	fixture.signRoots(&current)
	if _, err := host.AcceptManifestAt(current, testNow); err != nil {
		t.Fatal(err)
	}

	changed := fixture.manifest(6)
	changed.Keys[0].PublicKey = testPublicKey(testPrivateKey(0xBB))
	fixture.signRoots(&changed)
	if _, err := host.AcceptManifestAt(changed, testNow); !errors.Is(err, ErrKeyMaterialChanged) {
		t.Fatalf("active key material replacement was accepted: %v", err)
	}
}

func TestSameGenerationForkRejected(t *testing.T) {
	fixture := newTestFixture()
	host := fixture.host(NewMemoryStore())

	current := fixture.manifest(5)
	fixture.signRoots(&current)
	if _, err := host.AcceptManifestAt(current, testNow); err != nil {
		t.Fatal(err)
	}

	fork := fixture.manifest(5)
	fork.Keys[0].Usages = []string{"recovery"}
	fixture.signRoots(&fork)
	if _, err := host.AcceptManifestAt(fork, testNow); !errors.Is(err, ErrGenerationFork) {
		t.Fatalf("same-generation fork was accepted: %v", err)
	}

	repeat, err := host.AcceptManifestAt(current, testNow)
	if err != nil {
		t.Fatalf("exact same-generation repeat failed: %v", err)
	}
	if !repeat.AlreadyAccepted {
		t.Fatal("exact repeat was not reported as already accepted")
	}
}
