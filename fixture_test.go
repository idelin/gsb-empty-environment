package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSynthDeterministic(t *testing.T) {
	a := SynthEvents(42, 100, 4, 0.5)
	b := SynthEvents(42, 100, 4, 0.5)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed must produce identical events")
	}
	c := SynthEvents(43, 100, 4, 0.5)
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds should produce different events")
	}
}

func TestFixtureRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	want := SynthEvents(7, 25, 3, 0.4)
	if err := WriteFixture(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip mismatch:\nwant %v\ngot  %v", want, got)
	}
}

// TestReplayFixtureConservation replays a fixture through the queue and
// verifies the conservation invariant holds for the replayed stream.
func TestReplayFixtureConservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	events := SynthEvents(9, 60, 3, 0.5)
	if err := WriteFixture(path, events); err != nil {
		t.Fatal(err)
	}
	replayed, err := ReadFixture(path)
	if err != nil {
		t.Fatal(err)
	}

	q := NewQueue(16, DefaultHighBurst, PolicyRejectNew)
	pool := NewPool(q, func(Event) {})
	pool.Start(2)
	enqueueAll(q, replayed)
	q.Close(Drain)
	pool.Wait()

	snap := q.Snapshot()
	if snap.Received != uint64(len(events)) {
		t.Fatalf("received=%d want %d", snap.Received, len(events))
	}
	if !snap.Conserved() {
		t.Fatalf("conservation violated: %+v", snap)
	}
}
