package sim

import (
	"fmt"
	"math/rand"
	"testing"

	"causalslice/causal"
)

// Liveness: r2 holds B@1 without its cause A@1 while a link is partitioned;
// the pending request must stay blocked during the partition and be satisfied
// in bounded simulated time after the link heals.
func TestLivenessBlockedThenSatisfied(t *testing.T) {
	reg := causal.NewRegistry()
	s := New(reg, []string{"r1", "r2"})
	s.Latency = 3
	s.Gossip = 10

	var a1 causal.Ref
	releaseA := false
	s.Schedule(10, func() { a1, _ = s.Write("r1", "A", nil) })
	s.FilterPush = func(now int, from, to string, items []causal.Ref) []causal.Ref {
		if to != "r2" || releaseA {
			return items
		}
		kept := []causal.Ref{}
		for _, r := range items {
			if r == a1 {
				continue
			}
			kept = append(kept, r)
		}
		return kept
	}
	s.Schedule(20, func() { _, _ = s.Write("r1", "B", []causal.Ref{a1}) })
	s.Schedule(45, func() { s.Partition("r1", "r2"); releaseA = false })

	var w *Waiter
	done := make(chan causal.Selection, 1)
	s.Schedule(55, func() {
		w = s.Wait("r2", causal.Request{ID: "L", Latest: []string{"A", "B"}},
			func(sel causal.Selection) { done <- sel })
	})
	// Mid-partition observation: still blocked, nothing partial exposed.
	s.Schedule(120, func() {
		if !w.Pending() {
			t.Fatalf("waiter must remain pending during partition, got %v", w.Last())
		}
		if got := w.Last().Closure; len(got) != 0 {
			t.Fatalf("blocked waiter must expose empty slice, got %v", got)
		}
		if b, ok := blockingFor(w.Last().Blocking, causal.Ref{Object: "B", Seq: 1}); !ok || b.Cause != a1 {
			t.Fatalf("expected B@1 waits-for A@1, got %+v", w.Last().Blocking)
		}
	})
	s.Schedule(140, func() { releaseA = true; s.Heal("r1", "r2") })
	s.RunUntil(260)

	if w.Pending() {
		t.Fatal("waiter must be satisfied after heal (liveness)")
	}
	select {
	case sel := <-done:
		if sel.Closure["A"] != 1 || sel.Closure["B"] != 1 {
			t.Fatalf("served slice must close over A@1, got %v", sel.Closure)
		}
	default:
		t.Fatal("notify callback not invoked")
	}
}

// Different pending requests keep independent state: serving one does not
// corrupt another, and the replica still holds no per-request mutable data.
func TestWaiterIndependence(t *testing.T) {
	reg := causal.NewRegistry()
	s := New(reg, []string{"r1", "r2"})
	s.Latency = 2
	s.Gossip = 8

	s.Schedule(5, func() { _, _ = s.Write("r1", "X", nil) })
	// Only X propagates; A is withheld for the duration, so the A waiter
	// remains pending while the X waiter completes.
	s.FilterPush = func(now int, from, to string, items []causal.Ref) []causal.Ref {
		if to != "r2" {
			return items
		}
		kept := []causal.Ref{}
		for _, r := range items {
			if r.Object == "A" {
				continue
			}
			kept = append(kept, r)
		}
		return kept
	}
	s.Schedule(6, func() { _, _ = s.Write("r1", "A", nil) })

	var wX, wA *Waiter
	// By t=40 r2 has received digests advertising both X@1 and A@1; X@1 has
	// been pushed, A@1 is deliberately withheld.
	s.Schedule(40, func() {
		wX = s.Wait("r2", causal.Request{ID: "X", Latest: []string{"X"}}, nil)
		wA = s.Wait("r2", causal.Request{ID: "A", Latest: []string{"A"}}, nil)
	})
	s.RunUntil(80)

	if wX.Pending() {
		t.Fatal("X waiter should have completed independently")
	}
	if !wA.Pending() {
		t.Fatal("A waiter must still be pending; requests must not share state")
	}
	if len(wA.Last().Closure) != 0 {
		t.Fatalf("pending A waiter leaked a slice: %v", wA.Last().Closure)
	}
	if wX.Last().Closure["X"] != 1 {
		t.Fatalf("X waiter slice wrong: %v", wX.Last().Closure)
	}

	// Releasing A satisfies only the blocked waiter.
	s.Schedule(90, func() { s.FilterPush = nil })
	s.RunUntil(180)
	if wA.Pending() {
		t.Fatal("A waiter must complete once its missing version flows")
	}
	if wA.Last().Closure["A"] != 1 {
		t.Fatalf("A waiter slice wrong: %v", wA.Last().Closure)
	}
}

// TestRandomSafety runs many randomized histories with message loss, delays
// and partitions and asserts every observed slice is causally closed and
// every blocking frontier really is infeasible.
func TestRandomSafety(t *testing.T) {
	const runs, horizon = 40, 400
	objs := []string{"A", "B", "C"}
	reps := []string{"r0", "r1", "r2"}
	for run := 0; run < runs; run++ {
		rng := rand.New(rand.NewSource(int64(run) + 1))
		reg := causal.NewRegistry()
		s := New(reg, reps)
		s.Latency = 2 + rng.Intn(4)
		s.Gossip = 8 + rng.Intn(10)
		s.Recheck = 9

		// Sparse independent packet loss.
		s.AddRule(func(now int, m Message) bool {
			return m.Kind != MsgPush && rng.Intn(12) == 0
		})
		s.AddRule(func(now int, m Message) bool {
			return m.Kind == MsgPush && rng.Intn(20) == 0
		})

		var stamped []causal.Ref
		writeAt := func(at int) {
			s.Schedule(at, func() {
				rep := reps[rng.Intn(len(reps))]
				if !s.reps[rep].up {
					return
				}
				obj := objs[rng.Intn(len(objs))]
				// Pick 0-2 existing *other-object* versions as causal deps.
				var deps []causal.Ref
				for _, r := range stamped {
					if r.Object != obj && rng.Intn(2) == 0 {
						deps = append(deps, r)
					}
				}
				ref, err := s.Write(rep, obj, deps)
				if err == nil {
					stamped = append(stamped, ref)
				}
			})
		}
		for at := 5; at < horizon; at += 8 + rng.Intn(10) {
			writeAt(at)
		}
		// Two short partitions between random pairs.
		for i := 0; i < 2; i++ {
			a, b := reps[rng.Intn(3)], reps[(rng.Intn(2)+1)%3]
			if a == b {
				b = reps[(index(reps, a)+1)%3]
			}
			start := 50 + i*120
			s.Schedule(start, func() { s.Partition(a, b) })
			s.Schedule(start+40, func() { s.Heal(a, b) })
		}
		// Periodic one-shot reads at random replica with random pins/latest.
		for at := 30; at < horizon; at += 25 {
			s.Schedule(at, func() {
				rep := reps[rng.Intn(3)]
				req := causal.Request{ID: "rand", Target: causal.VV{}, Latest: nil}
				for _, o := range objs {
					max := reg.MaxSeq(o)
					if max == 0 {
						continue
					}
					switch rng.Intn(3) {
					case 0:
						req.Latest = append(req.Latest, o)
					case 1:
						req.Target[o] = 1 + rng.Intn(max)
					}
				}
				sel := s.Read(rep, req)
				assertSafe(t, reg, s.View(rep), req, sel)
			})
		}

		s.RunUntil(horizon + 300)

		// Eventual convergence after failures stop: heal every link, remove
		// packet loss, then every replica must reach the full version vector.
		for i := range reps {
			for j := i + 1; j < len(reps); j++ {
				s.Heal(reps[i], reps[j])
			}
		}
		s.ClearRules()
		for ri, rep := range reps {
			max := causal.VV{}
			for _, o := range objs {
				if n := reg.MaxSeq(o); n > 0 {
					max[o] = n
				}
			}
			fw := s.Wait(rep, causal.Request{ID: fmt.Sprintf("final-%d-%d", run, ri), Target: max}, nil)
			s.RunUntil(s.Now() + 800)
			if fw.Pending() {
				missing := []string{}
				for _, o := range objs {
					for seq := 1; seq <= reg.MaxSeq(o); seq++ {
						if !s.View(rep).Present[causal.Ref{Object: o, Seq: seq}] {
							missing = append(missing, fmt.Sprintf("%s@%d", o, seq))
						}
					}
				}
				t.Fatalf("run %d: %s failed to converge; missing %v blocking=%v",
					run, rep, missing, fw.Last().Blocking)
			}
			assertSafe(t, reg, s.View(rep), causal.Request{Target: max}, fw.Last())
			for o, n := range max {
				if fw.Last().Closure[o] != n {
					t.Fatalf("run %d: %s never converged on %s: %v", run, rep, o, fw.Last().Closure)
				}
			}
		}
	}
}

// assertSafe encodes the safety proof obligations for one observed selection.
func assertSafe(t *testing.T, reg *causal.Registry, view causal.View, req causal.Request, sel causal.Selection) {
	t.Helper()

	// 1. Every selected version is present.
	for o, n := range sel.Closure {
		for seq := 1; seq <= n; seq++ {
			ref := causal.Ref{Object: o, Seq: seq}
			if !viewPresent(view, ref) {
				t.Fatalf("slice contains absent %s", ref)
			}
		}
	}

	// 2. Dependency closure: no effect without its cause.
	for o, n := range sel.Closure {
		for seq := 1; seq <= n; seq++ {
			ref := causal.Ref{Object: o, Seq: seq}
			v, ok := reg.Lookup(ref)
			if !ok {
				t.Fatalf("unknown selected %s", ref)
			}
			for _, d := range v.Deps {
				if sel.Closure[d.Object] < d.Seq {
					t.Fatalf("slice has %s but misses cause %s", ref, d)
				}
			}
		}
	}

	// 3. Selected respects the request bounds.
	for o, n := range sel.Closure {
		if pin, ok := req.Target[o]; ok && n > pin {
			t.Fatalf("slice %s=%d exceeds pin %d", o, n, pin)
		}
	}

	// 4. Maximality: a strictly larger *consistent* slice exists. A larger
	// slice must still be present, dependency-closed and within every request
	// bound, so a candidate is addable only if raising one frontier yields a
	// vector whose full closure is present and within bounds.
	locallyFeasible := func(ref causal.Ref) bool {
		memo := map[causal.Ref]bool{}
		var go_ func(r causal.Ref) bool
		go_ = func(r causal.Ref) bool {
			if v, ok := memo[r]; ok {
				return v
			}
			ver, known := reg.Lookup(r)
			ok := known && viewPresent(view, r)
			if ok {
				for _, d := range ver.Deps {
					if !go_(d) {
						ok = false
						break
					}
				}
			}
			memo[r] = ok
			return ok
		}
		return go_(ref)
	}
	// closureOf mirrors causal.causalClosure (not exported) for the test.
	closureOf := func(ref causal.Ref) causal.VV {
		out := causal.VV{}
		visited := map[causal.Ref]bool{}
		var walk func(r causal.Ref)
		walk = func(r causal.Ref) {
			if visited[r] {
				return
			}
			visited[r] = true
			if out[r.Object] < r.Seq {
				out[r.Object] = r.Seq
			}
			ver, ok := reg.Lookup(r)
			if !ok {
				return
			}
			for _, d := range ver.Deps {
				walk(d)
			}
		}
		walk(ref)
		return out
	}
	bounds := causal.VV{}
	for _, o := range req.Latest {
		bounds[o] = reg.MaxSeq(o)
	}
	for o, n := range req.Target {
		if cur, ok := bounds[o]; !ok || n < cur {
			bounds[o] = n
		}
	}
	// addable reports whether raising object o's frontier to seq admits a
	// consistent superslice of the returned closure.
	addable := func(o string, seq int) bool {
		if !locallyFeasible(causal.Ref{Object: o, Seq: seq}) {
			return false
		}
		cand := closureOf(causal.Ref{Object: o, Seq: seq})
		// Merge the existing closure (it stays present by induction).
		for co, cn := range sel.Closure {
			if cn > cand[co] {
				cand[co] = cn
			}
		}
		// Every version in the candidate vector must be present.
		for co, cn := range cand {
			for k := 1; k <= cn; k++ {
				if !viewPresent(view, causal.Ref{Object: co, Seq: k}) {
					return false
				}
			}
			if bound, ok := bounds[co]; ok && cn > bound {
				return false
			}
		}
		return true
	}
	for o := range bounds {
		next := sel.Closure[o] + 1
		ref := causal.Ref{Object: o, Seq: next}
		if _, known := reg.Lookup(ref); known && viewKnown(view, ref) && addable(o, next) {
			t.Fatalf("non-greatest slice: addable %s omitted, closure=%v bounds=%v",
				ref, sel.Closure, bounds)
		}
	}

	// 5. Completion flag matches blocking set, and blocking entries sorted.
	if sel.Complete != (len(sel.Blocking) == 0) {
		t.Fatal("Complete flag inconsistent with Blocking")
	}
	for i := 1; i < len(sel.Blocking); i++ {
		b0, b1 := sel.Blocking[i-1], sel.Blocking[i]
		if b0.Want.Object > b1.Want.Object ||
			(b0.Want.Object == b1.Want.Object && b0.Want.Seq > b1.Want.Seq) {
			t.Fatal("blocking entries not sorted")
		}
	}
}

func viewPresent(v causal.View, ref causal.Ref) bool { return v.Present[ref] }
func viewKnown(v causal.View, ref causal.Ref) bool {
	return v.Present[ref] || v.Known[ref]
}

func blockingFor(bs []causal.Block, want causal.Ref) (causal.Block, bool) {
	for _, b := range bs {
		if b.Want == want {
			return b, true
		}
	}
	return causal.Block{}, false
}

func index(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
