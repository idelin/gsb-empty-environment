package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotDigestChainEnforced(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p"))

	// Re-sign a snapshot whose digest field was edited, so it remains a
	// validly signed envelope whose bytes no longer match the timestamp's
	// pinned digest.
	snapshot := mustDecodeSnapshot(t, bundle.Snapshot)
	for algorithm, sum := range snapshot.Meta[testTargetPath].Hashes {
		sum[0] ^= 0xFF
		snapshot.Meta[testTargetPath].Hashes[algorithm] = sum
	}
	bundle.Snapshot = env.sign(snapshot, env.signer("ss1"))
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrDigestMismatch)

	// A freshly signed timestamp that advertises the wrong snapshot length
	// must fail the transactional length check.
	bundle2 := env.refreshBundle(rootV1, nil, 11, 11, 11, []byte("p2"))
	timestamp := mustDecodeTimestamp(t, bundle2.Timestamp)
	timestamp.Meta.Length++
	bundle2.Timestamp = env.sign(timestamp, env.signer("ts1"))
	assertErrorIs(t, client.Refresh(ctx, bundle2), ErrLengthMismatch)
}

func TestTargetsEnvelopeDigestEnforced(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p"))

	// Snapshot pins targets v10 bytes; swap in a different validly signed
	// targets envelope whose bytes differ.
	other := &TargetFile{
		Type:    typeTargets,
		Version: 10,
		Expires: env.now.Add(180 * 24 * time.Hour),
	}
	other.Length, other.Hashes = payloadHashes(t, []byte("seven7"))
	otherEnvelope := env.sign(other, env.signer("tg1"))
	bundle.Targets = map[string]*Envelope{testTargetPath: otherEnvelope}
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrDigestMismatch)
}

func TestOldTimestampWithNewTargetsRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	first := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p1"))
	if err := client.Refresh(ctx, first); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// New targets document at version 12 while the mirror still serves the
	// old timestamp/snapshot generation: the digest chain cannot validate.
	newTargets := &TargetFile{
		Type:    typeTargets,
		Version: 12,
		Expires: env.now.Add(180 * 24 * time.Hour),
	}
	newTargets.Length, newTargets.Hashes = payloadHashes(t, []byte("brand new payload"))
	newTargetsEnvelope := env.sign(newTargets, env.signer("tg1"))

	mixed := first
	mixed.Targets = map[string]*Envelope{testTargetPath: newTargetsEnvelope}
	if err := client.Refresh(ctx, mixed); !errors.Is(err, ErrDigestMismatch) && !errors.Is(err, ErrRollback) {
		t.Fatalf("expected digest or rollback rejection, got %v", err)
	}

	// Even with a rewritten snapshot pointing at the new targets, replaying
	// the old timestamp version is a rollback.
	snapshot := &SnapshotFile{
		Type:    typeSnapshot,
		Version: 10,
		Expires: env.now.Add(180 * 24 * time.Hour),
		Meta: map[string]MetaRef{
			testTargetPath: envelopeMetaRef(12, newTargetsEnvelope),
		},
	}
	snapshotEnvelope := env.sign(snapshot, env.signer("ss1"))
	timestamp := &TimestampFile{
		Type:    typeTimestamp,
		Version: 10,
		Expires: env.now.Add(30 * 24 * time.Hour),
		Meta:    envelopeMetaRef(10, snapshotEnvelope),
	}
	timestampEnvelope := env.sign(timestamp, env.signer("ts1"))
	replay := Bundle{
		Timestamp: timestampEnvelope,
		Snapshot:  snapshotEnvelope,
		Targets:   map[string]*Envelope{testTargetPath: newTargetsEnvelope},
	}
	assertErrorIs(t, client.Refresh(ctx, replay), ErrRollback)
}

func TestNonCanonicalEnvelopeRejected(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p"))

	withLeadingSpace := append([]byte(" "), bundle.Timestamp.Bytes()...)
	if _, err := ParseEnvelope(withLeadingSpace); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("expected ErrNonCanonical for whitespace, got %v", err)
	}

	trailing := append(append([]byte(nil), bundle.Timestamp.Bytes()...), ' ', '\n')
	if _, err := ParseEnvelope(trailing); !errors.Is(err, ErrNonCanonical) && !errors.Is(err, ErrTrailingData) {
		t.Fatalf("expected rejection for trailing data, got %v", err)
	}

	// A well-formed but re-key-ordered inner payload breaks signature
	// coverage because its bytes are no longer canonical.
	raw := bundle.Timestamp.Bytes()
	reordered := reorderJSONKeys(t, raw)
	if _, err := ParseEnvelope(reordered); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("expected ErrNonCanonical for reordered payload, got %v", err)
	}

	// Refresh with a tampered-but-parseable timestamp is rejected.
	timestamp := mustDecodeTimestamp(t, bundle.Timestamp)
	timestamp.Meta.Version = 11
	bundle.Timestamp = env.sign(timestamp, env.signer("ts1"))
	// The snapshot stays at version 10 while timestamp now pins 11.
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrRollback)
}

func TestTransactionRejectsBeforeAnyCommit(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	dir := t.TempDir()
	store := NewFileStore(dir)
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	good := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p1"))
	if err := client.Refresh(ctx, good); err != nil {
		t.Fatalf("good refresh: %v", err)
	}
	goodState := readStateFile(t, dir)

	bad := env.refreshBundle(rootV1, nil, 9, 11, 11, []byte("p2"))
	assertErrorIs(t, client.Refresh(ctx, bad), ErrRollback)

	// The rejected transaction must not have touched the committed file.
	if got := readStateFile(t, dir); string(got) != string(goodState) {
		t.Fatal("rejected refresh modified committed state")
	}
}

func TestInterruptibleStorePowerLossDuringRotation(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	rootV2Envelope, rootV2 := env.rootV2()

	for _, point := range []CrashPoint{CrashBeforeWrite, CrashAfterTempWrite, CrashAfterRename} {
		point := point
		t.Run(string(point), func(t *testing.T) {
			dir := t.TempDir()
			// Bootstrap with no armed crash, then arm power loss for the
			// rotation commit.
			store := NewInterruptibleStore(dir, point)
			store.Disarm()
			client := env.bootstrapClient(ctx, store)
			store.Arm(point)

			bundle := env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("rotated"))
			err := client.Refresh(ctx, bundle)
			if !errors.Is(err, ErrSimulatedPowerLoss) {
				t.Fatalf("expected simulated power loss at %s, got %v", point, err)
			}

			// Reopen exactly as a rebooted client would.
			reopenedStore := NewFileStore(dir)
			reopened := NewClient(reopenedStore).WithClock(env.clock())
			version, err := reopened.TrustedRootVersion(ctx)
			if err != nil {
				t.Fatalf("load state after crash at %s: %v", point, err)
			}
			switch point {
			case CrashBeforeWrite, CrashAfterTempWrite:
				if version != 1 {
					t.Fatalf("root version after pre-rename crash = %d, want 1", version)
				}
			case CrashAfterRename:
				if version != 2 {
					t.Fatalf("root version after post-rename crash = %d, want 2", version)
				}
			}

			// A fresh retry on a non-injecting store converges to the new
			// generation. For the post-rename case the same bundle now
			// looks like a replay, so build the next generation instead.
			steadyStore := NewFileStore(dir)
			steady := NewClient(steadyStore).WithClock(env.clock())
			var retryBundle Bundle
			switch point {
			case CrashAfterRename:
				retryBundle = env.refreshBundle(rootV2, nil, 21, 21, 21, []byte("rotated-2"))
			default:
				retryBundle = env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("rotated"))
			}
			if err := steady.Refresh(ctx, retryBundle); err != nil {
				t.Fatalf("converging refresh after crash: %v", err)
			}
			version, err = steady.TrustedRootVersion(ctx)
			if err != nil {
				t.Fatalf("root version after convergence: %v", err)
			}
			if version != 2 {
				t.Fatalf("root version = %d, want 2", version)
			}

			// No stray temp file remains after a successful commit.
			if _, err := os.Stat(filepath.Join(dir, "trusted-state.json.tmp")); !os.IsNotExist(err) {
				t.Fatalf("temp file should be gone, stat err = %v", err)
			}
		})
	}
}

func TestStaleTempFileDoesNotBlockRecovery(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	dir := t.TempDir()

	store := NewInterruptibleStore(dir, CrashAfterTempWrite)
	store.Disarm()
	client := env.bootstrapClient(ctx, store)
	store.Arm(CrashAfterTempWrite)
	rootV2Envelope, rootV2 := env.rootV2()
	bundle := env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("p"))
	if err := client.Refresh(ctx, bundle); !errors.Is(err, ErrSimulatedPowerLoss) {
		t.Fatalf("expected power loss, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trusted-state.json.tmp")); err != nil {
		t.Fatalf("temp file expected after crash: %v", err)
	}

	// Recovery overwrites the stale temp file and completes atomically.
	recovered := NewClient(NewFileStore(dir)).WithClock(env.clock())
	nextBundle := env.refreshBundle(rootV2, rootV2Envelope, 20, 20, 20, []byte("p"))
	if err := recovered.Refresh(ctx, nextBundle); err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	version, err := recovered.TrustedRootVersion(ctx)
	if err != nil {
		t.Fatalf("root version: %v", err)
	}
	if version != 2 {
		t.Fatalf("root version = %d, want 2", version)
	}
}

func TestRefreshContextCancellation(t *testing.T) {
	env := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	store := NewFileStore(t.TempDir())
	client := env.bootstrapClient(ctx, store)

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 10, 10, 10, []byte("p"))
	cancel()
	if err := client.Refresh(ctx, bundle); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func mustDecodeTimestamp(t *testing.T, envelope *Envelope) *TimestampFile {
	t.Helper()
	parsed, err := ParseEnvelope(envelope.Bytes())
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	timestamp, err := parseTimestamp(parsed)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	return timestamp
}

func mustDecodeSnapshot(t *testing.T, envelope *Envelope) *SnapshotFile {
	t.Helper()
	parsed, err := ParseEnvelope(envelope.Bytes())
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	snapshot, err := parseSnapshot(parsed)
	if err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	return snapshot
}

func readStateFile(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "trusted-state.json"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	return raw
}

// reorderJSONKeys rewrites the inner "signed" object so its first two keys
// are swapped. encoding/json sorts map keys, so decode into an ordered struct
// and emit manually with swapped key order.
func reorderJSONKeys(t *testing.T, envelopeRaw []byte) []byte {
	t.Helper()
	var outer struct {
		Signed     map[string]json.RawMessage `json:"signed"`
		Signatures []Signature                `json:"signatures"`
	}
	if err := json.Unmarshal(envelopeRaw, &outer); err != nil {
		t.Fatalf("decode envelope for reorder: %v", err)
	}
	keys := make([]string, 0, len(outer.Signed))
	for key := range outer.Signed {
		keys = append(keys, key)
	}
	if len(keys) < 2 {
		t.Fatal("need at least two signed keys to reorder")
	}
	keys[0], keys[1] = keys[1], keys[0]

	var builder strings.Builder
	builder.WriteString(`{"signed":{`)
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		builder.Write(name)
		builder.WriteByte(':')
		builder.Write(outer.Signed[key])
	}
	builder.WriteString(`},"signatures":`)
	signatures, err := json.Marshal(outer.Signatures)
	if err != nil {
		t.Fatalf("marshal signatures: %v", err)
	}
	builder.Write(signatures)
	builder.WriteByte('}')
	return []byte(builder.String())
}
