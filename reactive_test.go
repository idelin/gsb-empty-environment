package reactive

import "testing"

// Acceptance boundary: an Effect watches X and Y; inside one batch X=10 then
// Y=20 are assigned. The Effect must fire zero times during the batch and
// exactly once at batch exit, observing X=10 and Y=20 together.
func TestBatchFiresEffectOnceWithLatestValues(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)
	y := NewSignal(s, 0)

	runs := 0
	var gotX, gotY int
	NewEffect(s, func() {
		runs++
		gotX = x.Get()
		gotY = y.Get()
	})
	if runs != 1 {
		t.Fatalf("effect should run once at creation, got %d", runs)
	}
	runs = 0

	s.Batch(func() {
		x.Set(10)
		if runs != 0 {
			t.Fatalf("effect fired inside batch after x.Set, runs=%d", runs)
		}
		y.Set(20)
		if runs != 0 {
			t.Fatalf("effect fired inside batch after y.Set, runs=%d", runs)
		}
	})

	if runs != 1 {
		t.Fatalf("effect should fire exactly once after batch, got %d", runs)
	}
	if gotX != 10 || gotY != 20 {
		t.Fatalf("effect observed (%d, %d), want (10, 20)", gotX, gotY)
	}
}

// Without a batch, every Set triggers the effect immediately.
func TestImmediatePropagationOutsideBatch(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)

	runs := 0
	NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	x.Set(1)
	x.Set(2)
	if runs != 2 {
		t.Fatalf("expected 2 immediate runs, got %d", runs)
	}
}

// Computeds stay silent during a batch and recompute lazily on read,
// observing the final values.
func TestComputedSilentDuringBatchAndLazyAfter(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 1)
	y := NewSignal(s, 2)

	evals := 0
	sum := NewComputed(s, func() int {
		evals++
		return x.Get() + y.Get()
	})
	if got := sum.Get(); got != 3 {
		t.Fatalf("initial sum = %d, want 3", got)
	}
	if evals != 1 {
		t.Fatalf("computed should evaluate once, got %d", evals)
	}

	s.Batch(func() {
		x.Set(10)
		y.Set(20)
		if evals != 1 {
			t.Fatalf("computed re-evaluated inside batch, evals=%d", evals)
		}
	})
	if evals != 1 {
		t.Fatalf("computed should stay lazy after batch exit, evals=%d", evals)
	}
	if got := sum.Get(); got != 30 {
		t.Fatalf("sum after batch = %d, want 30", got)
	}
	if evals != 2 {
		t.Fatalf("computed should re-evaluate once on read, got %d", evals)
	}
}

// An Effect reading a Computed fires once per batch and sees the derived
// value computed from the latest signals.
func TestEffectOverComputedInBatch(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 1)
	y := NewSignal(s, 2)
	sum := NewComputed(s, func() int { return x.Get() + y.Get() })

	runs := 0
	got := 0
	NewEffect(s, func() {
		runs++
		got = sum.Get()
	})
	runs = 0

	s.Batch(func() {
		x.Set(10)
		y.Set(20)
	})
	if runs != 1 || got != 30 {
		t.Fatalf("runs=%d got=%d, want runs=1 got=30", runs, got)
	}
}

// Multiple Sets of the same signal inside one batch still produce a single
// effect run.
func TestRepeatedSetsInBatchDeduplicate(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)

	runs := 0
	NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	s.Batch(func() {
		x.Set(1)
		x.Set(2)
		x.Set(3)
	})
	if runs != 1 {
		t.Fatalf("expected 1 run after repeated sets, got %d", runs)
	}
	if x.Get() != 3 {
		t.Fatalf("x = %d, want 3", x.Get())
	}
}

// Nested batches flush only when the outermost batch exits.
func TestNestedBatchFlushesAtOutermostExit(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)

	runs := 0
	NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	s.Batch(func() {
		x.Set(1)
		s.Batch(func() {
			x.Set(2)
		})
		if runs != 0 {
			t.Fatalf("effect fired after inner batch, runs=%d", runs)
		}
		x.Set(3)
	})
	if runs != 1 {
		t.Fatalf("expected 1 run after outer batch, got %d", runs)
	}
}

// Setting a signal to its current value is a no-op and triggers nothing.
func TestNoChangeNoNotify(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 5)

	runs := 0
	NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	x.Set(5)
	s.Batch(func() { x.Set(5) })
	if runs != 0 {
		t.Fatalf("unchanged set should not notify, runs=%d", runs)
	}
}

// A stopped effect never runs again.
func TestStopEffect(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)

	runs := 0
	e := NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	e.Stop()
	x.Set(1)
	s.Batch(func() { x.Set(2) })
	if runs != 0 {
		t.Fatalf("stopped effect ran %d times", runs)
	}
}

// A panicking batch discards queued notifications and propagates the panic.
func TestBatchPanicSkipsFlush(t *testing.T) {
	s := NewScope()
	x := NewSignal(s, 0)

	runs := 0
	NewEffect(s, func() {
		runs++
		_ = x.Get()
	})
	runs = 0

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		s.Batch(func() {
			x.Set(1)
			panic("boom")
		})
	}()
	if runs != 0 {
		t.Fatalf("effect should not run when batch panics, runs=%d", runs)
	}
}
