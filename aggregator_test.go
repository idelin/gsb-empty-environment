package pixelagg

import "testing"

func findPixel(t *testing.T, res WindowResult, x, y int) PixelSummary {
	t.Helper()
	for _, ps := range res.Pixels {
		if ps.Pixel.X == x && ps.Pixel.Y == y {
			return ps
		}
	}
	t.Fatalf("pixel (%d,%d) not found in window [%d,%d)", x, y, res.Start, res.End)
	return PixelSummary{}
}

func TestNetCountAndTimeSummary(t *testing.T) {
	a := New(10, 0)
	a.Add(Event{ID: "e1", X: 1, Y: 2, Polarity: 1, Timestamp: 3})
	a.Add(Event{ID: "e2", X: 1, Y: 2, Polarity: 1, Timestamp: 7})
	a.Add(Event{ID: "e3", X: 1, Y: 2, Polarity: -1, Timestamp: 5})
	a.Add(Event{ID: "e4", X: 4, Y: 4, Polarity: -1, Timestamp: 1})
	a.Add(Event{ID: "close", X: 9, Y: 9, Polarity: 1, Timestamp: 10}) // closes [0,10)

	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 closed window, got %d", len(results))
	}
	res := results[0]
	if res.Start != 0 || res.End != 10 {
		t.Fatalf("unexpected window bounds [%d,%d)", res.Start, res.End)
	}
	p := findPixel(t, res, 1, 2)
	if p.NetCount != 1 || p.First != 3 || p.Last != 7 {
		t.Fatalf("got net=%d first=%d last=%d", p.NetCount, p.First, p.Last)
	}
	q := findPixel(t, res, 4, 4)
	if q.NetCount != -1 || q.First != 1 || q.Last != 1 {
		t.Fatalf("got net=%d first=%d last=%d", q.NetCount, q.First, q.Last)
	}
}

func TestCancelledCountKeepsTimeSummary(t *testing.T) {
	a := New(10, 0)
	a.Add(Event{ID: "on", X: 3, Y: 3, Polarity: 1, Timestamp: 2})
	a.Add(Event{ID: "off", X: 3, Y: 3, Polarity: -1, Timestamp: 8})
	a.Add(Event{ID: "close", X: 0, Y: 0, Polarity: 1, Timestamp: 10})

	p := findPixel(t, a.Results()[0], 3, 3)
	if p.NetCount != 0 {
		t.Fatalf("expected net count 0, got %d", p.NetCount)
	}
	if p.First != 2 || p.Last != 8 {
		t.Fatalf("time summary lost after cancellation: first=%d last=%d", p.First, p.Last)
	}
}

func TestNegativeTimestamps(t *testing.T) {
	a := New(10, 0)
	if got := a.WindowStart(-1); got != -10 {
		t.Fatalf("WindowStart(-1)=%d, want -10", got)
	}
	if got := a.WindowStart(-10); got != -10 {
		t.Fatalf("WindowStart(-10)=%d, want -10", got)
	}
	if got := a.WindowStart(-11); got != -20 {
		t.Fatalf("WindowStart(-11)=%d, want -20", got)
	}

	a.Add(Event{ID: "neg", X: 0, Y: 0, Polarity: 1, Timestamp: -5})
	a.Add(Event{ID: "close", X: 0, Y: 0, Polarity: 1, Timestamp: 0}) // closes [-10,0)
	results := a.Results()
	if len(results) != 1 || results[0].Start != -10 || results[0].End != 0 {
		t.Fatalf("unexpected windows: %+v", results)
	}
	p := findPixel(t, results[0], 0, 0)
	if p.NetCount != 1 || p.First != -5 || p.Last != -5 {
		t.Fatalf("got net=%d first=%d last=%d", p.NetCount, p.First, p.Last)
	}
}

func TestWindowBoundary(t *testing.T) {
	a := New(10, 5)
	// Timestamp exactly at the boundary belongs to the next window.
	a.Add(Event{ID: "edge", X: 1, Y: 1, Polarity: 1, Timestamp: 10})
	a.Add(Event{ID: "before", X: 1, Y: 1, Polarity: 1, Timestamp: 9})
	a.Add(Event{ID: "close", X: 2, Y: 2, Polarity: 1, Timestamp: 25})

	results := a.Results()
	if len(results) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(results))
	}
	first, second := results[0], results[1]
	if first.Start != 0 || second.Start != 10 {
		t.Fatalf("unexpected window starts: %d, %d", first.Start, second.Start)
	}
	if p := findPixel(t, first, 1, 1); p.NetCount != 1 || p.First != 9 {
		t.Fatalf("boundary event leaked into [0,10): %+v", p)
	}
	if p := findPixel(t, second, 1, 1); p.NetCount != 1 || p.First != 10 {
		t.Fatalf("boundary event missing from [10,20): %+v", p)
	}
}

func TestBoundedOutOfOrder(t *testing.T) {
	a := New(10, 5)
	a.Add(Event{ID: "wm", X: 0, Y: 0, Polarity: 1, Timestamp: 12})
	// Within the lateness bound: window [0,10) still open.
	a.Add(Event{ID: "late-ok", X: 1, Y: 1, Polarity: 1, Timestamp: 4})
	if len(a.Results()) != 0 {
		t.Fatal("window closed before watermark reached end+lateness")
	}
	a.Add(Event{ID: "close", X: 0, Y: 0, Polarity: 1, Timestamp: 15})
	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 window, got %d", len(results))
	}
	p := findPixel(t, results[0], 1, 1)
	if p.NetCount != 1 || p.First != 4 {
		t.Fatalf("out-of-order event not aggregated: %+v", p)
	}
}

func TestLateEventGoesToCorrections(t *testing.T) {
	a := New(10, 5)
	a.Add(Event{ID: "close", X: 0, Y: 0, Polarity: 1, Timestamp: 15}) // closes [0,10)
	a.Add(Event{ID: "too-late", X: 1, Y: 1, Polarity: 1, Timestamp: 3})

	corrections := a.Corrections()
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction, got %d", len(corrections))
	}
	c := corrections[0]
	if c.Event.ID != "too-late" || c.WindowStart != 0 {
		t.Fatalf("unexpected correction: %+v", c)
	}
	for _, res := range a.Results() {
		for _, ps := range res.Pixels {
			if ps.Pixel.X == 1 && ps.Pixel.Y == 1 {
				t.Fatal("late event leaked into closed window output")
			}
		}
	}
}

func TestDuplicateIDsDropped(t *testing.T) {
	a := New(10, 0)
	if !a.Add(Event{ID: "dup", X: 1, Y: 1, Polarity: 1, Timestamp: 2}) {
		t.Fatal("first event should be accepted")
	}
	if a.Add(Event{ID: "dup", X: 1, Y: 1, Polarity: 1, Timestamp: 2}) {
		t.Fatal("duplicate ID should be dropped")
	}
	// Same ID with different payload is still a duplicate.
	if a.Add(Event{ID: "dup", X: 5, Y: 5, Polarity: -1, Timestamp: 3}) {
		t.Fatal("duplicate ID with different payload should be dropped")
	}
	a.Add(Event{ID: "close", X: 0, Y: 0, Polarity: 1, Timestamp: 10})

	p := findPixel(t, a.Results()[0], 1, 1)
	if p.NetCount != 1 {
		t.Fatalf("duplicate was counted: net=%d", p.NetCount)
	}
}

func TestFlushEmitsRemainingWindows(t *testing.T) {
	a := New(10, 0)
	a.Add(Event{ID: "a", X: 1, Y: 1, Polarity: 1, Timestamp: 25})
	a.Flush()
	results := a.Results()
	if len(results) != 1 || results[0].Start != 20 {
		t.Fatalf("flush did not emit open window: %+v", results)
	}
}
