package spp

import "fmt"

// Outcome classifies the control-plane convergence result.
type Outcome int

const (
	// OutcomeNoStable means no Stable Path Assignment exists: every state has
	// at least one AS that wants to switch, so BGP oscillates forever.
	OutcomeNoStable Outcome = iota
	// OutcomeUnique means exactly one Stable Path Assignment exists; the
	// instance is solvable and the unique fixed point is reported.
	OutcomeUnique
	// OutcomeMultiple means at least two mutually exclusive Stable Path
	// Assignments exist; message timing decides which split materializes.
	OutcomeMultiple
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNoStable:
		return "no-stable-assignment (permanent oscillation)"
	case OutcomeUnique:
		return "unique-stable-assignment (safe convergence)"
	default:
		return "multiple-stable-assignments (non-deterministic split)"
	}
}

// SolveOptions bounds exact enumeration.
type SolveOptions struct {
	// MaxSolutions caps the number of reported SPAs (0 -> default 64).
	MaxSolutions int
	// MaxBacktracks bounds the search (0 -> default 2_000_000).
	MaxBacktracks int
}

// SolveResult is the exact stability answer plus search diagnostics.
type SolveResult struct {
	Outcome    Outcome
	Solutions  []Assignment
	Backtracks int
	Truncated  bool
}

// Assignment maps every AS (including the origin) to its selected path.
// A nil/empty path means the AS currently holds no route to d.
type Assignment map[int][]int

type solver struct {
	inst      *Instance
	nodes     []int
	opts      SolveOptions
	current   Assignment
	solutions []Assignment
	steps     int
	truncated bool
}

// Solve performs exact SPA enumeration. The origin is pinned to the empty
// (zero-length) route [origin]; every other AS is either unrouted or selects
// a permitted path whose suffix is the route currently selected by its next
// hop. Stability requires the selected path to be the best extendable path.
func (in *Instance) Solve(opts SolveOptions) (*SolveResult, error) {
	if opts.MaxSolutions <= 0 {
		opts.MaxSolutions = 64
	}
	if opts.MaxBacktracks <= 0 {
		opts.MaxBacktracks = 2_000_000
	}
	s := &solver{
		inst:    in,
		nodes:   in.SortedNodes(),
		opts:    opts,
		current: Assignment{},
	}
	// Pin the origin.
	s.current[in.Config.Origin] = []int{in.Config.Origin}
	s.dfs(0)

	res := &SolveResult{
		Solutions:  s.solutions,
		Backtracks: s.steps,
		Truncated:  s.truncated,
	}
	switch {
	case len(s.solutions) == 0:
		res.Outcome = OutcomeNoStable
	case len(s.solutions) == 1:
		res.Outcome = OutcomeUnique
	default:
		res.Outcome = OutcomeMultiple
	}
	return res, nil
}

func (s *solver) dfs(idx int) bool {
	if len(s.solutions) >= s.opts.MaxSolutions {
		s.truncated = true
		return true
	}
	if idx == len(s.nodes) {
		if s.stableFull() {
			s.solutions = append(s.solutions, s.snapshot())
		}
		return len(s.solutions) >= s.opts.MaxSolutions
	}
	s.steps++
	if s.steps > s.opts.MaxBacktracks {
		s.truncated = true
		return true
	}
	node := s.nodes[idx]
	if node == s.inst.Config.Origin {
		return s.dfs(idx + 1)
	}

	// If a permitted path of node is currently advertised by an already
	// decided neighbor, node is forced to select some advertised path: an
	// unrouted or inconsistent (non-advertised) choice is pruned immediately.
	advertised := s.advertisedPaths(node)

	// Option 1: node holds no route. This is legal only when none of its
	// permitted paths is extendable given the decisions already made. A path
	// whose next hop is still undecided keeps the node undecided, so it can
	// never be parked as unrouted ahead of that neighbor.
	if len(advertised) == 0 && s.canBeUnrouted(node) {
		s.current[node] = nil
		if s.dfs(idx + 1) {
			return true
		}
		delete(s.current, node)
	}

	// Option 2: node selects each advertised (or still-possible) permitted
	// path. Assigning it forces consistent suffix choices downstream.
	for _, pr := range s.inst.Ranked[node] {
		if !s.pathSelectable(node, pr.Path, advertised) {
			continue
		}
		ok, undo := s.tryForce(node, pr.Path)
		if !ok {
			undo()
			continue
		}
		if s.dfs(idx + 1) {
			undo()
			return true
		}
		undo()
	}
	return false
}

// advertisedPaths returns node's permitted paths whose immediate next hop is
// decided and advertises the exact matching suffix.
func (s *solver) advertisedPaths(node int) [][]int {
	var out [][]int
	for _, pr := range s.inst.Ranked[node] {
		p := pr.Path
		if len(p) < 2 {
			continue
		}
		selected, decided := s.current[p[1]]
		if decided && len(selected) > 0 && pathEquals(selected, p[1:]) {
			out = append(out, CopyPath(p))
		}
	}
	return out
}

// pathSelectable prunes paths whose immediate next hop is decided but does
// not advertise the required suffix. When no path is advertised yet, any path
// with an undecided next hop remains possible.
func (s *solver) pathSelectable(node int, p []int, advertised [][]int) bool {
	if len(p) < 2 {
		return false
	}
	selected, decided := s.current[p[1]]
	if !decided {
		return true // neighbor still free: this route may later arrive
	}
	return len(selected) > 0 && pathEquals(selected, p[1:])
}

// canBeUnrouted reports whether node may legally hold no route given the
// partial assignment: every permitted path must be blocked by an already
// decided neighbor (undecided neighbors mean a future route may arrive).
func (s *solver) canBeUnrouted(node int) bool {
	for _, pr := range s.inst.Ranked[node] {
		p := pr.Path
		if len(p) < 2 {
			continue
		}
		next := p[1]
		selected, decided := s.current[next]
		if !decided {
			return false // neighbor may still advertise: cannot park unrouted
		}
		if len(selected) > 0 && pathEquals(selected, p[1:]) {
			return false // this path is currently advertised
		}
	}
	return true
}

// tryForce assigns p to u and every unassigned downstream suffix node. It
// returns an undo closure; on conflict it returns ok=false.
func (s *solver) tryForce(u int, p []int) (bool, func()) {
	type change struct {
		node int
		old  []int
		had  bool
	}
	var changes []change
	rollback := func() {
		for i := len(changes) - 1; i >= 0; i-- {
			c := changes[i]
			if c.had {
				s.current[c.node] = c.old
			} else {
				delete(s.current, c.node)
			}
		}
	}
	assign := func(n int, path []int) bool {
		old, had := s.current[n]
		if had {
			return pathEquals(old, path)
		}
		changes = append(changes, change{node: n, old: old, had: had})
		s.current[n] = CopyPath(path)
		return true
	}
	// Walk u -> ... -> origin. Every suffix node must select its suffix path.
	for i := 0; i < len(p); i++ {
		n := p[i]
		if n == s.inst.Config.Origin {
			if !assign(n, []int{s.inst.Config.Origin}) {
				return false, rollback
			}
			continue
		}
		suffix := p[i:]
		if _, legal := s.inst.RankOf(n, suffix); !legal && n != u {
			return false, rollback
		}
		if !assign(n, suffix) {
			return false, rollback
		}
	}
	return true, rollback
}

func (s *solver) stableFull() bool {
	for _, n := range s.nodes {
		if n == s.inst.Config.Origin {
			continue
		}
		chosen := s.current[n]
		best := s.inst.BestAvailable(n, s.current)
		switch {
		case best == nil:
			if len(chosen) > 0 {
				return false
			}
		case len(chosen) == 0 || !pathEquals(chosen, best):
			return false
		}
	}
	return true
}

func (s *solver) snapshot() Assignment {
	out := Assignment{}
	for _, n := range s.nodes {
		out[n] = CopyPath(s.current[n])
	}
	return out
}

// IsStable is a standalone checker for a complete candidate assignment.
func (in *Instance) IsStable(a Assignment) error {
	for _, n := range in.SortedNodes() {
		if n == in.Config.Origin {
			continue
		}
		chosen := a[n]
		best := in.BestAvailable(n, a)
		switch {
		case best == nil && (chosen == nil || len(chosen) == 0):
			// ok: unrouted
		case best != nil && pathEquals(chosen, best):
			// ok
		default:
			return fmt.Errorf("spp: AS %d selects %v but best available is %v", n, chosen, best)
		}
	}
	return nil
}
