package netsec

import (
	"errors"
	"math"
	"testing"
	"time"
)

var testTime = time.Unix(1_700_000_000, 0)

func mustAccept(t *testing.T, state *KeyState, neighbor string, sequence, counter uint32, now time.Time) uint8 {
	t.Helper()
	index, err := state.AcceptFrame(neighbor, sequence, counter, now)
	if err != nil {
		t.Fatalf("AcceptFrame(%q, %d, %d) rejected: %v", neighbor, sequence, counter, err)
	}
	return index
}

func mustReject(t *testing.T, state *KeyState, want error, neighbor string, sequence, counter uint32, now time.Time) {
	t.Helper()
	_, err := state.AcceptFrame(neighbor, sequence, counter, now)
	if !errors.Is(err, want) {
		t.Fatalf("AcceptFrame(%q, %d, %d) error = %v, want %v", neighbor, sequence, counter, err, want)
	}
}

func TestKeyIndex(t *testing.T) {
	tests := []struct {
		sequence uint32
		want     uint8
	}{
		{0, 1},
		{1, 2},
		{126, 127},
		{127, 128},
		{128, 1},
		{255, 128},
		{256, 1},
	}
	for _, tt := range tests {
		if got := KeyIndex(tt.sequence); got != tt.want {
			t.Errorf("KeyIndex(%d) = %d, want %d", tt.sequence, got, tt.want)
		}
	}
}

func TestSequenceMustAdvanceByExactlyOne(t *testing.T) {
	state := NewAt(10, 10*time.Second, testTime)

	mustAccept(t, state, "node-a", 10, 1, testTime)

	mustReject(t, state, ErrSequenceSkipped, "node-a", 12, 1, testTime.Add(10*time.Second))
	if got := state.CurrentSequence(); got != 10 {
		t.Fatalf("skipped sequence changed current sequence to %d", got)
	}

	// A rejected skip must not consume the frame counter or switch time.
	mustAccept(t, state, "node-a", 10, 2, testTime.Add(10*time.Second))
	mustAccept(t, state, "node-a", 11, 1, testTime.Add(10*time.Second))
}

func TestSwitchRequiresProtectionPeriod(t *testing.T) {
	const protection = 100 * time.Millisecond
	state := NewAt(7, protection, testTime)

	mustReject(t, state, ErrProtectionPeriod, "node-a", 8, 1, testTime.Add(protection-1))
	if got := state.CurrentSequence(); got != 7 {
		t.Fatalf("rejected switch changed current sequence to %d", got)
	}
	if got := state.LastSwitch(); !got.Equal(testTime) {
		t.Fatalf("rejected switch changed last switch to %v", got)
	}

	mustAccept(t, state, "node-a", 8, 1, testTime.Add(protection))
	if got := state.CurrentSequence(); got != 8 {
		t.Fatalf("accepted switch left current sequence at %d", got)
	}
	if got := state.CurrentKeyIndex(); got != KeyIndex(8) {
		t.Fatalf("CurrentKeyIndex() = %d, want %d", got, KeyIndex(8))
	}

	mustReject(t, state, ErrProtectionPeriod, "node-a", 9, 1, testTime.Add(2*protection-1))
	mustAccept(t, state, "node-a", 9, 1, testTime.Add(2*protection))
}

func TestProtectionPeriodStartsAtConstruction(t *testing.T) {
	state := NewAt(0, time.Second, testTime)
	mustReject(t, state, ErrProtectionPeriod, "node-a", 1, 1, testTime.Add(time.Second/2))
	mustAccept(t, state, "node-a", 1, 1, testTime.Add(time.Second))
}

func TestClockRollbackDoesNotBypassProtection(t *testing.T) {
	state := NewAt(1, time.Second, testTime)
	mustReject(t, state, ErrProtectionPeriod, "node-a", 2, 1, testTime.Add(-time.Second))
}

func TestRetiredSequenceCannotBeReenabled(t *testing.T) {
	state := NewAt(20, 0, testTime)

	mustAccept(t, state, "node-a", 20, 100, testTime)
	mustAccept(t, state, "node-a", 21, 100, testTime)
	mustAccept(t, state, "node-a", 22, 100, testTime)

	// Replays under the old sequence are rejected even with a fresh counter.
	mustReject(t, state, ErrSequenceRetired, "node-a", 20, 101, testTime)
	mustReject(t, state, ErrSequenceRetired, "node-a", 21, 101, testTime)

	// Sequence 148 has the same 7-bit key index as retired sequence 20, but
	// it is still a skipped sequence and must not alias the old key.
	if KeyIndex(148) != KeyIndex(20) {
		t.Fatal("test setup: sequences do not alias")
	}
	mustReject(t, state, ErrSequenceSkipped, "node-a", 148, 101, testTime)
}

func TestRetiredSequenceIsRejectedDespiteKeyIndexAlias(t *testing.T) {
	state := NewAt(128, 0, testTime)
	if KeyIndex(0) != KeyIndex(128) {
		t.Fatal("test setup: sequences do not alias")
	}

	// Sequence zero has the same key index as sequence 128, but it was
	// retired before the current sequence and must stay retired.
	mustReject(t, state, ErrSequenceRetired, "node-a", 0, 1, testTime)
}

func TestFrameCounterIsStrictlyIncreasingPerNeighbor(t *testing.T) {
	state := NewAt(42, 0, testTime)

	if got := mustAccept(t, state, "node-a", 42, 0, testTime); got != KeyIndex(42) {
		t.Fatalf("first frame key index = %d, want %d", got, KeyIndex(42))
	}
	mustAccept(t, state, "node-a", 42, 2, testTime)

	mustReject(t, state, ErrFrameCounter, "node-a", 42, 0, testTime)
	mustReject(t, state, ErrFrameCounter, "node-a", 42, 2, testTime)
	mustReject(t, state, ErrFrameCounter, "node-a", 42, 1, testTime)

	// A rejected replay must not lower the high-water mark.
	mustAccept(t, state, "node-a", 42, 3, testTime)

	// Counters are independent for every neighbor.
	mustAccept(t, state, "node-b", 42, 0, testTime)
	mustAccept(t, state, "node-b", 42, 1, testTime)
	mustReject(t, state, ErrFrameCounter, "node-b", 42, 1, testTime)
}

func TestCountersRestartUnderNewSequence(t *testing.T) {
	state := NewAt(50, 0, testTime)
	mustAccept(t, state, "node-a", 50, 1000, testTime)

	mustAccept(t, state, "node-a", 51, 0, testTime)
	mustAccept(t, state, "node-a", 51, 1, testTime)

	// The retired sequence remains rejected regardless of its counter.
	mustReject(t, state, ErrSequenceRetired, "node-a", 50, 1001, testTime)
}

func TestMaxSequenceDoesNotWrap(t *testing.T) {
	state := NewAt(math.MaxUint32, 0, testTime)
	mustAccept(t, state, "node-a", math.MaxUint32, 1, testTime)

	// There is no representable successor, and sequence zero must not be
	// treated as one.
	mustReject(t, state, ErrSequenceRetired, "node-a", 0, 2, testTime)
	if got := state.CurrentSequence(); got != math.MaxUint32 {
		t.Fatalf("current sequence changed to %d", got)
	}
}

func TestRejectedFrameDoesNotAdvanceCounter(t *testing.T) {
	state := NewAt(1, 0, testTime)
	mustAccept(t, state, "node-a", 1, 10, testTime)
	mustReject(t, state, ErrFrameCounter, "node-a", 1, 9, testTime)
	mustAccept(t, state, "node-a", 1, 11, testTime)
}
