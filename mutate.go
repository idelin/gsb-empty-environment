package netsec

import (
	"fmt"
	"math/rand/v2"
)

// Boundary kinds returned by [Problem.BoundarySamples]. The numeric values
// are stable and used as part of the reproducible seed.
const (
	seedRandom     uint64 = 1
	seedSimult     uint64 = 2
	seedBeforeDead uint64 = 3
	seedAtDead     uint64 = 4
)

// Mutation is one offline time assignment produced for an experiment.
type Mutation struct {
	// Times assigns one integer timestamp to every event, in declaration
	// order.
	Times []int64
	// Seed deterministically identifies the experiment: re-running the same
	// problem with this seed reproduces Times exactly.
	Seed uint64
	// Tag is a human-readable label ("random", "simultaneous", ...).
	Tag string
}

// Random returns a legal assignment generated deterministically from seed.
// The same problem and seed always produce the same assignment, so test
// runs are reproducible without recording full timestamps.
//
// It first samples every event uniformly inside its tight feasible window
// and rejects assignments that break an ordering. For tightly coupled
// systems where rejection rarely succeeds, it falls back to a seed-shuffled
// topological construction that always yields a legal assignment. Both
// paths are pure functions of the seed.
func (p *Problem) Random(seed uint64) (*Mutation, error) {
	sol, cyc := p.solve(nil)
	if cyc != nil {
		return nil, cyc
	}
	rng := rand.New(rand.NewPCG(seed, seedRandom))
	n := len(p.names)

	// Primary strategy: sample each event uniformly inside its tight feasible
	// window, then reject unless every ordering holds. Tight windows keep the
	// acceptance rate high even on small domains; bounded attempts keep
	// runtime predictable.
	spans := make([]uint64, n)
	for i := 0; i < n; i++ {
		spans[i] = uint64(sol.latest[i]-sol.earliest[i]) + 1
	}
	for attempt := 0; attempt < 256; attempt++ {
		times := make([]int64, n)
		for i := 0; i < n; i++ {
			times[i] = sol.earliest[i] + int64(rng.Uint64()%spans[i])
		}
		if p.Legal(times) {
			return &Mutation{Times: times, Seed: seed, Tag: "random"}, nil
		}
	}

	// Fallback for tightly coupled systems: topological construction always
	// yields a legal assignment.
	return &Mutation{Times: topologicalSample(p, sol, seed), Seed: seed, Tag: "random"}, nil
}

// topologicalSample builds a legal assignment by visiting events in a
// seed-shuffled topological order and choosing each value inside the range
// left open by already-fixed predecessors. It never rejects because every
// chosen value is constrained to be feasible by construction.
func topologicalSample(p *Problem, sol *Solution, seed uint64) []int64 {
	rng := rand.New(rand.NewPCG(seed, seedRandom))
	n := len(p.names)
	order := p.topoOrder(rng)
	times := make([]int64, n)
	preds := p.predecessors()
	for _, i := range order {
		lower := sol.earliest[i]
		for _, pr := range preds[i] {
			if cand := times[pr] + p.edgeWeight(pr, i); cand > lower {
				lower = cand
			}
		}
		upper := sol.latest[i]
		if lower >= upper {
			times[i] = upper
			continue
		}
		span := uint64(upper-lower) + 1
		times[i] = lower + int64(rng.Uint64()%span)
	}
	return times
}

// Generate returns count distinct legal assignments for seeds base,
// base+1, ..., base+count-1. Duplicate assignments (possible on very small
// time domains) are skipped while consuming the next seed, and generation
// stops once count distinct mutations have been collected or every seed in
// a window of count*8+16 attempts has been exhausted.
func (p *Problem) Generate(base uint64, count int) ([]Mutation, error) {
	if count <= 0 {
		return nil, fmt.Errorf("netsec: count must be positive, got %d", count)
	}
	seen := make(map[string]bool, count)
	out := make([]Mutation, 0, count)
	maxAttempts := count*8 + 16
	for attempt := 0; len(out) < count && attempt < maxAttempts; attempt++ {
		seed := base + uint64(attempt)
		mut, err := p.Random(seed)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprint(mut.Times)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *mut)
	}
	if len(out) < count {
		return nil, fmt.Errorf("netsec: only %d distinct assignments exist in reachable seeds; requested %d", len(out), count)
	}
	return out, nil
}

// edgeWeight returns 1 for a strict order pr -> i, 0 for a weak one.
func (p *Problem) edgeWeight(pr, i int) int64 {
	for _, ord := range p.orders {
		if p.index[ord.Before] == pr && p.index[ord.After] == i {
			if ord.Kind == StrictOrder {
				return 1
			}
			return 0
		}
	}
	return 0
}

// predecessors lists, for each event, the events directly ordered before
// it. Transitive predecessors are respected automatically because they are
// fixed earlier in topological order.
func (p *Problem) predecessors() [][]int {
	n := len(p.names)
	preds := make([][]int, n)
	for _, ord := range p.orders {
		b := p.index[ord.Before]
		a := p.index[ord.After]
		preds[a] = append(preds[a], b)
	}
	return preds
}

// topoOrder returns a Kahn topological order; ties are broken with rng so
// different seeds explore different construction orders.
func (p *Problem) topoOrder(rng *rand.Rand) []int {
	n := len(p.names)
	indeg := make([]int, n)
	children := make([][]int, n)
	for _, ord := range p.orders {
		b := p.index[ord.Before]
		a := p.index[ord.After]
		children[b] = append(children[b], a)
		indeg[a]++
	}
	ready := make([]int, 0, n)
	for i, d := range indeg {
		if d == 0 {
			ready = append(ready, i)
		}
	}
	order := make([]int, 0, n)
	for len(ready) > 0 {
		k := rng.IntN(len(ready))
		node := ready[k]
		ready = append(ready[:k], ready[k+1:]...)
		order = append(order, node)
		for _, c := range children[node] {
			indeg[c]--
			if indeg[c] == 0 {
				ready = append(ready, c)
			}
		}
	}
	return order
}

// BoundarySamples groups the three mandatory edge-case assignments. Any
// field may be nil when that boundary is structurally impossible for the
// instance; the accompanying error is nil as long as the instance itself is
// feasible.
type BoundarySamples struct {
	// Simultaneous contains at least two events sharing a timestamp while
	// every strict order still holds.
	Simultaneous *Mutation
	// BeforeDeadline places at least one event at Deadline-1.
	BeforeDeadline *Mutation
	// AtDeadline places at least one event exactly at Deadline.
	AtDeadline *Mutation
}

// BoundarySamples constructs the three deterministic boundary assignments.
func (p *Problem) BoundarySamples() (*BoundarySamples, error) {
	if _, err := p.Solve(); err != nil {
		return nil, err
	}
	out := &BoundarySamples{}

	if mut, err := p.simultaneous(); err == nil {
		out.Simultaneous = mut
	}
	if mut, ok := p.pinAtDeadline(p.Deadline()-1, seedBeforeDead, "before-deadline"); ok {
		out.BeforeDeadline = mut
	}
	if mut, ok := p.pinAtDeadline(p.Deadline(), seedAtDead, "at-deadline"); ok {
		out.AtDeadline = mut
	}
	return out, nil
}

// simultaneous returns a legal assignment in which at least two events
// share a timestamp. The component-wise earliest assignment already
// coincides whenever a zero-weight (weak-order) path joins two events, so it
// is checked first. If no coincidence appears there, every weak order is
// tried as an explicit equality pin and the first feasible pin wins.
func (p *Problem) simultaneous() (*Mutation, error) {
	sol, cyc := p.solve(nil)
	if cyc != nil {
		return nil, cyc
	}
	if countDistinct(sol.earliest) < len(sol.earliest) {
		return &Mutation{Times: sol.earliest, Seed: seedSimult, Tag: "simultaneous"}, nil
	}

	for _, ord := range p.orders {
		if ord.Kind != WeakOrder {
			continue
		}
		before := p.index[ord.Before]
		after := p.index[ord.After]
		// Pin both endpoints to each value in their overlapping tight window.
		low := maxInt64(sol.earliest[before], sol.earliest[after])
		high := minInt64(sol.latest[before], sol.latest[after])
		for v := low; v <= high; v++ {
			pinned, cyc := p.solve(map[int]int64{before: v, after: v})
			if cyc != nil {
				continue
			}
			return &Mutation{Times: pinned.earliest, Seed: seedSimult, Tag: "simultaneous"}, nil
		}
	}
	return nil, fmt.Errorf("netsec: no simultaneous assignment exists for this partial order")
}

func countDistinct(xs []int64) int {
	seen := map[int64]struct{}{}
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// pinAtDeadline builds an assignment in which the event nearest the global
// deadline is pinned to target. Events whose own window cannot reach target
// are skipped; the tight latest vector decides the best candidate.
func (p *Problem) pinAtDeadline(target int64, seed uint64, tag string) (*Mutation, bool) {
	sol, cyc := p.solve(nil)
	if cyc != nil {
		return nil, false
	}
	var cands []int
	for i := range p.names {
		if target >= p.lo[i] && target <= p.hi[i] && sol.latest[i] >= target && sol.earliest[i] <= target {
			cands = append(cands, i)
		}
	}
	// Prefer the event whose tight upper bound equals the target so the
	// boundary really is tight, falling back to any reachable candidate.
	picked := -1
	for _, idx := range cands {
		if sol.latest[idx] == target {
			picked = idx
			break
		}
	}
	if picked == -1 && len(cands) > 0 {
		picked = cands[0]
	}
	if picked == -1 {
		return nil, false
	}
	sol2, cyc := p.solve(map[int]int64{picked: target})
	if cyc != nil {
		return nil, false
	}
	return &Mutation{Times: sol2.earliest, Seed: seed, Tag: tag}, true
}
