package configstream

import (
	"math/rand"
	"reflect"
	"testing"
)

func upsert(key, value string) Change { return Change{Type: ChangeUpsert, Key: key, Value: value} }
func del(key string) Change           { return Change{Type: ChangeDelete, Key: key} }

func mustHandle(t *testing.T, s *Stream, ev Event) {
	t.Helper()
	if err := s.Handle(ev); err != nil {
		t.Fatalf("Handle(%+v) returned unexpected error: %v", ev, err)
	}
}

func assertSnapshot(t *testing.T, s *Stream, wantRev uint64, want map[string]string) {
	t.Helper()
	got, rev := s.Snapshot()
	if rev != wantRev {
		t.Fatalf("revision = %d, want %d", rev, wantRev)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

// model is the reference snapshot model used to cross-check the stream.
type model struct {
	data map[string]string
}

func newModel() *model { return &model{data: make(map[string]string)} }

func (m *model) commit(changes []Change) { applyChanges(m.data, changes) }

// TestAtomicCommitAcrossChunks verifies that a revision mixing deletes
// and adds, delivered as several chunks, only becomes visible when the
// commit marker arrives.
func TestAtomicCommitAcrossChunks(t *testing.T) {
	s := NewStream()
	m := newModel()

	rev1 := []Change{upsert("a", "1"), upsert("b", "2"), upsert("c", "3")}
	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: rev1, Commit: true})
	m.commit(rev1)
	assertSnapshot(t, s, 1, m.data)

	// Revision 2 deletes a and b, updates c, adds d — split into chunks.
	chunks := []Event{
		{Revision: 2, Seq: 1, Changes: []Change{del("a"), upsert("d", "4")}},
		{Revision: 2, Seq: 2, Changes: []Change{del("b"), upsert("c", "33")}},
		{Revision: 2, Seq: 3, Commit: true},
	}
	mustHandle(t, s, chunks[0])
	mustHandle(t, s, chunks[1])
	// Nothing applied before the commit marker.
	assertSnapshot(t, s, 1, map[string]string{"a": "1", "b": "2", "c": "3"})

	mustHandle(t, s, chunks[2])
	for _, ch := range chunks {
		m.commit(ch.Changes)
	}
	assertSnapshot(t, s, 2, m.data)
}

// TestDeleteThenReaddSameRevision checks last-write-wins inside one
// revision against the model.
func TestDeleteThenReaddSameRevision(t *testing.T) {
	s := NewStream()
	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("x", "old")}, Commit: true})

	changes := []Change{del("x"), upsert("x", "new")}
	mustHandle(t, s, Event{Revision: 2, Seq: 1, Changes: changes})
	mustHandle(t, s, Event{Revision: 2, Seq: 2, Commit: true})
	assertSnapshot(t, s, 2, map[string]string{"x": "new"})
}

// TestOutOfOrderChunksWait verifies chunks with missing predecessors are
// buffered, not applied, and drain in order once the gap is filled.
func TestOutOfOrderChunksWait(t *testing.T) {
	s := NewStream()

	mustHandle(t, s, Event{Revision: 1, Seq: 1, Changes: []Change{upsert("b", "2")}})
	mustHandle(t, s, Event{Revision: 1, Seq: 2, Commit: true})
	if got := s.Pending(); got != 2 {
		t.Fatalf("Pending() = %d, want 2", got)
	}
	assertSnapshot(t, s, 0, map[string]string{})

	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}})
	if got := s.Pending(); got != 0 {
		t.Fatalf("Pending() = %d, want 0", got)
	}
	assertSnapshot(t, s, 1, map[string]string{"a": "1", "b": "2"})
}

// TestMissingSeqBlocksCommit verifies a commit marker that arrives while
// an earlier chunk is still missing is buffered, so nothing is applied
// partially.
func TestMissingSeqBlocksCommit(t *testing.T) {
	s := NewStream()

	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}})
	mustHandle(t, s, Event{Revision: 1, Seq: 2, Changes: []Change{upsert("c", "3")}, Commit: true})
	assertSnapshot(t, s, 0, map[string]string{})

	mustHandle(t, s, Event{Revision: 1, Seq: 1, Changes: []Change{upsert("b", "2")}})
	assertSnapshot(t, s, 1, map[string]string{"a": "1", "b": "2", "c": "3"})
}

// TestReplayConsistentRevision verifies re-sending a committed revision
// with identical content is accepted as a no-op.
func TestReplayConsistentRevision(t *testing.T) {
	s := NewStream()
	rev1 := []Event{
		{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}},
		{Revision: 1, Seq: 1, Changes: []Change{del("z"), upsert("b", "2")}, Commit: true},
	}
	for _, ev := range rev1 {
		mustHandle(t, s, ev)
	}

	s.Disconnect()
	for i, ev := range rev1 {
		ev.Seq = uint64(i) // sender restarts the session sequence
		mustHandle(t, s, ev)
	}
	assertSnapshot(t, s, 1, map[string]string{"a": "1", "b": "2"})
}

// TestReplayConflictingRevision verifies a re-sent old revision whose
// content differs from what was committed is rejected.
func TestReplayConflictingRevision(t *testing.T) {
	s := NewStream()
	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}, Commit: true})

	s.Disconnect()
	err := s.Handle(Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "tampered")}, Commit: true})
	if err != ErrInconsistentReplay {
		t.Fatalf("Handle() error = %v, want ErrInconsistentReplay", err)
	}
	assertSnapshot(t, s, 1, map[string]string{"a": "1"})
}

// TestDisconnectDiscardsStaging verifies uncommitted staged changes and
// buffered chunks are dropped on disconnect, and the session restarts
// cleanly from sequence 0.
func TestDisconnectDiscardsStaging(t *testing.T) {
	s := NewStream()
	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}, Commit: true})

	// Stage half of revision 2 plus one out-of-order chunk, then drop.
	mustHandle(t, s, Event{Revision: 2, Seq: 1, Changes: []Change{del("a")}})
	mustHandle(t, s, Event{Revision: 2, Seq: 3, Changes: []Change{upsert("b", "2")}})
	s.Disconnect()
	if got := s.Pending(); got != 0 {
		t.Fatalf("Pending() = %d, want 0", got)
	}
	assertSnapshot(t, s, 1, map[string]string{"a": "1"})

	// Reconnect: a different revision 2 must apply cleanly from seq 0.
	mustHandle(t, s, Event{Revision: 2, Seq: 0, Changes: []Change{upsert("c", "3")}, Commit: true})
	assertSnapshot(t, s, 2, map[string]string{"a": "1", "c": "3"})
}

// TestInterleavedRevisionsRejected verifies chunks of a second revision
// cannot sneak in before the first one commits.
func TestInterleavedRevisionsRejected(t *testing.T) {
	s := NewStream()
	mustHandle(t, s, Event{Revision: 1, Seq: 0, Changes: []Change{upsert("a", "1")}})
	if err := s.Handle(Event{Revision: 2, Seq: 1, Changes: []Change{upsert("b", "2")}}); err != ErrRevisionInterleaved {
		t.Fatalf("Handle() error = %v, want ErrRevisionInterleaved", err)
	}
}

// TestChunkedStreamMatchesSnapshotModel drives the stream with
// randomized chunked revisions — adds, updates and deletes in the same
// revision, shuffled delivery, occasional mid-revision disconnects —
// and cross-checks every committed snapshot against the model.
func TestChunkedStreamMatchesSnapshotModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewStream()
	m := newModel()
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

	seq := uint64(0)
	for rev := uint64(1); rev <= 30; rev++ {
		// Build a revision with random adds/updates/deletes.
		var changes []Change
		for _, k := range keys {
			switch rng.Intn(4) {
			case 0, 1:
				changes = append(changes, upsert(k, string(rune('a'+rng.Intn(26)))+"-v"))
			case 2:
				changes = append(changes, del(k))
			}
		}

		// Split into chunks, last one carries the commit marker.
		var events []Event
		for len(changes) > 0 {
			n := 1 + rng.Intn(len(changes))
			events = append(events, Event{Revision: rev, Seq: seq, Changes: changes[:n]})
			changes = changes[n:]
			seq++
		}
		events = append(events, Event{Revision: rev, Seq: seq, Commit: true})
		seq++

		// Occasionally drop the connection mid-revision: staged state
		// must be discarded and the revision re-sent from scratch.
		if rev%7 == 0 && len(events) > 1 {
			cut := 1 + rng.Intn(len(events)-1)
			for _, ev := range events[:cut] {
				mustHandle(t, s, ev)
			}
			s.Disconnect()
			seq = 0
			for i := range events {
				events[i].Seq = uint64(i)
			}
			seq = uint64(len(events))
		}

		// Deliver chunks in shuffled order to exercise the wait buffer.
		perm := rng.Perm(len(events))
		for _, i := range perm {
			mustHandle(t, s, events[i])
		}

		// Commit the same changes to the model and compare snapshots.
		for _, ev := range events {
			m.commit(ev.Changes)
		}
		assertSnapshot(t, s, rev, m.data)
	}
}
