package pixelagg

import "testing"

func addOK(t *testing.T, a *Aggregator, ev Event) {
	t.Helper()
	if err := a.Add(ev); err != nil {
		t.Fatalf("Add(%+v) returned error: %v", ev, err)
	}
}

func findPixel(t *testing.T, w Window, x, y int) PixelSummary {
	t.Helper()
	for _, p := range w.Pixels {
		if p.X == x && p.Y == y {
			return p
		}
	}
	t.Fatalf("pixel (%d,%d) not found in window [%d,%d)", x, y, w.Start, w.End)
	return PixelSummary{}
}

func TestNetCountAndTimeSummary(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	addOK(t, a, Event{ID: "e1", X: 3, Y: 5, Polarity: 1, Time: 2})
	addOK(t, a, Event{ID: "e2", X: 3, Y: 5, Polarity: 1, Time: 7})
	addOK(t, a, Event{ID: "e3", X: 3, Y: 5, Polarity: -1, Time: 4})
	addOK(t, a, Event{ID: "e4", X: 0, Y: 1, Polarity: -1, Time: 1})
	a.Close()

	ws := a.Windows()
	if len(ws) != 1 {
		t.Fatalf("expected 1 window, got %d", len(ws))
	}
	w := ws[0]
	if w.Start != 0 || w.End != 10 {
		t.Fatalf("unexpected window bounds [%d,%d)", w.Start, w.End)
	}
	p := findPixel(t, w, 3, 5)
	if p.NetCount != 1 || p.Earliest != 2 || p.Latest != 7 {
		t.Fatalf("got %+v, want NetCount=1 Earliest=2 Latest=7", p)
	}
	q := findPixel(t, w, 0, 1)
	if q.NetCount != -1 || q.Earliest != 1 || q.Latest != 1 {
		t.Fatalf("got %+v, want NetCount=-1 Earliest=1 Latest=1", q)
	}
}

func TestBoundedOutOfOrderWithinLateness(t *testing.T) {
	a, err := New(10, 5)
	if err != nil {
		t.Fatal(err)
	}
	addOK(t, a, Event{ID: "e1", X: 1, Y: 1, Polarity: 1, Time: 14})
	// Watermark is 9; window [0,10) is still open, so t=8 is accepted.
	addOK(t, a, Event{ID: "e2", X: 2, Y: 2, Polarity: 1, Time: 8})
	if got := len(a.Corrections()); got != 0 {
		t.Fatalf("expected no corrections, got %d", got)
	}
	a.Close()
	ws := a.Windows()
	if len(ws) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(ws))
	}
	p := findPixel(t, ws[0], 2, 2)
	if p.NetCount != 1 || p.Earliest != 8 || p.Latest != 8 {
		t.Fatalf("out-of-order event not aggregated: %+v", p)
	}
}

func TestLateEventGoesToCorrections(t *testing.T) {
	a, err := New(10, 2)
	if err != nil {
		t.Fatal(err)
	}
	addOK(t, a, Event{ID: "e1", X: 1, Y: 1, Polarity: 1, Time: 5})
	// Watermark becomes 18, closing window [0,10).
	addOK(t, a, Event{ID: "e2", X: 9, Y: 9, Polarity: 1, Time: 20})
	if got := len(a.Windows()); got != 1 {
		t.Fatalf("expected 1 closed window, got %d", got)
	}
	// t=7 belongs to the already-closed window [0,10).
	addOK(t, a, Event{ID: "e3", X: 4, Y: 4, Polarity: -1, Time: 7})

	corr := a.Corrections()
	if len(corr) != 1 {
		t.Fatalf("expected 1 correction, got %d", len(corr))
	}
	c := corr[0]
	if c.Event.ID != "e3" || c.WindowStart != 0 || c.WindowEnd != 10 {
		t.Fatalf("unexpected correction: %+v", c)
	}
	// The late event must not leak into any window.
	for _, w := range a.Windows() {
		for _, p := range w.Pixels {
			if p.X == 4 && p.Y == 4 {
				t.Fatalf("late event leaked into window [%d,%d)", w.Start, w.End)
			}
		}
	}
}

func TestDuplicateIDsDropped(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{ID: "dup", X: 1, Y: 1, Polarity: 1, Time: 3}
	addOK(t, a, ev)
	addOK(t, a, ev)
	addOK(t, a, Event{ID: "dup", X: 1, Y: 1, Polarity: -1, Time: 4})
	if a.Duplicates() != 2 {
		t.Fatalf("expected 2 duplicates, got %d", a.Duplicates())
	}
	a.Close()
	p := findPixel(t, a.Windows()[0], 1, 1)
	if p.NetCount != 1 || p.Earliest != 3 || p.Latest != 3 {
		t.Fatalf("dedup failed: %+v", p)
	}
}

func TestNegativeTimeRejected(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add(Event{ID: "neg", X: 0, Y: 0, Polarity: 1, Time: -1}); err != ErrNegativeTime {
		t.Fatalf("expected ErrNegativeTime, got %v", err)
	}
	if err := a.Add(Event{ID: "zero", X: 0, Y: 0, Polarity: 1, Time: 0}); err != nil {
		t.Fatalf("time 0 should be accepted, got %v", err)
	}
}

func TestInvalidPolarityRejected(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add(Event{ID: "p0", X: 0, Y: 0, Polarity: 0, Time: 1}); err != ErrInvalidPolarity {
		t.Fatalf("expected ErrInvalidPolarity, got %v", err)
	}
	if err := a.Add(Event{ID: "p2", X: 0, Y: 0, Polarity: 2, Time: 1}); err != ErrInvalidPolarity {
		t.Fatalf("expected ErrInvalidPolarity, got %v", err)
	}
}

func TestWindowBoundary(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// t=9 belongs to [0,10); t=10 belongs to [10,20).
	addOK(t, a, Event{ID: "b1", X: 1, Y: 1, Polarity: 1, Time: 9})
	addOK(t, a, Event{ID: "b2", X: 1, Y: 1, Polarity: 1, Time: 10})
	a.Close()
	ws := a.Windows()
	if len(ws) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(ws))
	}
	if ws[0].Start != 0 || ws[0].End != 10 || ws[1].Start != 10 || ws[1].End != 20 {
		t.Fatalf("unexpected window bounds: %+v", ws)
	}
	if p := findPixel(t, ws[0], 1, 1); p.Latest != 9 {
		t.Fatalf("t=9 should be in [0,10): %+v", p)
	}
	if p := findPixel(t, ws[1], 1, 1); p.Earliest != 10 {
		t.Fatalf("t=10 should be in [10,20): %+v", p)
	}
}

func TestCancelledCountsKeepTimeSummary(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	addOK(t, a, Event{ID: "c1", X: 7, Y: 7, Polarity: 1, Time: 3})
	addOK(t, a, Event{ID: "c2", X: 7, Y: 7, Polarity: -1, Time: 8})
	a.Close()
	ws := a.Windows()
	if len(ws) != 1 {
		t.Fatalf("expected 1 window, got %d", len(ws))
	}
	p := findPixel(t, ws[0], 7, 7)
	if p.NetCount != 0 {
		t.Fatalf("expected NetCount=0 after cancellation, got %d", p.NetCount)
	}
	if p.Earliest != 3 || p.Latest != 8 {
		t.Fatalf("time summary lost after cancellation: %+v", p)
	}
}

func TestWatermarkClosesWindowsInOrder(t *testing.T) {
	a, err := New(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	addOK(t, a, Event{ID: "w1", X: 0, Y: 0, Polarity: 1, Time: 1})
	addOK(t, a, Event{ID: "w2", X: 0, Y: 0, Polarity: 1, Time: 11})
	// Watermark is 11, so [0,10) is closed but [10,20) is still open.
	ws := a.Windows()
	if len(ws) != 1 || ws[0].Start != 0 {
		t.Fatalf("expected only [0,10) closed, got %+v", ws)
	}
	// Watermark becomes 25, closing [10,20).
	addOK(t, a, Event{ID: "w3", X: 0, Y: 0, Polarity: 1, Time: 25})
	ws = a.Windows()
	if len(ws) != 2 || ws[0].Start != 0 || ws[1].Start != 10 {
		t.Fatalf("unexpected closed windows: %+v", ws)
	}
	a.Close()
	if got := len(a.Windows()); got != 3 {
		t.Fatalf("expected 3 windows after Close, got %d", got)
	}
}
