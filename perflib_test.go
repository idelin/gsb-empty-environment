package perflib

import (
	"strings"
	"testing"
)

func TestNestedInclusiveExclusive(t *testing.T) {
	tr := Build([]Event{
		Start("main", 1, 0),
		Start("read", 1, 10),
		End("read", 1, 40),
		Start("write", 1, 50),
		End("write", 1, 90),
		End("main", 1, 100),
	})
	if len(tr.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", tr.Diagnostics)
	}
	if len(tr.Roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(tr.Roots))
	}
	main := tr.Roots[0]
	if got := main.Inclusive(); got != 100 {
		t.Errorf("main inclusive = %d, want 100", got)
	}
	if got := main.Exclusive(); got != 30 {
		t.Errorf("main exclusive = %d, want 30", got)
	}
	stats := tr.Stats()
	if stats["read"].Inclusive != 30 || stats["read"].Exclusive != 30 {
		t.Errorf("read stat = %+v", stats["read"])
	}
	if stats["write"].Exclusive != 40 {
		t.Errorf("write exclusive = %d, want 40", stats["write"].Exclusive)
	}
}

func TestRecursion(t *testing.T) {
	// f -> f -> f, strictly nested on one thread.
	tr := Build([]Event{
		Start("f", 1, 0),
		Start("f", 1, 2),
		Start("f", 1, 4),
		End("f", 1, 6),
		End("f", 1, 8),
		End("f", 1, 10),
	})
	if len(tr.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", tr.Diagnostics)
	}
	s := tr.Stats()["f"]
	if s.Calls != 3 {
		t.Fatalf("f calls = %d, want 3", s.Calls)
	}
	if s.Inclusive != 10+6+2 {
		t.Errorf("f inclusive = %d, want 18", s.Inclusive)
	}
	// Per-frame exclusive: 4 + 4 + 2 = 10; for a single thread the sum
	// of exclusive spans equals the union of the name's intervals.
	if s.Exclusive != 10 {
		t.Errorf("f exclusive = %d, want 10", s.Exclusive)
	}
	if got := tr.FuncWallTime()["f"]; got != s.Exclusive {
		t.Errorf("occupancy union for f = %d, want %d (exclusive sum)", got, s.Exclusive)
	}
}

func TestSameTimestampEvents(t *testing.T) {
	// Zero-duration calls and boundaries sharing one timestamp keep
	// input order (stable sort) and still pair as a strict stack.
	tr := Build([]Event{
		Start("a", 1, 0),
		Start("b", 1, 5),
		End("b", 1, 5),
		Start("c", 1, 5),
		End("c", 1, 5),
		End("a", 1, 5),
	})
	if len(tr.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", tr.Diagnostics)
	}
	a := tr.Roots[0]
	if len(a.Children) != 2 || a.Children[0].Name != "b" || a.Children[1].Name != "c" {
		t.Fatalf("a children = %v, want [b c]", a.Children)
	}
	if a.Inclusive() != 5 || a.Exclusive() != 5 {
		t.Errorf("a inclusive=%d exclusive=%d, want 5/5", a.Inclusive(), a.Exclusive())
	}
	for _, c := range a.Children {
		if c.Inclusive() != 0 {
			t.Errorf("%s inclusive = %d, want 0", c.Name, c.Inclusive())
		}
	}
}

func TestAsyncOverlapNotSummed(t *testing.T) {
	// task runs on two threads with a 50ns overlap: inclusive sums to
	// 200 but the occupied wall time is the union, 150.
	tr := Build([]Event{
		Start("task", 1, 0),
		End("task", 1, 100),
		Start("task", 2, 50),
		End("task", 2, 150),
	})
	if len(tr.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", tr.Diagnostics)
	}
	s := tr.Stats()["task"]
	if s.Inclusive != 200 {
		t.Errorf("task inclusive sum = %d, want 200", s.Inclusive)
	}
	if got := tr.FuncWallTime()["task"]; got != 150 {
		t.Errorf("task wall union = %d, want 150", got)
	}
	if got := tr.WallTime(); got != 150 {
		t.Errorf("trace wall time = %d, want 150", got)
	}
}

func TestBadStackDiagnostics(t *testing.T) {
	tr := Build([]Event{
		End("ghost", 1, 0), // end with empty stack
		Start("a", 1, 1),
		Start("b", 1, 2),  // never closed
		End("a", 1, 3),    // skips b -> b unclosed at t=3
		Start("c", 1, 4),  // left open at stream end
		End("nope", 1, 5), // matches no open frame
		Start("neg", 1, 6),
		End("neg", 1, 5), // clock regresses; negative duration, clamped
	})

	kinds := map[DiagKind]int{}
	for _, d := range tr.Diagnostics {
		kinds[d.Kind]++
	}
	if kinds[DiagEndWithoutStart] != 1 {
		t.Errorf("end-without-start count = %d, want 1", kinds[DiagEndWithoutStart])
	}
	if kinds[DiagNameMismatch] != 1 {
		t.Errorf("name-mismatch count = %d, want 1", kinds[DiagNameMismatch])
	}
	// b skipped by end of a, plus c open at stream end.
	if kinds[DiagUnclosedFrame] != 2 {
		t.Errorf("unclosed-frame count = %d, want 3 (%v)", kinds[DiagUnclosedFrame], tr.Diagnostics)
	}
	if kinds[DiagNegativeDuration] != 1 {
		t.Errorf("negative-duration count = %d, want 1", kinds[DiagNegativeDuration])
	}
	if kinds[DiagOutOfOrder] != 1 {
		t.Errorf("out-of-order count = %d, want 1", kinds[DiagOutOfOrder])
	}

	// Recovery: a closed at t=3 with b force-closed inside it.
	var a, b *CallNode
	Walk(tr.Roots, func(n *CallNode) {
		switch n.Name {
		case "a":
			a = n
		case "b":
			b = n
		}
	})
	if a == nil || a.End != 3 {
		t.Errorf("a = %+v, want end=3", a)
	}
	if b == nil || b.End != 3 {
		t.Errorf("b = %+v, want force-closed at 3", b)
	}
	// neg clamped to zero duration.
	for _, r := range tr.Roots {
		if r.Name == "neg" && r.Inclusive() != 0 {
			t.Errorf("neg inclusive = %d, want clamped 0", r.Inclusive())
		}
	}
}

func TestOccupancyCrossCheck(t *testing.T) {
	// Mixed nested trace across two threads; verify the occupancy model
	// against per-frame accounting.
	tr := Build([]Event{
		Start("main", 1, 0),
		Start("io", 1, 10),
		End("io", 1, 30),
		Start("work", 2, 20),
		Start("sub", 2, 25),
		End("sub", 2, 45),
		End("work", 2, 60),
		End("main", 1, 80),
	})
	if len(tr.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", tr.Diagnostics)
	}

	// Per thread: sum of exclusive spans == union of that thread's
	// intervals (each occupied slice has exactly one innermost frame).
	byThread := map[uint64][]Interval{}
	Walk(tr.Roots, func(n *CallNode) {
		byThread[n.Thread] = append(byThread[n.Thread], n.Span())
	})
	for thread, ivs := range byThread {
		var exclSum int64
		Walk(tr.Roots, func(n *CallNode) {
			if n.Thread == thread {
				exclSum += n.Exclusive()
			}
		})
		if u := UnionDuration(ivs); u != exclSum {
			t.Errorf("thread %d: union %d != exclusive sum %d", thread, u, exclSum)
		}
	}

	// Wall time via occupancy segments: [0,80) on thread 1 alone is 80,
	// thread 2 adds nothing outside it.
	segs := Occupancy(tr.intervals(nil))
	var wall int64
	for _, s := range segs {
		wall += s.End - s.Start
	}
	if wall != 80 || wall != tr.WallTime() {
		t.Errorf("wall = %d (trace %d), want 80", wall, tr.WallTime())
	}
	// Peak occupancy is 4 during [25,30): main+io on thread 1 and
	// work+sub on thread 2 are all active.
	peak := 0
	for _, s := range segs {
		if s.Depth > peak {
			peak = s.Depth
		}
	}
	if peak != 4 {
		t.Errorf("peak occupancy = %d, want 4", peak)
	}
}

func TestDiagnosticString(t *testing.T) {
	d := Diagnostic{Kind: DiagNameMismatch, Thread: 7, Time: 3, Name: "x", Detail: "top is y"}
	if s := d.String(); !strings.Contains(s, "name-mismatch") || !strings.Contains(s, "thread=7") {
		t.Errorf("unexpected String(): %q", s)
	}
}
