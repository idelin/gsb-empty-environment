package main

import (
	"fmt"
	"math/bits"
	"sort"
)

// maxProjects is the hard limit on the number of projects (bitmask based search).
const maxProjects = 26

// maxMagnitude caps absolute values so that all internal arithmetic stays
// exact in 64/128-bit integers. Upper-bound marginals are bounded by
// (maxProjects+1)*maxMagnitude < 2^63, and any product of two such numbers
// fits in 128 bits.
const maxMagnitude = int64(1_000_000_000_000_000)

// Project is a single candidate investment.
type Project struct {
	Name     string   `json:"name"`
	Cost     int64    `json:"cost"`
	Value    int64    `json:"value"`
	Requires []string `json:"requires,omitempty"`
	Excludes []string `json:"excludes,omitempty"`
}

// Synergy is a pairwise benefit (may be negative) realized only when both
// endpoints are selected. It is never folded into standalone project values.
type Synergy struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Value int64  `json:"value"`
}

// Input is the problem instance.
type Input struct {
	Budget    int64     `json:"budget"`
	Projects  []Project `json:"projects"`
	Synergies []Synergy `json:"synergies,omitempty"`
}

// SynergyPair is an active pairwise term in the chosen solution.
type SynergyPair struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Value int64  `json:"value"`
}

// Result is the optimal solution together with optimality evidence.
type Result struct {
	Selected        []string      `json:"selected"`
	TotalCost       int64         `json:"totalCost"`
	Budget          int64         `json:"budget"`
	StandaloneSum   int64         `json:"standaloneSum"`
	SynergySum      int64         `json:"synergySum"`
	Total           int64         `json:"total"`
	ActiveSynergies []SynergyPair `json:"activeSynergies"`
	Nodes           int64         `json:"nodes"`
	Pruned          int64         `json:"pruned"`
	RootBound       int64         `json:"rootBound"`
}

// Solver solves the quadratic budget-selection problem exactly by
// branch & bound. Pairwise synergies are kept as pairwise terms during the
// whole search; they are never amortized into fixed project values.
type Solver struct {
	n       int
	names   []string
	cost    []int64
	value   []int64
	syn     []int64  // flat n*n, symmetric, diagonal 0
	prereq  []uint64 // transitive closure of requires (excludes self)
	exclude []uint64
	order   []int     // decision order: topological, high priority first
	posSyn  [][]int64 // posSyn[i][p] = sum of max(0, syn[i][order[k]]) for k >= p
	budget  int64

	best      int64
	bestMask  uint64
	nodes     int64
	pruned    int64
	rootBound int64
}

// NewSolver validates the input and precomputes the search structures.
func NewSolver(in Input) (*Solver, error) {
	n := len(in.Projects)
	if n > maxProjects {
		return nil, fmt.Errorf("too many projects: %d > %d", n, maxProjects)
	}
	if in.Budget < 0 || in.Budget > maxMagnitude {
		return nil, fmt.Errorf("budget %d out of range [0, %d]", in.Budget, maxMagnitude)
	}
	idx := make(map[string]int, n)
	s := &Solver{n: n, budget: in.Budget}
	for i, p := range in.Projects {
		if p.Name == "" {
			return nil, fmt.Errorf("project %d: empty name", i)
		}
		if _, dup := idx[p.Name]; dup {
			return nil, fmt.Errorf("duplicate project name %q", p.Name)
		}
		if p.Cost < 0 || p.Cost > maxMagnitude {
			return nil, fmt.Errorf("project %q: cost %d out of range", p.Name, p.Cost)
		}
		if abs64(p.Value) > maxMagnitude {
			return nil, fmt.Errorf("project %q: value %d out of range", p.Name, p.Value)
		}
		idx[p.Name] = i
		s.names = append(s.names, p.Name)
		s.cost = append(s.cost, p.Cost)
		s.value = append(s.value, p.Value)
	}
	s.syn = make([]int64, n*n)
	s.prereq = make([]uint64, n)
	s.exclude = make([]uint64, n)

	for i, p := range in.Projects {
		for _, r := range p.Requires {
			j, ok := idx[r]
			if !ok {
				return nil, fmt.Errorf("project %q requires unknown project %q", p.Name, r)
			}
			if j == i {
				return nil, fmt.Errorf("project %q requires itself", p.Name)
			}
			s.prereq[i] |= 1 << uint(j)
		}
		for _, e := range p.Excludes {
			j, ok := idx[e]
			if !ok {
				return nil, fmt.Errorf("project %q excludes unknown project %q", p.Name, e)
			}
			if j == i {
				return nil, fmt.Errorf("project %q excludes itself", p.Name)
			}
			s.exclude[i] |= 1 << uint(j)
			s.exclude[j] |= 1 << uint(i)
		}
	}
	for _, sy := range in.Synergies {
		a, ok := idx[sy.A]
		if !ok {
			return nil, fmt.Errorf("synergy references unknown project %q", sy.A)
		}
		b, ok := idx[sy.B]
		if !ok {
			return nil, fmt.Errorf("synergy references unknown project %q", sy.B)
		}
		if a == b {
			return nil, fmt.Errorf("synergy of project %q with itself", sy.A)
		}
		if abs64(sy.Value) > maxMagnitude {
			return nil, fmt.Errorf("synergy %q-%q: value %d out of range", sy.A, sy.B, sy.Value)
		}
		s.syn[a*n+b] += sy.Value
		s.syn[b*n+a] += sy.Value
	}

	// Transitive closure of prerequisites (n <= 26, simple fixpoint).
	for changed := true; changed; {
		changed = false
		for i := 0; i < n; i++ {
			m := s.prereq[i]
			for mm := m; mm != 0; mm &= mm - 1 {
				j := bits.TrailingZeros64(mm)
				if add := s.prereq[j] &^ m; add != 0 {
					s.prereq[i] |= add
					changed = true
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if s.prereq[i]>>uint(i)&1 == 1 {
			return nil, fmt.Errorf("prerequisite cycle involving project %q", s.names[i])
		}
		if s.prereq[i]&s.exclude[i] != 0 {
			return nil, fmt.Errorf("project %q both requires and excludes the same project", s.names[i])
		}
	}

	s.buildOrder()
	s.buildPosSyn()
	return s, nil
}

// priority is a heuristic score used only to order the search; it never
// affects correctness.
func (s *Solver) priority(i int) float64 {
	score := float64(s.value[i])
	for j := 0; j < s.n; j++ {
		if v := s.syn[i*s.n+j]; v > 0 {
			score += float64(v)
		}
	}
	c := s.cost[i]
	if c < 1 {
		c = 1
	}
	return score / float64(c)
}

// buildOrder produces a topological decision order (prerequisites first),
// breaking ties by descending priority.
func (s *Solver) buildOrder() {
	n := s.n
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		indeg[i] = bits.OnesCount64(s.prereq[i])
	}
	done := uint64(0)
	for len(s.order) < n {
		best := -1
		for i := 0; i < n; i++ {
			if indeg[i] != 0 || done>>uint(i)&1 == 1 {
				continue
			}
			if best == -1 || s.priority(i) > s.priority(best) {
				best = i
			}
		}
		s.order = append(s.order, best)
		done |= 1 << uint(best)
		for i := 0; i < n; i++ {
			if s.prereq[i]>>uint(best)&1 == 1 {
				indeg[i]--
			}
		}
	}
}

// buildPosSyn precomputes, for every item and every decision position, the
// sum of positive synergies with items still undecided at that position.
func (s *Solver) buildPosSyn() {
	n := s.n
	s.posSyn = make([][]int64, n)
	for i := 0; i < n; i++ {
		row := make([]int64, n+1)
		for p := n - 1; p >= 0; p-- {
			row[p] = row[p+1]
			if v := s.syn[i*n+s.order[p]]; v > 0 {
				row[p] += v
			}
		}
		s.posSyn[i] = row
	}
}

// bound computes a valid upper bound on the total value reachable from a
// node: current value plus a fractional-knapsack relaxation over undecided
// items whose profits are per-item upper-bound marginals
//
//	ub(i) = value[i] + sum of positive synergies with selected items
//	      + sum of positive synergies with undecided items.
//
// For any completion S', the true added value is at most sum of ub(i) over
// S' (negative synergies dropped, positive internal synergies counted at
// both endpoints), and the fractional knapsack dominates that sum because
// S' respects the remaining budget. All arithmetic is exact integer math;
// the fractional part is floored, which is valid because the true total is
// an integer.
func (s *Solver) bound(pos int, sel uint64, cost, val int64) int64 {
	rem := s.budget - cost
	total := val
	var ubs [maxProjects]int64
	var costs [maxProjects]int64
	m := 0
	for p := pos; p < s.n; p++ {
		i := s.order[p]
		ub := s.value[i] + s.posSyn[i][pos]
		for mm := sel; mm != 0; mm &= mm - 1 {
			j := bits.TrailingZeros64(mm)
			if v := s.syn[i*s.n+j]; v > 0 {
				ub += v
			}
		}
		if ub <= 0 {
			continue
		}
		if s.cost[i] == 0 {
			total += ub
			continue
		}
		ubs[m] = ub
		costs[m] = s.cost[i]
		m++
	}
	// Sort by decreasing ratio ub/cost using exact 128-bit cross products.
	idx := make([]int, m)
	for k := range idx {
		idx[k] = k
	}
	sort.Slice(idx, func(a, b int) bool {
		x, y := idx[a], idx[b]
		hi1, lo1 := bits.Mul64(uint64(ubs[x]), uint64(costs[y]))
		hi2, lo2 := bits.Mul64(uint64(ubs[y]), uint64(costs[x]))
		if hi1 != hi2 {
			return hi1 > hi2
		}
		return lo1 > lo2
	})
	for _, k := range idx {
		if costs[k] <= rem {
			total += ubs[k]
			rem -= costs[k]
			continue
		}
		if rem > 0 {
			hi, lo := bits.Mul64(uint64(ubs[k]), uint64(rem))
			quo, _ := bits.Div64(hi, lo, uint64(costs[k]))
			total += int64(quo)
		}
		break
	}
	return total
}

// Solve runs the branch & bound search and returns the optimal solution.
func (s *Solver) Solve() Result {
	s.best = 0 // the empty selection is always feasible
	s.bestMask = 0
	s.rootBound = s.bound(0, 0, 0, 0)
	s.dfs(0, 0, 0, 0)
	return s.buildResult()
}

func (s *Solver) dfs(pos int, sel uint64, cost, val int64) {
	s.nodes++
	if val > s.best {
		s.best = val
		s.bestMask = sel
	}
	if pos == s.n {
		return
	}
	if s.bound(pos, sel, cost, val) <= s.best {
		s.pruned++
		return
	}
	i := s.order[pos]
	bit := uint64(1) << uint(i)
	// Include branch: prerequisites already decided (topological order) must
	// all be selected; no selected item may be mutually exclusive; budget
	// must hold.
	if s.prereq[i]&^sel == 0 && s.exclude[i]&sel == 0 && cost+s.cost[i] <= s.budget {
		add := s.value[i]
		for mm := sel; mm != 0; mm &= mm - 1 {
			add += s.syn[i*s.n+bits.TrailingZeros64(mm)]
		}
		s.dfs(pos+1, sel|bit, cost+s.cost[i], val+add)
	}
	// Exclude branch.
	s.dfs(pos+1, sel, cost, val)
}

// buildResult recomputes the full decomposition from the best mask.
func (s *Solver) buildResult() Result {
	res := Result{Budget: s.budget, Nodes: s.nodes, Pruned: s.pruned, RootBound: s.rootBound}
	mask := s.bestMask
	for i := 0; i < s.n; i++ {
		if mask>>uint(i)&1 == 0 {
			continue
		}
		res.Selected = append(res.Selected, s.names[i])
		res.TotalCost += s.cost[i]
		res.StandaloneSum += s.value[i]
	}
	for i := 0; i < s.n; i++ {
		if mask>>uint(i)&1 == 0 {
			continue
		}
		for j := i + 1; j < s.n; j++ {
			if mask>>uint(j)&1 == 0 {
				continue
			}
			if v := s.syn[i*s.n+j]; v != 0 {
				res.ActiveSynergies = append(res.ActiveSynergies, SynergyPair{A: s.names[i], B: s.names[j], Value: v})
				res.SynergySum += v
			}
		}
	}
	res.Total = res.StandaloneSum + res.SynergySum
	return res
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
