package netsec

import (
	"math"
	"testing"
)

func TestReplayWindowOrderingAndWindowEdge(t *testing.T) {
	var window replayWindow

	steps := []struct {
		sequence uint64
		want     replayDecision
	}{
		{1000, replayAccepted},
		{1, replayAccepted},
		{999, replayAccepted},
		{1, replayDuplicate},
		{5000, replayAccepted},
		{3977, replayAccepted},
		{3976, replayTooOld},
		{5001, replayAccepted},
		{3978, replayAccepted},
		{3977, replayTooOld},
	}
	for _, step := range steps {
		if got := window.accept(step.sequence); got != step.want {
			t.Fatalf("accept(%d) = %v, want %v", step.sequence, got, step.want)
		}
	}
}

func TestReplayWindowShiftDistances(t *testing.T) {
	for _, distance := range []uint64{1, 2, 63, 64, 65, 127, 128, 1023, 1024, 2048} {
		var window replayWindow
		if got := window.accept(0); got != replayAccepted {
			t.Fatalf("distance %d: initial accept = %v", distance, got)
		}
		if distance < 1024 {
			if got := window.accept(distance); got != replayAccepted {
				t.Fatalf("distance %d: advancing accept = %v", distance, got)
			}
			if got := window.accept(0); got != replayDuplicate {
				t.Fatalf("distance %d: old sequence should be present, got %v", distance, got)
			}
			continue
		}

		if got := window.accept(distance); got != replayAccepted {
			t.Fatalf("distance %d: advancing accept = %v", distance, got)
		}
		if got := window.accept(0); got != replayTooOld {
			t.Fatalf("distance %d: old sequence should be too old, got %v", distance, got)
		}
	}
}

func TestReplayWindowDoesNotWrap(t *testing.T) {
	var window replayWindow
	if got := window.accept(math.MaxUint64); got != replayAccepted {
		t.Fatalf("accept(MaxUint64) = %v", got)
	}
	if got := window.accept(math.MaxUint64 - 1023); got != replayAccepted {
		t.Fatalf("accept(MaxUint64-1023) = %v", got)
	}
	if got := window.accept(math.MaxUint64 - 1024); got != replayTooOld {
		t.Fatalf("accept(MaxUint64-1024) = %v", got)
	}
	if got := window.accept(0); got != replayTooOld {
		t.Fatalf("accept(0) after MaxUint64 = %v", got)
	}
}

func TestReplayWindowReset(t *testing.T) {
	var window replayWindow
	window.accept(10)
	window.reset()
	if got := window.accept(10); got != replayAccepted {
		t.Fatalf("accept after reset = %v", got)
	}
}
