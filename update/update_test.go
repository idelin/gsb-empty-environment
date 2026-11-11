package update

import (
	"context"
	"testing"
	"time"
)

func TestBootstrapAndRefresh(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	rootV1Envelope, rootV1 := env.rootV1()
	assertEnvelopeRoundTrips(t, rootV1Envelope)

	payload := []byte("release-1 payload")
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, payload)
	if err := client.Refresh(ctx, bundle); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	target, err := client.VerifyTarget(ctx, testTargetPath, payload)
	if err != nil {
		t.Fatalf("verify target: %v", err)
	}
	if target.Version != 10 {
		t.Fatalf("target version = %d, want 10", target.Version)
	}

	// State must survive reopening the store from disk.
	reopened := NewClient(NewFileStore(store.dir)).WithClock(env.clock())
	if _, err := reopened.VerifyTarget(ctx, testTargetPath, payload); err != nil {
		t.Fatalf("verify after reopen: %v", err)
	}

	// A tampered payload fails length/digest checks.
	if _, err := reopened.VerifyTarget(ctx, testTargetPath, []byte("release-1 payload tampered")); err == nil {
		t.Fatal("expected tampered payload to be rejected")
	}

	// A second refresh must strictly increase every version.
	bundle2 := env.refreshBundle(rootV1, nil, 11, 11, 11, []byte("release-2 payload"))
	if err := reopened.Refresh(ctx, bundle2); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
}

func TestBootstrapRejectsWeakSelfSignature(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	client := NewClient(NewFileStore(t.TempDir())).WithClock(env.clock())

	_, root := env.rootV1()
	envelope := env.sign(root, env.signer("ra"))
	assertErrorIs(t, client.Bootstrap(ctx, envelope), ErrThresholdNotMet)
}

func TestBootstrapTwiceRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	rootEnvelope, _ := env.rootV1()
	assertErrorIs(t, client.Bootstrap(ctx, rootEnvelope), ErrAlreadyBootstrapped)
}

func TestRootRotationRequiresBothThresholds(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	rootV2Envelope, rootV2 := env.rootV2()
	bundle := env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("rotated payload"))
	if err := client.Refresh(ctx, bundle); err != nil {
		t.Fatalf("rotation refresh: %v", err)
	}

	version, err := client.TrustedRootVersion(ctx)
	if err != nil {
		t.Fatalf("root version: %v", err)
	}
	if version != 2 {
		t.Fatalf("trusted root version = %d, want 2", version)
	}

	// After rotation a v1 timestamp key is no longer authorized.
	_, rootV1 := env.rootV1()
	oldKeyBundle := env.refreshBundle(rootV1, nil, 21, 21, 21, []byte("old key payload"))
	assertErrorIs(t, client.Refresh(ctx, oldKeyBundle), ErrThresholdNotMet)
}

func TestRootRotationFailsWhenOldThresholdMissing(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV2 := env.rootV2()
	weakEnvelope := env.sign(rootV2, env.signer("rc"), env.signer("rd"))
	bundle := env.refreshBundle(rootV2, weakEnvelope, 20, 20, 20, []byte("p"))
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrRootRotation)
}

func TestRootRotationFailsWhenNewThresholdMissing(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV2 := env.rootV2()
	weakEnvelope := env.sign(rootV2, env.signer("ra"), env.signer("rb"))
	bundle := env.refreshBundle(rootV2, weakEnvelope, 20, 20, 20, []byte("p"))
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrRootRotation)

	version, err := client.TrustedRootVersion(ctx)
	if err != nil {
		t.Fatalf("root version: %v", err)
	}
	if version != 1 {
		t.Fatalf("trusted root version = %d, want 1 after failed rotation", version)
	}
}

func TestRootRotationRollbackRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV2 := env.rootV2()
	rootV2.Version = 1
	envelope := env.sign(rootV2,
		env.signer("ra"), env.signer("rb"),
		env.signer("rc"), env.signer("rd"),
	)
	bundle := env.refreshBundle(rootV2, envelope, 20, 20, 20, []byte("p"))
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrRollback)
}

func TestDuplicateAliasCountsOnce(t *testing.T) {
	env := newTestEnv(t)

	rootEnvelope, root := env.rootV1()
	parsed, err := ParseEnvelope(rootEnvelope.Bytes())
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}

	count, err := countValidSignatures(root, RoleRoot, parsed)
	if err != nil {
		t.Fatalf("count signatures: %v", err)
	}
	if count != 2 {
		t.Fatalf("distinct alias count = %d, want 2", count)
	}

	// Two valid signatures under the same alias still count once. Build the
	// envelope without the builder's alias dedup to exercise the verifier.
	payload := parsed.SignedBytes()
	dupEnvelope := &Envelope{
		Signed: parsed.Signed,
		Signatures: []Signature{
			env.signer("ra").Sign(payload),
			env.signer("ra").Sign(payload),
		},
	}
	dupRaw, err := jsonMarshal(dupEnvelope)
	if err != nil {
		t.Fatalf("marshal duplicate envelope: %v", err)
	}
	dupEnvelope, err = ParseEnvelope(dupRaw)
	if err != nil {
		t.Fatalf("parse duplicate envelope: %v", err)
	}
	count, err = countValidSignatures(root, RoleRoot, dupEnvelope)
	if err != nil {
		t.Fatalf("count duplicate signatures: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate alias count = %d, want 1", count)
	}
	assertErrorIs(t, verifyRole(root, RoleRoot, dupEnvelope), ErrThresholdNotMet)
}

func TestMetadataRollbackRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	first := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p1"))
	if err := client.Refresh(ctx, first); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Replaying the exact same bundle is a version rollback/replay.
	assertErrorIs(t, client.Refresh(ctx, first), ErrRollback)

	// An older timestamp version with otherwise fresh data is rejected.
	older := env.refreshBundle(rootV1, nil, 9, 9, 9, []byte("p2"))
	assertErrorIs(t, client.Refresh(ctx, older), ErrRollback)

	// Snapshot version not matching the timestamp-pinned version.
	mismatched := env.refreshBundle(rootV1, nil, 11, 11, 11, []byte("p3"))
	timestamp := mustDecodeTimestamp(t, mismatched.Timestamp)
	timestamp.Meta.Version = 99
	mismatched.Timestamp = env.sign(timestamp, env.signer("ts1"))
	assertErrorIs(t, client.Refresh(ctx, mismatched), ErrRollback)
}

func TestExpiredMetadataRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p"))

	env.now = env.now.Add(31 * 24 * time.Hour)
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrExpired)

	// Valid refresh first, then time travel: VerifyTarget must refuse an
	// expired timestamp even with the correct payload.
	env.now = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	if err := client.Refresh(ctx, bundle); err != nil {
		t.Fatalf("refresh before time travel: %v", err)
	}
	env.now = env.now.Add(31 * 24 * time.Hour)
	if _, err := client.VerifyTarget(ctx, testTargetPath, []byte("p")); !errorIs(err, ErrExpired) {
		t.Fatalf("expected ErrExpired from VerifyTarget, got %v", err)
	}
}
