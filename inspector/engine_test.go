package inspector

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func findingsOfKind(rep Report, kind string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func TestCleanGraphPasses(t *testing.T) {
	e := NewEngine()
	e.UpsertNode(Node{ID: "root", Refs: []string{"a", "b"}, Weight: 10})
	e.UpsertNode(Node{ID: "a", Refs: []string{"b"}, Weight: 10})
	e.UpsertNode(Node{ID: "b", Weight: 10})
	e.AddRoot("root")
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("expected clean report, got %+v", rep.Findings)
	}
}

func TestDanglingReference(t *testing.T) {
	e := NewEngine()
	e.UpsertNode(Node{ID: "root", Refs: []string{"ghost"}, Weight: 1})
	e.AddRoot("root")
	rep := e.Inspect()
	d := findingsOfKind(rep, KindDanglingRef)
	if len(d) != 1 {
		t.Fatalf("expected 1 dangling-ref finding, got %+v", rep.Findings)
	}
	if d[0].Nodes[0] != "root" || d[0].Nodes[1] != "ghost" {
		t.Fatalf("unexpected finding nodes: %+v", d[0])
	}
	// Adding the missing node clears the finding.
	e.UpsertNode(Node{ID: "ghost", Weight: 1})
	if rep := e.Inspect(); len(findingsOfKind(rep, KindDanglingRef)) != 0 {
		t.Fatalf("dangling ref not cleared: %+v", rep.Findings)
	}
	// Removing a node re-introduces dangling refs from its referrers.
	e.RemoveNode("ghost")
	if rep := e.Inspect(); len(findingsOfKind(rep, KindDanglingRef)) != 1 {
		t.Fatalf("expected dangling ref after removal: %+v", rep.Findings)
	}
}

func TestOrphanCycleDetection(t *testing.T) {
	e := NewEngine()
	// Reachable cycle: must NOT be flagged.
	e.UpsertNode(Node{ID: "root", Refs: []string{"x"}, Weight: 1})
	e.UpsertNode(Node{ID: "x", Refs: []string{"y"}, Weight: 1})
	e.UpsertNode(Node{ID: "y", Refs: []string{"x"}, Weight: 1})
	// Orphan cycle: must be flagged.
	e.UpsertNode(Node{ID: "p", Refs: []string{"q"}, Weight: 1})
	e.UpsertNode(Node{ID: "q", Refs: []string{"r"}, Weight: 1})
	e.UpsertNode(Node{ID: "r", Refs: []string{"p"}, Weight: 1})
	// Orphan self-loop: must be flagged.
	e.UpsertNode(Node{ID: "self", Refs: []string{"self"}, Weight: 1})
	e.AddRoot("root")

	rep := e.Inspect()
	orphans := findingsOfKind(rep, KindOrphanCycle)
	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphan cycles, got %+v", orphans)
	}
	var sawTriple, sawSelf bool
	for _, f := range orphans {
		joined := strings.Join(f.Nodes, ",")
		if joined == "p,q,r" {
			sawTriple = true
		}
		if joined == "self" {
			sawSelf = true
		}
		if joined == "x,y" {
			t.Fatalf("reachable cycle wrongly flagged: %+v", f)
		}
	}
	if !sawTriple || !sawSelf {
		t.Fatalf("missing orphan cycles: %+v", orphans)
	}
}

func TestOrphanCycleBecomesReachable(t *testing.T) {
	e := NewEngine()
	e.UpsertNode(Node{ID: "root", Weight: 1})
	e.UpsertNode(Node{ID: "a", Refs: []string{"b"}, Weight: 1})
	e.UpsertNode(Node{ID: "b", Refs: []string{"a"}, Weight: 1})
	e.AddRoot("root")
	if rep := e.Inspect(); len(findingsOfKind(rep, KindOrphanCycle)) != 1 {
		t.Fatalf("expected orphan cycle, got %+v", rep.Findings)
	}
	// Link root -> a: cycle becomes reachable, finding disappears.
	e.UpsertNode(Node{ID: "root", Refs: []string{"a"}, Weight: 1})
	if rep := e.Inspect(); len(findingsOfKind(rep, KindOrphanCycle)) != 0 {
		t.Fatalf("reachable cycle still flagged: %+v", rep.Findings)
	}
}

func TestDataSkew(t *testing.T) {
	e := NewEngine()
	e.SetSkewFactor(2.0)
	e.UpsertNode(Node{ID: "hot", Weight: 1000})
	e.UpsertNode(Node{ID: "c1", Weight: 10})
	e.UpsertNode(Node{ID: "c2", Weight: 10})
	e.UpsertNode(Node{ID: "c3", Weight: 10})
	rep := e.Inspect()
	skew := findingsOfKind(rep, KindDataSkew)
	if len(skew) != 1 || skew[0].Nodes[0] != "hot" {
		t.Fatalf("expected skew on hot node, got %+v", rep.Findings)
	}
	// Disabling the check (factor <= 0) clears it.
	e.SetSkewFactor(0)
	if rep := e.Inspect(); len(findingsOfKind(rep, KindDataSkew)) != 0 {
		t.Fatalf("skew check not disabled: %+v", rep.Findings)
	}
}

func TestSkewEdgeCases(t *testing.T) {
	e := NewEngine()
	// All-zero weights: no skew, no division by zero.
	e.UpsertNode(Node{ID: "z1"})
	e.UpsertNode(Node{ID: "z2"})
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("zero-weight graph should be clean: %+v", rep.Findings)
	}
	// Uniform weights: nobody exceeds factor*mean.
	e.UpsertNode(Node{ID: "z1", Weight: 5})
	e.UpsertNode(Node{ID: "z2", Weight: 5})
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("uniform graph should be clean: %+v", rep.Findings)
	}
}

func TestInvariantAssertion(t *testing.T) {
	e := NewEngine()
	e.RegisterInvariant("non-negative-weights", func(s Snapshot) error {
		for id, n := range s.Nodes {
			if n.Weight < 0 {
				return fmt.Errorf("node %s has negative weight %d", id, n.Weight)
			}
		}
		return nil
	})
	e.RegisterInvariant("always-ok", func(s Snapshot) error { return nil })
	e.UpsertNode(Node{ID: "good", Weight: 1})
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("unexpected findings: %+v", rep.Findings)
	}
	e.UpsertNode(Node{ID: "bad", Weight: -5})
	rep := e.Inspect()
	inv := findingsOfKind(rep, KindInvariant)
	if len(inv) != 1 || !strings.Contains(inv[0].Detail, "non-negative-weights") {
		t.Fatalf("expected invariant violation, got %+v", rep.Findings)
	}
	// Nil check functions are skipped safely.
	e.RegisterInvariant("nil-check", nil)
	e.RemoveNode("bad")
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("unexpected findings after cleanup: %+v", rep.Findings)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	e := NewEngine()
	e.UpsertNode(Node{ID: "n", Refs: []string{"m"}, Weight: 1})
	snap := e.Snapshot()
	// Mutating the snapshot must not affect the engine.
	n := snap.Nodes["n"]
	n.Refs[0] = "corrupted"
	n.Weight = 999
	snap.Nodes["n"] = n
	delete(snap.Nodes, "n")
	rep := e.Inspect()
	if len(findingsOfKind(rep, KindDanglingRef)) != 1 {
		t.Fatalf("engine state corrupted via snapshot: %+v", rep.Findings)
	}
}

func TestConcurrentAccess(t *testing.T) {
	e := NewEngine()
	e.RegisterInvariant("weights-bounded", func(s Snapshot) error {
		for id, n := range s.Nodes {
			if n.Weight > 1<<40 {
				return errors.New("weight overflow on " + id)
			}
		}
		return nil
	})
	const workers = 8
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("node-%d", w)
			for i := 0; i < iters; i++ {
				e.UpsertNode(Node{ID: id, Refs: []string{fmt.Sprintf("node-%d", (w+1)%workers)}, Weight: int64(i)})
				e.AddRoot("node-0")
				_ = e.Inspect()
				_ = e.Snapshot()
				if i%3 == 0 {
					e.RemoveNode(fmt.Sprintf("node-%d", (w+3)%workers))
				}
			}
		}(w)
	}
	wg.Wait()
	// Engine must still be functional and consistent after the storm.
	for w := 0; w < workers; w++ {
		e.UpsertNode(Node{ID: fmt.Sprintf("node-%d", w), Weight: 1})
	}
	e.SetSkewFactor(0)
	if rep := e.Inspect(); !rep.OK() {
		t.Fatalf("post-concurrency state not clean: %+v", rep.Findings)
	}
}

// TestNoGoroutineLeak asserts the engine spawns no background goroutines:
// after a full workout and a settle window, the goroutine count returns to
// the baseline.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	func() {
		e := NewEngine()
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					e.UpsertNode(Node{ID: fmt.Sprintf("n%d", w), Weight: int64(i)})
					_ = e.Inspect()
				}
			}(w)
		}
		wg.Wait()
	}()
	// Allow the scheduler to reap finished goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
}
