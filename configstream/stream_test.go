package configstream

import (
	"errors"
	"reflect"
	"testing"
)

func mustState(t *testing.T, r *Receiver, want map[string]string, wantRev uint64) {
	t.Helper()
	if got := r.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %v, want %v", got, want)
	}
	if got := r.Revision(); got != wantRev {
		t.Fatalf("revision = %d, want %d", got, wantRev)
	}
}

// Multiple changes in one revision plus a commit marker: nothing is
// visible until the marker arrives, then everything applies atomically.
func TestAtomicCommitAcrossChunks(t *testing.T) {
	r := NewReceiver()
	if err := r.ApplySnapshot(1, map[string]string{"keep": "1", "drop": "x"}); err != nil {
		t.Fatal(err)
	}

	if err := r.ApplyChunk(2, 0, []Change{{Op: OpDelete, Key: "drop"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyChunk(2, 1, []Change{{Op: OpPut, Key: "new", Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	// No commit marker yet: staged changes must not be visible.
	mustState(t, r, map[string]string{"keep": "1", "drop": "x"}, 1)

	if err := r.Commit(2, 2); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"keep": "1", "new": "v"}, 2)
}

// A missing sequence number must block finalization even after the
// commit marker arrived; the revision applies once the gap is filled.
func TestMissingSequenceWaits(t *testing.T) {
	r := NewReceiver()

	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyChunk(1, 2, []Change{{Op: OpPut, Key: "c", Value: "3"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(1, 3); err != nil {
		t.Fatal(err)
	}
	// Sequence 1 is missing: nothing may be applied.
	mustState(t, r, map[string]string{}, 0)

	if err := r.ApplyChunk(1, 1, []Change{{Op: OpPut, Key: "b", Value: "2"}}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"a": "1", "b": "2", "c": "3"}, 1)
}

// The commit marker may arrive before any chunk.
func TestCommitMarkerBeforeChunks(t *testing.T) {
	r := NewReceiver()
	if err := r.Commit(1, 2); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{}, 0)

	if err := r.ApplyChunk(1, 1, []Change{{Op: OpPut, Key: "b", Value: "2"}}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{}, 0)

	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"a": "1", "b": "2"}, 1)
}

// Replaying an already committed revision with the same change set is a
// no-op, even if the chunking differs from the original delivery.
func TestDuplicateRevisionConsistentReplay(t *testing.T) {
	r := NewReceiver()
	changes := []Change{
		{Op: OpPut, Key: "a", Value: "1"},
		{Op: OpPut, Key: "b", Value: "2"},
		{Op: OpDelete, Key: "gone"},
	}
	if err := r.ApplyChunk(1, 0, changes[:1]); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyChunk(1, 1, changes[1:]); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyChunk(2, 0, []Change{{Op: OpPut, Key: "c", Value: "3"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(2, 1); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "1", "b": "2", "c": "3"}
	mustState(t, r, want, 2)

	// Replay revision 1 with a different chunking: consistent, no-op.
	if err := r.ApplyChunk(1, 0, changes); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(1, 1); err != nil {
		t.Fatalf("consistent replay rejected: %v", err)
	}
	mustState(t, r, want, 2)
}

// Replaying an old revision with a different change set must be
// reported as inconsistent and must not touch the committed state.
func TestDuplicateRevisionConflictingReplay(t *testing.T) {
	r := NewReceiver()
	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(1, 1); err != nil {
		t.Fatal(err)
	}

	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "tampered"}}); err != nil {
		t.Fatal(err)
	}
	err := r.Commit(1, 1)
	var ire *InconsistentReplayError
	if !errors.As(err, &ire) {
		t.Fatalf("err = %v, want InconsistentReplayError", err)
	}
	if ire.Revision != 1 {
		t.Fatalf("ire.Revision = %d, want 1", ire.Revision)
	}
	mustState(t, r, map[string]string{"a": "1"}, 1)
}

// Disconnecting drops uncommitted staging; a later commit marker for
// the same revision must not resurrect it.
func TestDisconnectDiscardsStaging(t *testing.T) {
	r := NewReceiver()
	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	r.Disconnect()

	// The staged chunk is gone, so this commit cannot finalize.
	if err := r.Commit(1, 1); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{}, 0)

	// After a redelivery of the full revision, it applies normally.
	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"a": "1"}, 1)
}

// Disconnect also drops a pending commit marker that arrived early.
func TestDisconnectDropsPendingCommitMarker(t *testing.T) {
	r := NewReceiver()
	if err := r.Commit(1, 1); err != nil {
		t.Fatal(err)
	}
	r.Disconnect()

	// The marker is gone: this chunk alone must not finalize.
	if err := r.ApplyChunk(1, 0, []Change{{Op: OpPut, Key: "a", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{}, 0)
}

// Deletes and adds in the same revision, delivered as chunked events,
// must produce exactly the state predicted by the snapshot model —
// regardless of how the changes are split into chunks.
func TestDeleteAndAddSameRevisionMatchesSnapshotModel(t *testing.T) {
	base := map[string]string{"stale": "old", "keep": "k", "flip": "before"}
	revision := []Change{
		{Op: OpDelete, Key: "stale"},                   // delete existing key
		{Op: OpPut, Key: "fresh", Value: "new"},        // add new key
		{Op: OpPut, Key: "flip", Value: "after"},       // overwrite existing key
		{Op: OpDelete, Key: "flip"},                    // delete it again, same revision
		{Op: OpPut, Key: "flip", Value: "resurrected"}, // and add it back, same revision
		{Op: OpDelete, Key: "never-existed"},           // delete of absent key
	}
	want := ApplyChanges(base, revision)

	chunkings := [][][]Change{
		{revision}, // one chunk
		{revision[:2], revision[2:]},
		{revision[:1], revision[1:3], revision[3:]}, // delete/add pairs split across chunks
	}
	for i, chunks := range chunkings {
		r := NewReceiver()
		if err := r.ApplySnapshot(7, base); err != nil {
			t.Fatal(err)
		}
		for seq, chunk := range chunks {
			if err := r.ApplyChunk(8, seq, chunk); err != nil {
				t.Fatalf("chunking %d: %v", i, err)
			}
		}
		// Interleave a duplicate delivery of the first chunk: harmless.
		if err := r.ApplyChunk(8, 0, chunks[0]); err != nil {
			t.Fatalf("chunking %d: %v", i, err)
		}
		if err := r.Commit(8, len(chunks)); err != nil {
			t.Fatalf("chunking %d: %v", i, err)
		}
		if got := r.State(); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunking %d: state = %v, want snapshot model %v", i, got, want)
		}
		if got := r.Revision(); got != 8 {
			t.Fatalf("chunking %d: revision = %d, want 8", i, got)
		}
	}
}

// A snapshot is an atomic resync point: it replaces the state, sets the
// revision, drops superseded staging, and stale snapshots are ignored.
func TestSnapshotResync(t *testing.T) {
	r := NewReceiver()
	if err := r.ApplyChunk(3, 0, []Change{{Op: OpPut, Key: "pending", Value: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySnapshot(5, map[string]string{"snap": "s"}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"snap": "s"}, 5)

	// Staging for revision 3 was superseded by the snapshot.
	if err := r.Commit(3, 1); err == nil {
		t.Fatal("commit for superseded revision should report unknown revision")
	} else {
		var ure *UnknownRevisionError
		if !errors.As(err, &ure) {
			t.Fatalf("err = %v, want UnknownRevisionError", err)
		}
	}
	mustState(t, r, map[string]string{"snap": "s"}, 5)

	// Stale snapshots are ignored.
	if err := r.ApplySnapshot(4, map[string]string{"stale": "snapshot"}); err != nil {
		t.Fatal(err)
	}
	mustState(t, r, map[string]string{"snap": "s"}, 5)
}
