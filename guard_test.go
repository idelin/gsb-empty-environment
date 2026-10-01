package reactiveguard

import (
	"strings"
	"testing"
)

// Acceptance boundary: an Effect reads Signal A and, by its own logic,
// writes Signal A. The guard must intercept the infinite loop at the
// 50th iteration, raising CircularSignalUpdate naming the trigger source.
func TestSelfWriteLoopBlockedAtIteration50(t *testing.T) {
	g := NewGuard(50)
	a := NewSignal(g, "A", 0)

	runs := 0
	err, caught := CatchCircular(func() {
		g.Effect("self-increment", func() {
			v := a.Get().(int) // read dependency A
			runs++
			a.Set(v + 1) // malicious: write own dependency
		})
	})

	if !caught {
		t.Fatal("expected CircularSignalUpdate, got none")
	}
	if err.Iterations != 50 {
		t.Fatalf("expected block at iteration 50, got %d", err.Iterations)
	}
	if !strings.Contains(err.Source, "self-increment") {
		t.Fatalf("source should name the triggering effect, got %q", err.Source)
	}
	report := err.Error()
	for _, want := range []string{"CircularSignalUpdate", "self-increment", `"A"`, "SELF-DEPENDENCY", "iteration 50"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if runs != 50 {
		t.Fatalf("effect should have executed 50 times before the block, ran %d", runs)
	}
	t.Logf("causal chain report:\n%s", report)
}

// Default guard (no explicit threshold) must also block at 50.
func TestDefaultThresholdIs50(t *testing.T) {
	g := NewGuard(0)
	if g.MaxIterations() != DefaultMaxIterations {
		t.Fatalf("default threshold = %d, want %d", g.MaxIterations(), DefaultMaxIterations)
	}
	a := NewSignal(g, "A", 0)
	err, caught := CatchCircular(func() {
		g.Effect("loop", func() { a.Set(a.Get().(int) + 1) })
	})
	if !caught || err.Iterations != 50 {
		t.Fatalf("caught=%v iterations=%d, want block at 50", caught, err.Iterations)
	}
}

// Indirect loop: two effects ping-ponging through signals A and B must
// be intercepted, and the chain must show both participants.
func TestIndirectTwoEffectLoop(t *testing.T) {
	g := NewGuard(50)
	a := NewSignal(g, "A", 0)
	b := NewSignal(g, "B", 0)

	g.Effect("a-to-b", func() { b.Set(a.Get().(int) + 1) })
	err, caught := CatchCircular(func() {
		g.Effect("b-to-a", func() { a.Set(b.Get().(int) + 1) })
	})

	if !caught {
		t.Fatal("expected CircularSignalUpdate, got none")
	}
	if err.Iterations != 50 {
		t.Fatalf("expected block at iteration 50, got %d", err.Iterations)
	}
	report := err.Error()
	if !strings.Contains(report, `"A"`) || !strings.Contains(report, `"B"`) {
		t.Fatalf("chain should mention both signals:\n%s", report)
	}
	if !strings.Contains(report, "a-to-b") || !strings.Contains(report, "b-to-a") {
		t.Fatalf("chain should mention both effects:\n%s", report)
	}
	t.Logf("causal chain report:\n%s", report)
}

// Call-stack cycle: two Computeds reading each other must be blocked
// immediately (no iteration storm) with a call-stack cycle report.
func TestComputedCallStackCycle(t *testing.T) {
	g := NewGuard(50)

	var x, y *Computed
	x = NewComputed(g, "X", func() interface{} { return y.Get().(int) + 1 })
	y = NewComputed(g, "Y", func() interface{} { return x.Get().(int) + 1 })

	err, caught := CatchCircular(func() { x.Get() })
	if !caught {
		t.Fatal("expected CircularSignalUpdate, got none")
	}
	if !strings.Contains(err.Reason, "call-stack cycle") {
		t.Fatalf("reason = %q, want call-stack cycle", err.Reason)
	}
	report := err.Error()
	if !strings.Contains(report, `"X"`) || !strings.Contains(report, `"Y"`) {
		t.Fatalf("chain should render X -> Y -> X:\n%s", report)
	}
	t.Logf("causal chain report:\n%s", report)
}

// Direct self-read: a Computed that reads itself.
func TestComputedDirectSelfRead(t *testing.T) {
	g := NewGuard(50)
	var c *Computed
	c = NewComputed(g, "C", func() interface{} { return c.Get().(int) + 1 })

	err, caught := CatchCircular(func() { c.Get() })
	if !caught {
		t.Fatal("expected CircularSignalUpdate, got none")
	}
	if !strings.Contains(err.Source, `"C"`) {
		t.Fatalf("source should name computed C, got %q", err.Source)
	}
}

// No false positive: an effect that reads A and writes a *different*
// signal B is legitimate and must run exactly once per external write.
func TestNoFalsePositiveForLegitEffect(t *testing.T) {
	g := NewGuard(50)
	a := NewSignal(g, "A", 0)
	b := NewSignal(g, "B", 0)

	runs := 0
	g.Effect("a-to-b", func() {
		b.Set(a.Get().(int) * 2)
		runs++
	})
	a.Set(1)
	a.Set(2)
	a.Set(3)

	if runs != 4 { // initial run + 3 external writes
		t.Fatalf("runs = %d, want 4", runs)
	}
	if b.Get().(int) != 6 {
		t.Fatalf("B = %v, want 6", b.Get())
	}
}

// No false positive: a finite cascade well below the threshold settles.
func TestFiniteCascadeSettles(t *testing.T) {
	g := NewGuard(50)
	n := NewSignal(g, "N", 0)

	// Counts down from 10 to 0, then stops writing: 11 iterations total.
	g.Effect("countdown", func() {
		v := n.Get().(int)
		if v < 10 {
			n.Set(v + 1)
		}
	})
	if n.Get().(int) != 10 {
		t.Fatalf("N = %v, want 10 (cascade should settle)", n.Get())
	}
}

// After a blocked loop the guard must remain usable.
func TestGuardRecoversAfterBlock(t *testing.T) {
	g := NewGuard(50)
	a := NewSignal(g, "A", 0)
	_, caught := CatchCircular(func() {
		g.Effect("loop", func() { a.Set(a.Get().(int) + 1) })
	})
	if !caught {
		t.Fatal("expected block")
	}

	b := NewSignal(g, "B", 0)
	seen := 0
	g.Effect("ok", func() { seen = b.Get().(int) })
	b.Set(42)
	if seen != 42 {
		t.Fatalf("guard unusable after block: seen = %d, want 42", seen)
	}
}

// A custom threshold must be honored exactly.
func TestCustomThreshold(t *testing.T) {
	g := NewGuard(7)
	a := NewSignal(g, "A", 0)
	err, caught := CatchCircular(func() {
		g.Effect("loop", func() { a.Set(a.Get().(int) + 1) })
	})
	if !caught || err.Iterations != 7 {
		t.Fatalf("caught=%v iterations=%d, want block at 7", caught, err.Iterations)
	}
}

// Non-circular panics must propagate through CatchCircular untouched.
func TestCatchCircularReraisesForeignPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("expected foreign panic to propagate, got %v", r)
		}
	}()
	CatchCircular(func() { panic("boom") })
}
