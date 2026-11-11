package revocation

import (
	"errors"
	"sync"
	"testing"
)

func newTestSubscriber(t *testing.T) *Subscriber {
	t.Helper()
	sub, err := NewSubscriber(nil)
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	return sub
}

func revoke(gen, seq int64, id string) Event {
	return Event{Generation: gen, Sequence: seq, CredentialID: id, Revoked: true}
}

func unrevoke(gen, seq int64, id string) Event {
	return Event{Generation: gen, Sequence: seq, CredentialID: id, Revoked: false}
}

func mustApplyEvent(t *testing.T, sub *Subscriber, event Event) {
	t.Helper()
	if err := sub.ApplyEvent(event); err != nil {
		t.Fatalf("ApplyEvent(%+v): %v", event, err)
	}
}

func mustApplyCheckpoint(t *testing.T, sub *Subscriber, cp Checkpoint) {
	t.Helper()
	if err := sub.ApplyCheckpoint(cp); err != nil {
		t.Fatalf("ApplyCheckpoint(%+v): %v", cp, err)
	}
}

func mustApplySnapshot(t *testing.T, sub *Subscriber, snap Snapshot) {
	t.Helper()
	if err := sub.ApplySnapshot(snap); err != nil {
		t.Fatalf("ApplySnapshot(%+v): %v", snap, err)
	}
}

func requireSnapshotRequest(t *testing.T, sub *Subscriber, want Checkpoint) {
	t.Helper()
	select {
	case got := <-sub.SnapshotRequests():
		if got != want {
			t.Fatalf("snapshot request = %+v, want %+v", got, want)
		}
	default:
		t.Fatalf("expected snapshot request %+v, channel empty", want)
	}
}

func requireNoSnapshotRequest(t *testing.T, sub *Subscriber) {
	t.Helper()
	select {
	case got := <-sub.SnapshotRequests():
		t.Fatalf("unexpected snapshot request %+v", got)
	default:
	}
}

func TestFreshSubscriberFailsClosed(t *testing.T) {
	sub := newTestSubscriber(t)
	if sub.Synced() || sub.CaughtUp() {
		t.Fatal("fresh subscriber must not report synced")
	}
	if !sub.IsRevoked("anything") {
		t.Fatal("fresh subscriber must deny every credential")
	}
	if sub.Valid("anything") {
		t.Fatal("fresh subscriber must not validate any credential")
	}
}

func TestContiguousApplicationReachesCheckpoint(t *testing.T) {
	sub := newTestSubscriber(t)

	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	if sub.Synced() {
		t.Fatal("must not sync before a checkpoint arrives")
	}

	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})
	if !sub.Synced() {
		t.Fatal("contiguous run up to checkpoint must sync")
	}
	if !sub.IsRevoked("alice") {
		t.Fatal("alice must be revoked")
	}
	if sub.IsRevoked("bob") {
		t.Fatal("bob must be valid")
	}

	mustApplyEvent(t, sub, revoke(1, 2, "bob"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 2})
	if !sub.IsRevoked("bob") {
		t.Fatal("bob must be revoked after second event")
	}
}

func TestDuplicateEventsAreIdempotent(t *testing.T) {
	sub := newTestSubscriber(t)
	for seq := int64(1); seq <= 3; seq++ {
		mustApplyEvent(t, sub, revoke(1, seq, "id"))
	}
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 3})

	for _, seq := range []int64{1, 2, 3, 2, 1} {
		mustApplyEvent(t, sub, revoke(1, seq, "id"))
	}

	status := sub.Status()
	if !status.Synced() {
		t.Fatal("duplicates must not break synced state")
	}
	if status.NextSequence != 4 {
		t.Fatalf("NextSequence = %d, want 4", status.NextSequence)
	}
	requireNoSnapshotRequest(t, sub)
}

func TestDuplicateCheckpointsAreIdempotent(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "id"))
	for range 3 {
		mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})
	}
	if !sub.Synced() {
		t.Fatal("duplicate checkpoints must keep synced state")
	}
}

func TestOutOfOrderEventOpensGapAndRequestsSnapshot(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyEvent(t, sub, revoke(1, 2, "bob"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 2})

	// Sequence 3 is lost; 4 arrives first.
	mustApplyEvent(t, sub, revoke(1, 4, "carol"))

	if sub.Synced() {
		t.Fatal("out-of-order event must break synced state")
	}
	if sub.Valid("bob") {
		t.Fatal("gap state must deny previously valid credentials")
	}
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 2})

	// The missing event arrives during the repair: buffered, still denying.
	mustApplyEvent(t, sub, revoke(1, 3, "dave"))
	if sub.Synced() {
		t.Fatal("buffered events must not sync before the snapshot lands")
	}

	mustApplySnapshot(t, sub, Snapshot{
		Generation:   1,
		LastSequence: 2,
		Revoked:      map[string]bool{"alice": true, "bob": true},
	})
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 4})

	if !sub.Synced() {
		t.Fatal("snapshot plus buffered increments must restore sync")
	}
	for _, id := range []string{"alice", "bob", "dave", "carol"} {
		if !sub.IsRevoked(id) {
			t.Fatalf("%s must be revoked after atomic repair", id)
		}
	}
	requireNoSnapshotRequest(t, sub)
}

func TestEventsArrivingDuringSnapshotAreBufferedAndMergedAtomically(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	// Lose 2; 3 opens the gap.
	mustApplyEvent(t, sub, revoke(1, 3, "carol"))
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	// More events land while the snapshot is in flight, including a
	// duplicate of an already buffered sequence.
	mustApplyEvent(t, sub, revoke(1, 4, "dave"))
	mustApplyEvent(t, sub, unrevoke(1, 5, "alice"))
	mustApplyEvent(t, sub, revoke(1, 4, "dave"))

	// Snapshot only covers up to 2; increments 3..5 must merge on top.
	mustApplySnapshot(t, sub, Snapshot{
		Generation:   1,
		LastSequence: 2,
		Revoked:      map[string]bool{"alice": true, "bob": true},
	})
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 5})

	if !sub.Synced() {
		t.Fatal("snapshot merged with buffered increments must sync")
	}
	if sub.IsRevoked("alice") {
		t.Fatal("buffered unrevoke of alice must win over the snapshot")
	}
	for _, id := range []string{"bob", "carol", "dave"} {
		if !sub.IsRevoked(id) {
			t.Fatalf("%s must be revoked after merge", id)
		}
	}
}

func TestSnapshotWithRemainingGapKeepsDenyingAndRequestsAgain(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	// Lose 2,3,4; 5 arrives and is buffered.
	mustApplyEvent(t, sub, revoke(1, 5, "eve"))
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	// Snapshot covers only up to 2; buffered 5 is still discontinuous.
	mustApplySnapshot(t, sub, Snapshot{
		Generation:   1,
		LastSequence: 2,
		Revoked:      map[string]bool{"alice": true},
	})
	if sub.Synced() {
		t.Fatal("remaining gap must keep the subscriber denying")
	}
	if sub.Valid("alice") {
		t.Fatal("unrepaired subscriber must not validate")
	}
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	// The buffered event must survive the failed repair: a snapshot covering
	// up to 4 must merge buffered 5 without re-delivering it.
	mustApplySnapshot(t, sub, Snapshot{
		Generation:   1,
		LastSequence: 4,
		Revoked:      map[string]bool{"alice": true},
	})
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 5})
	if !sub.Synced() {
		t.Fatal("second snapshot must complete the repair")
	}
	if !sub.IsRevoked("eve") {
		t.Fatal("buffered event must survive the failed first repair")
	}
}

func TestStaleAndUnexpectedSnapshotsAreRejected(t *testing.T) {
	sub := newTestSubscriber(t)

	if err := sub.ApplySnapshot(Snapshot{Generation: 1, Revoked: map[string]bool{}}); !errors.Is(err, ErrUnexpectedSnapshot) {
		t.Fatalf("snapshot without gap: %v", err)
	}

	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})
	mustApplyEvent(t, sub, revoke(1, 3, "carol")) // opens gap on generation 1
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 1})

	err := sub.ApplySnapshot(Snapshot{Generation: 2, LastSequence: 9, Revoked: map[string]bool{}})
	if !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("wrong-generation snapshot: %v", err)
	}
	if sub.Synced() {
		t.Fatal("stale snapshot must not repair the gap")
	}
}

func TestGenerationRolloverDiscardsStateAndRequestsSnapshot(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})
	if !sub.IsRevoked("alice") {
		t.Fatal("precondition: alice revoked in generation 1")
	}

	// Generation rollover is an explicit discontinuity.
	mustApplyEvent(t, sub, revoke(2, 1, "zoe"))
	if sub.Synced() {
		t.Fatal("rollover must break synced state")
	}
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 2, Sequence: 0})

	// Events from the retired generation are ignored.
	mustApplyEvent(t, sub, revoke(1, 2, "mallory"))
	if sub.Status().Generation != 2 {
		t.Fatal("retired-generation events must not move the subscriber")
	}

	mustApplySnapshot(t, sub, Snapshot{Generation: 2, LastSequence: 0, Revoked: map[string]bool{}})
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 2, Sequence: 1})
	if !sub.Synced() {
		t.Fatal("generation 2 snapshot plus buffered event must sync")
	}
	if sub.IsRevoked("alice") {
		t.Fatal("rollover must discard the old generation's revocation set")
	}
	if !sub.IsRevoked("zoe") {
		t.Fatal("buffered rollover event must apply on top of the snapshot")
	}
}

func TestStaleSubscriptionCannotReportCaughtUp(t *testing.T) {
	sub := newTestSubscriber(t)
	for seq := int64(1); seq <= 3; seq++ {
		mustApplyEvent(t, sub, revoke(1, seq, "id"))
	}
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 3})
	if !sub.CaughtUp() {
		t.Fatal("precondition: subscriber caught up at 1:3")
	}

	// The publisher advances to 1:5 but events 4 and 5 never arrive. The
	// checkpoint alone must demote the subscriber: it is now stale.
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 5})
	if sub.CaughtUp() || sub.Synced() {
		t.Fatal("stale subscription must not report caught up")
	}
	if sub.Valid("id") {
		t.Fatal("stale subscription must deny judgments")
	}
	requireSnapshotRequest(t, sub, Checkpoint{Generation: 1, Sequence: 3})

	// A replayed older checkpoint must not resurrect caught-up status.
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 3})
	if sub.CaughtUp() {
		t.Fatal("older checkpoint must not resurrect caught-up status")
	}
}

func TestCheckpointFromRetiredGenerationIsIgnored(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(2, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 2, Sequence: 1})

	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 99})
	if !sub.Synced() {
		t.Fatal("retired-generation checkpoint must not disturb synced state")
	}
}

func TestUnrevokeEventLiftsRevocation(t *testing.T) {
	sub := newTestSubscriber(t)
	mustApplyEvent(t, sub, revoke(1, 1, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 1})
	if !sub.IsRevoked("alice") {
		t.Fatal("precondition: alice revoked")
	}

	mustApplyEvent(t, sub, unrevoke(1, 2, "alice"))
	mustApplyCheckpoint(t, sub, Checkpoint{Generation: 1, Sequence: 2})
	if sub.IsRevoked("alice") {
		t.Fatal("unrevoke event must lift the revocation")
	}
}

func TestInvalidInputsAreRejected(t *testing.T) {
	sub := newTestSubscriber(t)

	if err := sub.ApplyEvent(Event{Generation: 0, Sequence: 1, CredentialID: "x"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("zero generation: %v", err)
	}
	if err := sub.ApplyEvent(Event{Generation: 1, Sequence: 0, CredentialID: "x"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("zero sequence: %v", err)
	}
	if err := sub.ApplyEvent(Event{Generation: 1, Sequence: 1}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty credential: %v", err)
	}
	if err := sub.ApplyCheckpoint(Checkpoint{}); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("zero checkpoint: %v", err)
	}
	if err := sub.ApplySnapshot(Snapshot{}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("zero snapshot: %v", err)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	sub := newTestSubscriber(t)

	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := int64(worker * 1000)
			for seq := base + 1; seq <= base+50; seq++ {
				// Distinct generations per worker keep each writer's stream
				// internally dense while racing the others.
				_ = sub.ApplyEvent(revoke(base+1, seq-base, "id"))
				_ = sub.ApplyCheckpoint(Checkpoint{Generation: base + 1, Sequence: seq - base})
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				_ = sub.IsRevoked("id")
				_ = sub.Valid("id")
				_ = sub.Synced()
				_ = sub.Status()
				_ = sub.Position()
			}
		}()
	}
	wg.Wait()
}
