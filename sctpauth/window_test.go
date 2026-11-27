package sctpauth

import "testing"

func TestReceiveWindowOrderedAndReplay(t *testing.T) {
	window := NewReceiveWindow(1000, 4)

	for tsn := uint32(1000); tsn < 1005; tsn++ {
		if err := window.Accept(tsn); err != nil {
			t.Fatalf("accept in-order TSN %d: %v", tsn, err)
		}
	}
	if got := window.CumulativeTSN(); got != 1004 {
		t.Fatalf("cumulative TSN = %d, want 1004", got)
	}

	if err := window.Accept(1002); err != ErrDuplicateTSN {
		t.Fatalf("replayed TSN error = %v, want ErrDuplicateTSN", err)
	}
	if err := window.Accept(999); err != ErrDuplicateTSN {
		t.Fatalf("stale TSN error = %v, want ErrDuplicateTSN", err)
	}
}

func TestReceiveWindowOutOfWindowBlindInjection(t *testing.T) {
	window := NewReceiveWindow(100, 8)

	if err := window.Check(100); err != nil {
		t.Fatalf("initial TSN should be accepted: %v", err)
	}
	if err := window.Check(109); err != ErrTSNOutsideWindow {
		t.Fatalf("edge TSN at distance 9 error = %v, want ErrTSNOutsideWindow", err)
	}
	if err := window.Check(1_000_000); err != ErrTSNOutsideWindow {
		t.Fatalf("far future TSN error = %v, want ErrTSNOutsideWindow", err)
	}

	if err := window.Accept(105); err != nil {
		t.Fatalf("in-window gap TSN: %v", err)
	}
	if err := window.Accept(105); err != ErrDuplicateTSN {
		t.Fatalf("duplicate gap TSN error = %v, want ErrDuplicateTSN", err)
	}

	if err := window.Accept(100); err != nil {
		t.Fatalf("fill base TSN: %v", err)
	}
	if got := window.CumulativeTSN(); got != 100 {
		t.Fatalf("cumulative TSN after gap = %d, want 100", got)
	}
	if err := window.Accept(101); err != nil {
		t.Fatalf("continue window: %v", err)
	}
	if got := window.CumulativeTSN(); got != 101 {
		t.Fatalf("cumulative TSN = %d, want 101", got)
	}
}

func TestReceiveWindowWraparound(t *testing.T) {
	const base = ^uint32(0) - 2 // 4294967293
	window := NewReceiveWindow(base, 4)

	ordered := make([]uint32, 5)
	tsn := base
	for i := range ordered {
		ordered[i] = tsn
		tsn++
	}
	for _, tsn := range ordered {
		if err := window.Accept(tsn); err != nil {
			t.Fatalf("wrapping TSN %d: %v", tsn, err)
		}
	}
	if got := window.CumulativeTSN(); got != 1 {
		t.Fatalf("cumulative TSN after wrap = %d, want 1", got)
	}
	if err := window.Accept(base - 1); err != ErrDuplicateTSN {
		t.Fatalf("pre-wrap stale TSN error = %v, want ErrDuplicateTSN", err)
	}
}

func TestReceiveWindowCheckDoesNotMutate(t *testing.T) {
	window := NewReceiveWindow(7, 4)
	if err := window.Check(7); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := window.Accept(7); err != nil {
		t.Fatalf("accept after check should still succeed: %v", err)
	}
}
