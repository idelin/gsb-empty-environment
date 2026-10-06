package sim

import (
	"reflect"
	"testing"
)

func applyAll(t *testing.T, e *Engine, events []Event) {
	t.Helper()
	for _, ev := range events {
		if err := e.Apply(ev); err != nil {
			t.Fatalf("Apply(%+v): %v", ev, err)
		}
	}
}

func buildLog(start uint64, ids ...string) []Event {
	var out []Event
	seq := start
	for _, id := range ids {
		out = append(out, Event{Seq: seq, Kind: KindAddEntity, EntityID: id, Key: "hp", Delta: 100})
		seq++
	}
	return out
}

func mutationLog(start uint64, id string, n int) []Event {
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Event{Seq: start + uint64(i), Kind: KindMutate, EntityID: id, Key: "hp", Delta: int64(i)})
	}
	return out
}

// Two branches forked from one checkpoint must reach identical state and
// identical RNG draw counts when replayed with the same events.
func TestForkReplayIdentical(t *testing.T) {
	base := New(42)
	applyAll(t, base, buildLog(0, "alpha", "beta"))
	applyAll(t, base, mutationLog(2, "alpha", 5))
	applyAll(t, base, mutationLog(7, "beta", 3))

	// Serialize through the wire format so the fork is fully independent.
	data, err := MarshalCheckpoint(base.Checkpoint())
	if err != nil {
		t.Fatalf("MarshalCheckpoint: %v", err)
	}
	cp, err := UnmarshalCheckpoint(data)
	if err != nil {
		t.Fatalf("UnmarshalCheckpoint: %v", err)
	}
	branchA, err := Restore(cp)
	if err != nil {
		t.Fatalf("Restore A: %v", err)
	}
	branchB, err := Restore(cp)
	if err != nil {
		t.Fatalf("Restore B: %v", err)
	}

	tail := append(mutationLog(10, "alpha", 7), mutationLog(17, "beta", 4)...)
	applyAll(t, branchA, tail)
	applyAll(t, branchB, tail)

	if got, want := branchA.StateHash(), branchB.StateHash(); got != want {
		t.Fatalf("state hash mismatch after replay: A=%x B=%x", got, want)
	}
	if got, want := branchA.RNGCalls(), branchB.RNGCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RNG call counts mismatch: A=%v B=%v", got, want)
	}

	// And the branches must match a single uninterrupted run of the same log.
	solo := New(42)
	applyAll(t, solo, buildLog(0, "alpha", "beta"))
	applyAll(t, solo, mutationLog(2, "alpha", 5))
	applyAll(t, solo, mutationLog(7, "beta", 3))
	applyAll(t, solo, tail)
	if got, want := branchA.StateHash(), solo.StateHash(); got != want {
		t.Fatalf("checkpoint round-trip changed trajectory: fork=%x solo=%x", got, want)
	}
}

// Adding an unrelated entity must not perturb an existing entity's random
// sequence or resulting attributes.
func TestUnrelatedEntityDoesNotPerturbStreams(t *testing.T) {
	lonely := New(7)
	applyAll(t, lonely, buildLog(0, "alpha"))
	applyAll(t, lonely, mutationLog(1, "alpha", 10))

	crowded := New(7)
	applyAll(t, crowded, buildLog(0, "alpha", "gamma", "delta"))
	applyAll(t, crowded, mutationLog(3, "alpha", 10))
	applyAll(t, crowded, mutationLog(13, "gamma", 4))

	wantCalls := lonely.RNGCalls()["alpha"]
	if got := crowded.RNGCalls()["alpha"]; got != wantCalls {
		t.Fatalf("alpha RNG calls changed: got %d, want %d", got, wantCalls)
	}
	for _, key := range []string{"hp"} {
		got, _ := crowded.Attr("alpha", key)
		want, _ := lonely.Attr("alpha", key)
		if got != want {
			t.Fatalf("alpha attr %q changed: got %d, want %d", key, got, want)
		}
	}
}

// Event order is explicit: gaps, duplicates, and rewinds are rejected.
func TestExplicitEventOrderEnforced(t *testing.T) {
	e := New(1)
	if err := e.Apply(Event{Seq: 1, Kind: KindAddEntity, EntityID: "a"}); err == nil {
		t.Fatal("expected gap in sequence to be rejected")
	}
	if err := e.Apply(Event{Seq: 0, Kind: KindAddEntity, EntityID: "a"}); err != nil {
		t.Fatalf("in-order event rejected: %v", err)
	}
	if err := e.Apply(Event{Seq: 0, Kind: KindAddEntity, EntityID: "b"}); err == nil {
		t.Fatal("expected duplicate sequence to be rejected")
	}
	if got := e.NextSeq(); got != 1 {
		t.Fatalf("NextSeq = %d, want 1", got)
	}
}

// Checkpoints from unknown versions must be refused, both in memory and on
// the wire.
func TestRejectUnknownCheckpointVersion(t *testing.T) {
	e := New(9)
	applyAll(t, e, buildLog(0, "alpha"))

	cp := e.Checkpoint()
	cp.Version = CheckpointVersion + 1000
	if _, err := Restore(cp); err == nil {
		t.Fatal("Restore accepted unknown checkpoint version")
	}

	data, err := MarshalCheckpoint(cp)
	if err != nil {
		t.Fatalf("MarshalCheckpoint: %v", err)
	}
	if _, err := UnmarshalCheckpoint(data); err == nil {
		t.Fatal("UnmarshalCheckpoint accepted unknown checkpoint version")
	}

	// The current version still round-trips.
	good, err := MarshalCheckpoint(e.Checkpoint())
	if err != nil {
		t.Fatalf("MarshalCheckpoint: %v", err)
	}
	if _, err := UnmarshalCheckpoint(good); err != nil {
		t.Fatalf("current-version checkpoint rejected: %v", err)
	}
}
