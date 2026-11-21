package manifest

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestAdmitRootQuorumAndUnknownRoots(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2", "r3"}, 2, 1)
	keys := makeKeys(t, "k1")
	m := baseManifest(entries(keys, 1, 10), 1, 1)

	// One known root plus an unknown root must not reach the quorum.
	sm := env.signManifest(m, []string{"r1"})
	sm.Signatures = append(sm.Signatures, RootSignature{
		RootID:    "unknown-root",
		Signature: ed25519.Sign(env.roots["r1"].priv, mustEncode(t, m)),
	})
	if _, err := env.host.Admit(sm); !errors.Is(err, ErrRootThresholdNotMet) {
		t.Fatalf("expected ErrRootThresholdNotMet, got %v", err)
	}

	// A bad signature from a known root is a hard failure, not silently ignored.
	bad := env.signManifest(m, []string{"r1", "r2"})
	bad.Signatures[0].Signature[0] ^= 0xFF
	if _, err := env.host.Admit(bad); !errors.Is(err, ErrInvalidRootSignature) {
		t.Fatalf("expected ErrInvalidRootSignature, got %v", err)
	}

	// Two distinct known roots admit the manifest.
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}
}

func TestAdmitConflictingDuplicateRoot(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")
	m := baseManifest(entries(keys, 1, 10), 1, 1)

	encoded := mustEncode(t, m)
	sig1 := ed25519.Sign(env.roots["r1"].priv, encoded)
	sig2 := append([]byte(nil), sig1...)
	sig2[0] ^= 0x01

	sm := SignedManifest{Manifest: m, Signatures: []RootSignature{
		{RootID: "r1", Signature: sig1},
		{RootID: "r1", Signature: sig2},
		{RootID: "r2", Signature: ed25519.Sign(env.roots["r2"].priv, encoded)},
	}}
	if _, err := env.host.Admit(sm); !errors.Is(err, ErrConflictingRootSignatures) {
		t.Fatalf("expected ErrConflictingRootSignatures, got %v", err)
	}

	// An identical duplicate is harmless and still reaches the quorum.
	sm.Signatures[1].Signature = append([]byte(nil), sig1...)
	if _, err := env.host.Admit(sm); err != nil {
		t.Fatalf("admit with duplicate identical signature: %v", err)
	}
}

func TestOldManifestReplayRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")

	gen1 := baseManifest(entries(keys, 1, 10), 1, 1)
	if _, err := env.host.Admit(env.signManifest(gen1, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen1: %v", err)
	}

	// Gen 2 revokes k2.
	gen2 := baseManifest(entries(keys, 1, 10), 2, 1, "k2")
	if _, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen2: %v", err)
	}

	// Replaying the old gen-1 manifest (which would resurrect revoked k2) is stale.
	if _, err := env.host.Admit(env.signManifest(gen1, []string{"r1", "r2"})); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("expected ErrStaleGeneration, got %v", err)
	}

	// Re-submitting the exact accepted manifest is idempotent and writes nothing.
	stateBefore := mustLoad(t, env.store)
	state, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"}))
	if err != nil {
		t.Fatalf("idempotent re-admit: %v", err)
	}
	if state.Generation != stateBefore.Generation || env.store.SaveAttempts() != 2 {
		t.Fatalf("re-admit must not write again: attempts=%d", env.store.SaveAttempts())
	}
}

func TestSameGenerationForkRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")

	m := baseManifest(entries(keys, 1, 10), 1, 1)
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	// Same generation, different body (different window): reject despite valid roots.
	fork := m
	fork.NotAfter = m.NotAfter.Add(time.Minute)
	if _, err := env.host.Admit(env.signManifest(fork, []string{"r1", "r2"})); !errors.Is(err, ErrSameGenerationFork) {
		t.Fatalf("expected ErrSameGenerationFork, got %v", err)
	}
}

func TestRevocationRollbackRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")

	gen1 := baseManifest(entries(keys, 1, 10), 1, 1, "k2")
	if _, err := env.host.Admit(env.signManifest(gen1, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen1: %v", err)
	}

	// Gen 2 drops k2 from the revocation set: rollback.
	gen2 := baseManifest(entries(keys, 1, 10), 2, 1)
	if _, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"})); !errors.Is(err, ErrRevocationRollback) {
		t.Fatalf("expected ErrRevocationRollback, got %v", err)
	}
}

func TestKeyMaterialRebindForValidKeyRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	k1 := newKeyPair(t, "k1")

	gen1 := baseManifest([]FirmwareKey{firmwareEntry(k1, 1, 10)}, 1, 1)
	if _, err := env.host.Admit(env.signManifest(gen1, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen1: %v", err)
	}

	// Rebinding the still-valid keyID k1 to new key material is rejected.
	replacement := newKeyPair(t, "replacement")
	gen2 := baseManifest([]FirmwareKey{firmwareEntry(replacement, 1, 10)}, 2, 1)
	gen2.Keys[0].KeyID = "k1"
	if _, err := env.host.Admit(env.signManifest(gen2, []string{"r1", "r2"})); !errors.Is(err, ErrKeyMaterialChanged) {
		t.Fatalf("expected ErrKeyMaterialChanged, got %v", err)
	}

	// A revoked keyID may later be re-bound to new material.
	revoked := newKeyPair(t, "revoked")
	gen3 := baseManifest([]FirmwareKey{
		firmwareEntry(k1, 1, 10),
		firmwareEntry(revoked, 1, 10),
	}, 3, 1)
	if _, err := env.host.Admit(env.signManifest(gen3, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen3: %v", err)
	}
	gen4 := baseManifest([]FirmwareKey{
		firmwareEntry(k1, 1, 10),
		firmwareEntry(replacement, 1, 10),
	}, 4, 1, "revoked")
	gen4.Keys[1].KeyID = "revoked"
	if _, err := env.host.Admit(env.signManifest(gen4, []string{"r1", "r2"})); err != nil {
		t.Fatalf("rebind revoked keyID should be allowed: %v", err)
	}
}

func TestThresholdBelowMinimumRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 2)
	keys := makeKeys(t, "k1", "k2")

	first := baseManifest(entries(keys, 1, 10), 1, 2)
	if _, err := env.host.Admit(env.signManifest(first, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit gen1: %v", err)
	}

	lowered := baseManifest(entries(keys, 1, 10), 2, 1)
	if _, err := env.host.Admit(env.signManifest(lowered, []string{"r1", "r2"})); !errors.Is(err, ErrThresholdTooLow) {
		t.Fatalf("expected ErrThresholdTooLow, got %v", err)
	}
}

func TestValidityWindowHalfOpen(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")
	m := baseManifest(entries(keys, 1, 10), 1, 1)

	env.host.Now = func() time.Time { return m.NotBefore }
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit at NotBefore: %v", err)
	}

	next := baseManifest(entries(keys, 1, 10), 2, 1)
	env.host.Now = func() time.Time { return next.NotAfter }
	if _, err := env.host.Admit(env.signManifest(next, []string{"r1", "r2"})); !errors.Is(err, ErrOutsideValidityWindow) {
		t.Fatalf("expected ErrOutsideValidityWindow at NotAfter, got %v", err)
	}
}

func TestModelMismatchRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1")
	m := baseManifest(entries(keys, 1, 10), 1, 1)
	m.DeviceModel = "other-device"
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("expected ErrModelMismatch, got %v", err)
	}
}
