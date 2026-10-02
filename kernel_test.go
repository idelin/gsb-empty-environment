package kernel

import (
	"bytes"
	"testing"
)

// recordReferenceTrace records the acceptance trace: 50 packet arrivals
// and exactly 3 retransmission timeouts, plus clock interrupts.
func recordReferenceTrace(t *testing.T) *Kernel {
	t.Helper()
	k := RecordTrace(TraceSeed, BuildTrace())
	if got := k.Log().CountKind(KindPacketArrival); got != 50 {
		t.Fatalf("trace must contain 50 packet arrivals, got %d", got)
	}
	if got := k.Log().CountKind(KindTimeout); got != 3 {
		t.Fatalf("trace must contain 3 timeouts, got %d", got)
	}
	return k
}

// TestAcceptanceReplayToEvent35 is the acceptance boundary: replay the
// recorded trace to the 35th event and require the reconstructed state to
// be byte-for-byte identical to the historical snapshot.
func TestAcceptanceReplayToEvent35(t *testing.T) {
	recorded := recordReferenceTrace(t)
	log := recorded.Log()
	t.Logf("recorded trace: %d events (%d arrivals, %d timeouts, %d clock interrupts), seed=%d",
		log.Len(),
		log.CountKind(KindPacketArrival),
		log.CountKind(KindTimeout),
		log.CountKind(KindClockInterrupt),
		log.Seed)

	const eventNumber = 35 // 1-based: "the 35th event"
	index := eventNumber - 1
	if log.Len() <= index {
		t.Fatalf("trace too short: %d events", log.Len())
	}

	dbg := NewDebugger(log)
	replayed, err := dbg.Goto(index)
	if err != nil {
		t.Fatalf("time travel to event %d failed: %v", eventNumber, err)
	}

	want := recorded.SnapshotAt(index)
	got := replayed.SnapshotAt(index)
	t.Logf("event %d: %s", eventNumber, log.Events[index])
	t.Logf("historical snapshot hash: %x", want.Hash)
	t.Logf("replayed   state   hash: %x", got.Hash)

	if want.Hash != got.Hash {
		t.Fatalf("snapshot hash mismatch at event %d:\nrecorded %x\nreplayed %x",
			eventNumber, want.Hash, got.Hash)
	}
	if !bytes.Equal(want.Bytes, got.Bytes) {
		t.Fatalf("state bytes differ at event %d", eventNumber)
	}
}

// TestTimeTravelEveryCheckpoint generalises the acceptance test: jumping
// to any historical event must reproduce that moment exactly.
func TestTimeTravelEveryCheckpoint(t *testing.T) {
	recorded := recordReferenceTrace(t)
	dbg := NewDebugger(recorded.Log())

	for i := 0; i < recorded.Log().Len(); i++ {
		replayed, err := dbg.Goto(i)
		if err != nil {
			t.Fatalf("Goto(%d): %v", i, err)
		}
		want := recorded.SnapshotAt(i)
		got := replayed.SnapshotAt(i)
		if want.Hash != got.Hash {
			t.Fatalf("hash mismatch at event %d (%s):\nrecorded %x\nreplayed %x",
				i, recorded.Log().Events[i], want.Hash, got.Hash)
		}
		if !bytes.Equal(want.Bytes, got.Bytes) {
			t.Fatalf("state bytes differ at event %d", i)
		}
	}
}

// TestTimeTravelIsSideEffectFree verifies that jumping around in time does
// not disturb the kernel that recorded the trace.
func TestTimeTravelIsSideEffectFree(t *testing.T) {
	recorded := recordReferenceTrace(t)
	last := recorded.Log().Len() - 1
	before := recorded.SnapshotAt(last)

	dbg := NewDebugger(recorded.Log())
	for _, index := range []int{0, 17, 34, 7, last} {
		if _, err := dbg.Goto(index); err != nil {
			t.Fatalf("Goto(%d): %v", index, err)
		}
	}

	after := recorded.SnapshotAt(last)
	if before.Hash != after.Hash || !bytes.Equal(before.Bytes, after.Bytes) {
		t.Fatal("time travel mutated the recording kernel")
	}
}

// TestReplayReDerivesInternalEvents ensures timeouts are not replayed from
// the log but genuinely re-derived from the seed and external inputs.
func TestReplayReDerivesInternalEvents(t *testing.T) {
	recorded := recordReferenceTrace(t)
	dbg := NewDebugger(recorded.Log())

	replayed, err := dbg.Goto(recorded.Log().Len() - 1)
	if err != nil {
		t.Fatalf("full replay: %v", err)
	}
	if replayed.Log().Len() != recorded.Log().Len() {
		t.Fatalf("replayed %d events, recorded %d", replayed.Log().Len(), recorded.Log().Len())
	}
	for i := range recorded.Log().Events {
		if replayed.Log().Events[i] != recorded.Log().Events[i] {
			t.Fatalf("event %d diverged: recorded %v, replayed %v",
				i, recorded.Log().Events[i], replayed.Log().Events[i])
		}
	}
	if replayed.Stats() != recorded.Stats() {
		t.Fatalf("stats diverged: recorded %+v, replayed %+v", recorded.Stats(), replayed.Stats())
	}
}

// TestGotoOutOfRange checks the debugger's bounds handling.
func TestGotoOutOfRange(t *testing.T) {
	recorded := recordReferenceTrace(t)
	dbg := NewDebugger(recorded.Log())
	if _, err := dbg.Goto(-1); err == nil {
		t.Fatal("expected error for negative index")
	}
	if _, err := dbg.Goto(recorded.Log().Len()); err == nil {
		t.Fatal("expected error for index past the end")
	}
}
