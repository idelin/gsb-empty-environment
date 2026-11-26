package sdf

// livenessResult reports an exact reachability analysis.
type livenessResult struct {
	live      bool
	deadEdge  int // edge that starves the blocked actor, -1 otherwise
	fullEdge  int // edge whose free space blocks a firing, -1 otherwise
	explored  int
	truncated bool
}

// livenessCap bounds the exact state search. The bound is an explicit safety
// envelope only: hitting it yields StatusInconclusive rather than a claim of
// stability, so liveness is never asserted from a fixed simulation horizon.
const livenessCap = 4_000_000

// checkLiveness decides whether the actor system can keep firing forever from
// the initial state. In the untimed abstraction every firing completes
// immediately, so a state is just the token backlog of each edge.
//
// Input edges have an unbounded external source: they only constrain consumer
// launches through their bounded buffer. Output edges never back-pressure the
// producer because the external sink drains them at completion.
//
// Liveness means there exists an infinite legal firing sequence. The finite
// state space (each backlog is bounded by MaxCap) makes this exact: a state is
// live iff it is part of or reaches a reachable cycle with no dead state on
// the path. We compute it by exhaustive reachability and then checking that
// every reached state has at least one outgoing transition.
func checkLiveness(p *Problem, idx *index, caps []int) livenessResult {
	E := len(p.Edges)
	start := make([]int, E)
	for eid, e := range p.Edges {
		start[eid] = e.Delay
	}
	visited := map[string]struct{}{}
	queue := [][]int{start}
	visited[encodeOcc(start)] = struct{}{}
	var deadState []int
	var starve, full = -1, -1
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(visited) >= livenessCap {
			return livenessResult{truncated: true, explored: len(visited)}
		}
		fireable, se, fe := fireableActors(p, idx, caps, cur)
		if len(fireable) == 0 {
			deadState = cur
			starve, full = se, fe
			break
		}
		for _, a := range fireable {
			next := stepFire(p, idx, caps, cur, a)
			key := encodeOcc(next)
			if _, seen := visited[key]; !seen {
				visited[key] = struct{}{}
				queue = append(queue, next)
			}
		}
	}
	if deadState != nil {
		return livenessResult{live: false, deadEdge: starve, fullEdge: full, explored: len(visited)}
	}
	// Every reached state had an outgoing transition and the space is finite,
	// so an infinite path (cycle) necessarily exists.
	return livenessResult{live: true, explored: len(visited)}
}

// fireableActors returns actors that can fire now, plus diagnostic edges for
// the first blocked actor encountered.
func fireableActors(p *Problem, idx *index, caps []int, occ []int) ([]int, int, int) {
	var fireable []int
	starve, full := -1, -1
	for a := 0; a < p.Actors; a++ {
		ok, se, fe := canFire(p, idx, caps, occ, a)
		if ok {
			fireable = append(fireable, a)
			continue
		}
		if starve == -1 && se != -1 {
			starve = se
		}
		if full == -1 && fe != -1 {
			full = fe
		}
	}
	return fireable, starve, full
}

// canFire checks token availability and statically reserved output space.
func canFire(p *Problem, idx *index, caps []int, occ []int, a int) (bool, int, int) {
	for _, eid := range idx.in[a] {
		if p.Edges[eid].IsInput() {
			// External source never runs dry; only the bounded buffer can
			// block, and that is checked through the outgoing edges below.
			continue
		}
		if occ[eid] < p.Edges[eid].Cons {
			return false, eid, -1
		}
	}
	// Project the post-consumption backlog, then verify every internal output
	// can reserve its production. A self-loop frees its consumed slots first.
	post := append([]int(nil), occ...)
	for _, eid := range idx.in[a] {
		if !p.Edges[eid].IsInput() {
			post[eid] -= p.Edges[eid].Cons
		}
	}
	for _, eid := range idx.out[a] {
		e := p.Edges[eid]
		if e.IsOutput() {
			continue // external sink drains at completion
		}
		if post[eid]+e.Prod > caps[eid] {
			return false, -1, eid
		}
	}
	return true, -1, -1
}

// stepFire consumes inputs and publishes internal outputs (untimed immediate
// completion). Output edges vanish into the external sink.
func stepFire(p *Problem, idx *index, caps, occ []int, a int) []int {
	next := append([]int(nil), occ...)
	for _, eid := range idx.in[a] {
		if p.Edges[eid].IsInput() {
			continue // tokens come from the infinite external source
		}
		next[eid] -= p.Edges[eid].Cons
	}
	for _, eid := range idx.out[a] {
		e := p.Edges[eid]
		if e.IsOutput() {
			continue
		}
		next[eid] += e.Prod
	}
	return next
}

func encodeOcc(occ []int) string {
	b := make([]byte, 0, len(occ)*2)
	for _, v := range occ {
		b = append(b, byte(v&0xff), byte((v>>8)&0xff))
	}
	return string(b)
}

// edgeLowerBound finds the smallest capacity in [delay, MaxCap] for which the
// isolated edge never reaches a dead state. The result is a necessary
// structural lower bound for the whole graph.
func edgeLowerBound(p *Problem, eid int) (bound, witness int, ok bool) {
	e := p.Edges[eid]
	lo := e.Delay
	if e.IsOutput() {
		// One firing's production must fit at launch; the sink drains it.
		return max2(e.Delay, e.Prod), e.Delay, true
	}
	if e.IsInput() {
		// External source is unbounded but the bounded edge must absorb
		// back-to-back arrivals while an in-flight consumer still holds its
		// reserved path: one firing's consumption plus one queued token.
		return max2(e.Delay, e.Cons+1), e.Delay, true
	}
	for c := lo; c <= p.MaxCap; c++ {
		if edgeSoloFeasible(p, eid, c) {
			wit := 0
			if c > 0 {
				wit = edgeMaxBacklog(p, eid, c-1)
			}
			return c, wit, true
		}
	}
	return -1, edgeMaxBacklog(p, eid, p.MaxCap), false
}

// edgeState is (backlog, reserved) for an isolated edge.
type edgeState struct{ backlog, reserved int }

// edgeSoloFeasible reports whether any reachable state is a dead end. State
// space is bounded by cap (<= MaxCap), so DFS with memoization is exact and
// never relies on a simulation horizon.
func edgeSoloFeasible(p *Problem, eid, cap int) bool {
	e := p.Edges[eid]
	seen := map[edgeState]struct{}{}
	var dfs func(s edgeState) bool
	dfs = func(s edgeState) bool {
		if _, ok := seen[s]; ok {
			return true // all states on this path still have moves
		}
		seen[s] = struct{}{}
		moves := edgeMoves(p, eid, cap, s)
		if len(moves) == 0 {
			return false
		}
		for _, m := range moves {
			if !dfs(m) {
				return false
			}
		}
		return true
	}
	return dfs(edgeState{backlog: e.Delay})
}

// edgeMoves enumerates legal transitions of the isolated edge:
// producer launch (reserve), in-flight completion, consumer launch, and for
// input edges external injection.
func edgeMoves(p *Problem, eid, cap int, s edgeState) []edgeState {
	e := p.Edges[eid]
	var out []edgeState
	if !e.IsInput() && s.backlog+s.reserved+e.Prod <= cap {
		out = append(out, edgeState{s.backlog, s.reserved + e.Prod})
	}
	if s.reserved > 0 {
		out = append(out, edgeState{s.backlog + s.reserved, 0})
	}
	if s.backlog >= e.Cons {
		out = append(out, edgeState{s.backlog - e.Cons, s.reserved})
	}
	if e.IsInput() && s.backlog < cap {
		out = append(out, edgeState{s.backlog + 1, s.reserved})
	}
	return out
}

// edgeMaxBacklog returns the largest backlog reachable on an isolated edge
// with the given capacity, used as the certificate witness.
func edgeMaxBacklog(p *Problem, eid, cap int) int {
	e := p.Edges[eid]
	best := e.Delay
	seen := map[edgeState]struct{}{}
	var dfs func(s edgeState)
	dfs = func(s edgeState) {
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		if s.backlog > best {
			best = s.backlog
		}
		for _, m := range edgeMoves(p, eid, cap, s) {
			dfs(m)
		}
	}
	dfs(edgeState{backlog: e.Delay})
	return best
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
