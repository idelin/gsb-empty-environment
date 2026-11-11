package update

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMirrorRollbackAfterRotation models a mirror that serves a valid newer
// generation and then tries to roll every role back to an older, still
// correctly signed generation.
func TestMirrorRollbackAfterRotation(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	rootV2Envelope, rootV2 := env.rootV2()

	gen1 := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("gen1"))
	if err := client.Refresh(ctx, gen1); err != nil {
		t.Fatalf("gen1 refresh: %v", err)
	}
	gen2 := env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("gen2"))
	if err := client.Refresh(ctx, gen2); err != nil {
		t.Fatalf("gen2 refresh with root rotation: %v", err)
	}

	// A malicious mirror replays the old v1-keyed timestamp/snapshot pair.
	rollback := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("gen1"))
	err := client.Refresh(ctx, rollback)
	if !errors.Is(err, ErrRollback) && !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("expected rollback or threshold rejection, got %v", err)
	}

	// Even a well-formed but lower timestamp version under the new root is
	// rejected because the trusted generation is already at version 20.
	older := env.refreshBundle(rootV2, nil, 15, 15, 15, []byte("older"))
	assertErrorIs(t, client.Refresh(ctx, older), ErrRollback)

	// Trusted state is unchanged: only the gen2 payload still verifies.
	if _, err := client.VerifyTarget(ctx, testTargetPath, []byte("gen2")); err != nil {
		t.Fatalf("gen2 payload should still verify after rollback attempts: %v", err)
	}
	if _, err := client.VerifyTarget(ctx, testTargetPath, []byte("gen1")); err == nil {
		t.Fatal("old payload must not verify against the newer targets metadata")
	}
}

// TestOldTimestampCannotAuthorizeNewTargetFile is the direct scenario from
// the task: stale timestamp metadata combined with a different target file.
func TestOldTimestampCannotAuthorizeNewTargetFile(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	gen1 := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("old-file"))
	if err := client.Refresh(ctx, gen1); err != nil {
		t.Fatalf("gen1 refresh: %v", err)
	}

	// A brand-new payload advertised through fresh targets, but the mirror
	// keeps serving the cached timestamp/snapshot without advancing them.
	freshTargets := &TargetFile{
		Type:    typeTargets,
		Version: 11,
		Expires: env.now.Add(180 * 24 * time.Hour),
	}
	freshTargets.Length, freshTargets.Hashes = payloadHashes(t, []byte("new-file"))
	freshEnvelope := env.sign(freshTargets, env.signer("tg1"))

	attempt := gen1
	attempt.Targets = map[string]*Envelope{testTargetPath: freshEnvelope}
	err := client.Refresh(ctx, attempt)
	if !errors.Is(err, ErrDigestMismatch) && !errors.Is(err, ErrRollback) {
		t.Fatalf("expected digest/rollback rejection, got %v", err)
	}

	// And VerifyTarget must never accept the new file under the old state.
	if _, err := client.VerifyTarget(ctx, testTargetPath, []byte("new-file")); err == nil {
		t.Fatal("new target file verified under stale timestamp metadata")
	}
	if _, err := client.VerifyTarget(ctx, testTargetPath, []byte("old-file")); err != nil {
		t.Fatalf("original payload should still verify: %v", err)
	}
}
