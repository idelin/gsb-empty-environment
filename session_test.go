package sessionwin

import (
	"reflect"
	"testing"
)

func collect(changes []Change) (retracts, upserts []Window) {
	for _, c := range changes {
		if c.Kind == Retract {
			retracts = append(retracts, c.Window)
		} else {
			upserts = append(upserts, c.Window)
		}
	}
	return
}

// Acceptance scenario: gap=5; t=0 and t=9 form two windows; late t=4
// merges them into [0,9] with retractions; removing t=4 splits again.
func TestLateEventMergeAndRetractSplit(t *testing.T) {
	m := NewMerger(5, 10)

	// t=0 arrives -> window [0,0]
	_, upserts := collect(m.Add(0))
	if !reflect.DeepEqual(upserts, []Window{{0, 0}}) {
		t.Fatalf("after Add(0): upserts=%v", upserts)
	}

	// t=9 arrives (gap 9 > 5) -> independent window [9,9]
	retracts, upserts := collect(m.Add(9))
	if len(retracts) != 0 || !reflect.DeepEqual(upserts, []Window{{9, 9}}) {
		t.Fatalf("after Add(9): retracts=%v upserts=%v", retracts, upserts)
	}
	if got := m.Windows(); !reflect.DeepEqual(got, []Window{{0, 0}, {9, 9}}) {
		t.Fatalf("two independent windows expected, got %v", got)
	}

	// Late t=4 bridges both windows -> retract both, upsert [0,9].
	retracts, upserts = collect(m.Add(4))
	if !reflect.DeepEqual(retracts, []Window{{0, 0}, {9, 9}}) {
		t.Fatalf("merge should retract both windows, got %v", retracts)
	}
	if !reflect.DeepEqual(upserts, []Window{{0, 9}}) {
		t.Fatalf("merge should upsert [0,9], got %v", upserts)
	}
	if got := m.Windows(); !reflect.DeepEqual(got, []Window{{0, 9}}) {
		t.Fatalf("merged window expected, got %v", got)
	}

	// Retract t=4 -> window splits back into [0,0] and [9,9].
	retracts, upserts = collect(m.Remove(4))
	if !reflect.DeepEqual(retracts, []Window{{0, 9}}) {
		t.Fatalf("split should retract [0,9], got %v", retracts)
	}
	if !reflect.DeepEqual(upserts, []Window{{0, 0}, {9, 9}}) {
		t.Fatalf("split should upsert [0,0] and [9,9], got %v", upserts)
	}
}

func TestWindowExtension(t *testing.T) {
	m := NewMerger(5, 100)
	m.Add(0)
	// t=3 is within gap of [0,0]: retract [0,0], upsert [0,3].
	retracts, upserts := collect(m.Add(3))
	if !reflect.DeepEqual(retracts, []Window{{0, 0}}) ||
		!reflect.DeepEqual(upserts, []Window{{0, 3}}) {
		t.Fatalf("extension: retracts=%v upserts=%v", retracts, upserts)
	}
	// Duplicate event changes nothing.
	if changes := m.Add(3); len(changes) != 0 {
		t.Fatalf("duplicate event should be a no-op, got %v", changes)
	}
}

func TestWatermarkDiscardsState(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	m.Add(9)
	m.Add(100)

	// Watermark 20 -> cutoff 10: events 0 and 9 are discarded.
	// Their windows end <= 10, so they are sealed without retraction.
	changes := m.AdvanceWatermark(20)
	if len(changes) != 0 {
		t.Fatalf("sealed windows must not emit changes, got %v", changes)
	}
	if got := m.Windows(); !reflect.DeepEqual(got, []Window{{100, 100}}) {
		t.Fatalf("only [100,100] should remain, got %v", got)
	}

	// Event at t=5 is now beyond maxLateness (5+10 <= 20): dropped.
	if changes := m.Add(5); len(changes) != 0 {
		t.Fatalf("too-late event must be dropped, got %v", changes)
	}
	if got := m.Windows(); !reflect.DeepEqual(got, []Window{{100, 100}}) {
		t.Fatalf("state must be unchanged, got %v", got)
	}

	// Watermark cannot move backwards.
	if changes := m.AdvanceWatermark(5); len(changes) != 0 || m.Watermark() != 20 {
		t.Fatalf("watermark regression must be ignored")
	}
}

func TestWatermarkUnsealsActiveWindow(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	m.Add(5) // window [0,5]

	// Watermark 14 -> cutoff 4: event 0 discarded, window shrinks to
	// [5,5]; since [0,5] ends above the cutoff it is retracted.
	retracts, upserts := collect(m.AdvanceWatermark(14))
	if !reflect.DeepEqual(retracts, []Window{{0, 5}}) ||
		!reflect.DeepEqual(upserts, []Window{{5, 5}}) {
		t.Fatalf("shrink: retracts=%v upserts=%v", retracts, upserts)
	}
}

func TestRemoveUnknownIsNoOp(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	if changes := m.Remove(42); len(changes) != 0 {
		t.Fatalf("removing unknown event must be a no-op, got %v", changes)
	}
}
