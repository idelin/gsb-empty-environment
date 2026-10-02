package replay

import (
	"bytes"
	"testing"
)

const (
	testSeed        = 2
	testEnvSeed     = 0xDEADBEEF
	testPacketCount = 50
	testTargetEvent = 35
)

// TestRecordedTraceShape verifies the acceptance trace contains exactly
// 50 packet arrivals and 3 timeout clock interrupts.
func TestRecordedTraceShape(t *testing.T) {
	kernel, _ := Record(testSeed, testEnvSeed, testPacketCount)
	log := kernel.Log()
	if got := log.Count(EventPacketArrival); got != 50 {
		t.Fatalf("expected 50 packet arrivals, got %d", got)
	}
	if got := log.Count(EventTimeout); got != 3 {
		t.Fatalf("expected 3 timeouts, got %d", got)
	}
	if got := log.Count(EventSeed); got != 1 {
		t.Fatalf("expected 1 seed event, got %d", got)
	}
	if log.Events[0].Type != EventSeed {
		t.Fatalf("first log entry must record the random seed")
	}
}

// TestReplayToEvent35MatchesSnapshot is the core acceptance check: replay
// to the 35th event and require a byte-identical state hash.
func TestReplayToEvent35MatchesSnapshot(t *testing.T) {
	kernel, snaps := Record(testSeed, testEnvSeed, testPacketCount)
	hist := snaps[testTargetEvent-1]

	replayed, err := ReplayTo(kernel.Log(), testTargetEvent)
	if err != nil {
		t.Fatalf("ReplayTo: %v", err)
	}
	now := replayed.Snapshot()

	if now.Hash != hist.Hash {
		t.Fatalf("hash mismatch at event %d:\n recorded %x\n replayed %x",
			testTargetEvent, hist.Hash, now.Hash)
	}
	if !bytes.Equal(now.Bytes, hist.Bytes) {
		t.Fatalf("serialized state mismatch at event %d", testTargetEvent)
	}
	if now.Tick != hist.Tick {
		t.Fatalf("tick mismatch: recorded %d, replayed %d", hist.Tick, now.Tick)
	}
}

// TestReplayEveryEvent checks determinism at every single tick, not just
// the acceptance checkpoint.
func TestReplayEveryEvent(t *testing.T) {
	kernel, snaps := Record(testSeed, testEnvSeed, testPacketCount)
	log := kernel.Log()
	if len(snaps) != log.Len() {
		t.Fatalf("expected %d snapshots, got %d", log.Len(), len(snaps))
	}
	for n := 1; n <= log.Len(); n++ {
		replayed, err := ReplayTo(log, n)
		if err != nil {
			t.Fatalf("ReplayTo(%d): %v", n, err)
		}
		if got := replayed.Snapshot(); got.Hash != snaps[n-1].Hash {
			t.Fatalf("state diverged at event %d", n)
		}
	}
}

// TestTimeTravelIsSideEffectFree jumps the live kernel into the past and
// back, and requires the live run and its log to be untouched.
func TestTimeTravelIsSideEffectFree(t *testing.T) {
	kernel, snaps := Record(testSeed, testEnvSeed, testPacketCount)
	log := kernel.Log()
	finalHash := kernel.Snapshot().Hash
	logLen := log.Len()

	if err := kernel.TravelTo(testTargetEvent); err != nil {
		t.Fatalf("TravelTo(%d): %v", testTargetEvent, err)
	}
	if got := kernel.Snapshot().Hash; got != snaps[testTargetEvent-1].Hash {
		t.Fatalf("travelled state does not match history at event %d", testTargetEvent)
	}

	if err := kernel.TravelTo(logLen); err != nil {
		t.Fatalf("TravelTo(%d): %v", logLen, err)
	}
	if got := kernel.Snapshot().Hash; got != finalHash {
		t.Fatalf("live run mutated by time travel")
	}
	if log.Len() != logLen {
		t.Fatalf("event log mutated by time travel: %d -> %d", logLen, log.Len())
	}
}

// TestDifferentSeedsDiverge is a sanity check that the snapshot hash
// actually depends on the recorded seed.
func TestDifferentSeedsDiverge(t *testing.T) {
	a, _ := Record(testSeed, testEnvSeed, testPacketCount)
	b, _ := Record(testSeed+1, testEnvSeed, testPacketCount)
	if a.Snapshot().Hash == b.Snapshot().Hash {
		t.Fatalf("different seeds produced identical final state")
	}
}
