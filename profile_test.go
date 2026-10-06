package prof

import "testing"

func start(t int64, thread, fn string) Event { return Event{EventStart, thread, fn, t} }
func end(t int64, thread, fn string) Event   { return Event{EventEnd, thread, fn, t} }

func mustClean(t *testing.T, r *Report) {
	t.Helper()
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", r.Diagnostics)
	}
	if problems := Verify(r); len(problems) != 0 {
		t.Fatalf("verify failed: %v", problems)
	}
}

func stat(t *testing.T, r *Report, fn string) *FuncStats {
	t.Helper()
	s := r.Funcs[fn]
	if s == nil {
		t.Fatalf("no stats for %q", fn)
	}
	return s
}

// Nested same-thread calls: inclusive = end-start, exclusive subtracts
// only same-thread children.
func TestNestedInclusiveExclusive(t *testing.T) {
	r := Analyze([]Event{
		start(0, "main", "main"),
		start(10, "main", "a"),
		end(40, "main", "a"),
		start(50, "main", "b"),
		end(70, "main", "b"),
		end(100, "main", "main"),
	})
	mustClean(t, r)

	if got := stat(t, r, "main"); got.Inclusive != 100 || got.Exclusive != 50 {
		t.Fatalf("main: got incl=%d excl=%d, want 100/50", got.Inclusive, got.Exclusive)
	}
	if got := stat(t, r, "a"); got.Inclusive != 30 || got.Exclusive != 30 {
		t.Fatalf("a: got incl=%d excl=%d, want 30/30", got.Inclusive, got.Exclusive)
	}
	if got := stat(t, r, "b"); got.Inclusive != 20 || got.Exclusive != 20 {
		t.Fatalf("b: got incl=%d excl=%d, want 20/20", got.Inclusive, got.Exclusive)
	}
	if r.WallBusy != 100 {
		t.Fatalf("WallBusy = %d, want 100", r.WallBusy)
	}
	if r.Overlap() != 0 {
		t.Fatalf("Overlap = %d, want 0 for a single thread", r.Overlap())
	}
}

// Recursion: inclusive sum counts every nested activation, while the
// occupancy union collapses them to the outermost interval.
func TestRecursion(t *testing.T) {
	r := Analyze([]Event{
		start(0, "w", "f"),
		start(2, "w", "f"),
		start(4, "w", "f"),
		end(6, "w", "f"),
		end(8, "w", "f"),
		end(10, "w", "f"),
	})
	mustClean(t, r)

	s := stat(t, r, "f")
	if s.Calls != 3 {
		t.Fatalf("calls = %d, want 3", s.Calls)
	}
	if s.Inclusive != 10+6+2 {
		t.Fatalf("inclusive = %d, want 18", s.Inclusive)
	}
	// self times: innermost 2, middle 6-2=4, outer 10-6=4
	if s.Exclusive != 2+4+4 {
		t.Fatalf("exclusive = %d, want 10", s.Exclusive)
	}
	if s.WallUnion != 10 {
		t.Fatalf("wall union = %d, want 10 (nested recursion collapses)", s.WallUnion)
	}
	if r.WallBusy != 10 {
		t.Fatalf("WallBusy = %d, want 10", r.WallBusy)
	}
}

// Same-timestamp events: zero-duration calls pair deterministically and
// contribute nothing to occupancy.
func TestSameTimestampEvents(t *testing.T) {
	r := Analyze([]Event{
		start(0, "w", "main"),
		start(5, "w", "z"), // zero-duration call
		end(5, "w", "z"),
		end(5, "w", "main"), // main ends at the same instant
		start(5, "w", "next"),
		end(9, "w", "next"),
	})
	mustClean(t, r)

	z := stat(t, r, "z")
	if z.Calls != 1 || z.Inclusive != 0 || z.Exclusive != 0 {
		t.Fatalf("z: calls=%d incl=%d excl=%d, want 1/0/0", z.Calls, z.Inclusive, z.Exclusive)
	}
	if got := stat(t, r, "main"); got.Inclusive != 5 || got.Exclusive != 5 {
		t.Fatalf("main: incl=%d excl=%d, want 5/5", got.Inclusive, got.Exclusive)
	}
	if got := stat(t, r, "next"); got.Inclusive != 4 {
		t.Fatalf("next: incl=%d, want 4", got.Inclusive)
	}
	if r.WallBusy != 9 {
		t.Fatalf("WallBusy = %d, want 9", r.WallBusy)
	}
}

// Bad stacks must produce precise diagnostics and still recover.
func TestBadStackDiagnostics(t *testing.T) {
	r := Analyze([]Event{
		end(1, "w", "ghost"),   // orphan end, empty stack
		start(2, "w", "outer"), // \
		start(3, "w", "inner"), //  } inner never closed
		end(4, "w", "outer"),   // / end of outer interrupts inner
		start(5, "w", "leak"),  // never closed, force-closed at thread end
		end(6, "w", "nope"),    // matches no open call (leak is open)
	})

	var orphan, mismatch, unclosed int
	for _, d := range r.Diagnostics {
		switch d.Kind {
		case DiagOrphanEnd:
			orphan++
			if d.Func != "ghost" {
				t.Fatalf("orphan func = %q, want ghost", d.Func)
			}
		case DiagMismatchEnd:
			mismatch++
			if d.Func != "nope" {
				t.Fatalf("mismatch func = %q, want nope", d.Func)
			}
		case DiagUnclosedStart:
			unclosed++
		}
	}
	if orphan != 1 || mismatch != 1 || unclosed != 2 {
		t.Fatalf("diags orphan=%d mismatch=%d unclosed=%d, want 1/1/2",
			orphan, mismatch, unclosed)
	}

	// Recovery: outer closed at 4 with inner force-closed at 4 (self 1+0),
	// leak closed at thread end (t=6).
	if got := stat(t, r, "outer"); got.Inclusive != 2 || got.Exclusive != 1 {
		t.Fatalf("outer: incl=%d excl=%d, want 2/1", got.Inclusive, got.Exclusive)
	}
	if got := stat(t, r, "inner"); got.Inclusive != 1 {
		t.Fatalf("inner: incl=%d, want 1 (force-closed at 4)", got.Inclusive)
	}
	if got := stat(t, r, "leak"); got.Inclusive != 1 {
		t.Fatalf("leak: incl=%d, want 1 (closed at last t=6)", got.Inclusive)
	}
	if problems := Verify(r); len(problems) != 0 {
		t.Fatalf("verify failed after recovery: %v", problems)
	}
}

// Async child tasks on separate threads overlap; their durations must not
// be summed to get elapsed time. The occupancy union gives the true value.
func TestAsyncOverlapNotAdditive(t *testing.T) {
	r := Analyze([]Event{
		start(0, "main", "main"),
		end(100, "main", "main"),
		start(20, "worker-1", "job"),
		end(50, "worker-1", "job"),
		start(30, "worker-2", "job"),
		end(80, "worker-2", "job"),
	})
	mustClean(t, r)

	job := stat(t, r, "job")
	if job.Inclusive != 30+50 {
		t.Fatalf("job inclusive = %d, want 80 (sum over activations)", job.Inclusive)
	}
	if job.WallUnion != 60 {
		t.Fatalf("job wall union = %d, want 60 ([20,50) U [30,80))", job.WallUnion)
	}
	if r.TotalInclusive() != 100+30+50 {
		t.Fatalf("total inclusive = %d, want 180", r.TotalInclusive())
	}
	if r.WallBusy != 100 {
		t.Fatalf("WallBusy = %d, want 100 (union, not sum)", r.WallBusy)
	}
	if r.Overlap() != 80 {
		t.Fatalf("Overlap = %d, want 80", r.Overlap())
	}
}

// Parent spawns an async child on another thread: the child is a separate
// root and does NOT reduce the parent's exclusive time.
func TestAsyncChildDoesNotConsumeParent(t *testing.T) {
	r := Analyze([]Event{
		start(0, "main", "parent"),
		end(50, "main", "parent"),
		start(10, "async", "child"),
		end(40, "async", "child"),
	})
	mustClean(t, r)

	if len(r.Roots) != 2 {
		t.Fatalf("roots = %d, want 2 (async child is its own root)", len(r.Roots))
	}
	p := stat(t, r, "parent")
	if p.Exclusive != 50 {
		t.Fatalf("parent exclusive = %d, want 50 (async child not subtracted)", p.Exclusive)
	}
	if r.WallBusy != 50 {
		t.Fatalf("WallBusy = %d, want 50", r.WallBusy)
	}
	if r.Overlap() != 30 {
		t.Fatalf("Overlap = %d, want 30", r.Overlap())
	}
}
