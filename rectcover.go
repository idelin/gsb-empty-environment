// Package rectcover solves the lattice rectangle cover problem: given target
// and forbidden points on a finite integer grid, cover every target with
// axis-aligned lattice rectangles that contain no forbidden point, minimizing
// first total area (overlaps are charged repeatedly) and then rectangle count.
package rectcover

import (
	"errors"
	"sort"
)

// Point is an integer lattice point.
type Point struct {
	X, Y int
}

// Rect is an axis-aligned lattice rectangle, inclusive on all sides.
type Rect struct {
	MinX, MinY, MaxX, MaxY int
}

// Area returns the number of lattice points inside the rectangle.
func (r Rect) Area() int {
	return (r.MaxX - r.MinX + 1) * (r.MaxY - r.MinY + 1)
}

// Contains reports whether p lies inside r (inclusive).
func (r Rect) Contains(p Point) bool {
	return p.X >= r.MinX && p.X <= r.MaxX && p.Y >= r.MinY && p.Y <= r.MaxY
}

// Status describes the outcome of a solve.
type Status string

const (
	// StatusOptimal means a provably optimal solution was found (optimal
	// among solutions within the configured budgets).
	StatusOptimal Status = "optimal"
	// StatusInfeasible means no cover exists within the configured budgets
	// (a target coincides with a forbidden point, or the area/rectangle
	// budget rules out every cover).
	StatusInfeasible Status = "infeasible"
	// StatusBudgetExhausted means the search-node budget interrupted the
	// search before optimality could be proven; the returned solution, if
	// any, is the best found so far.
	StatusBudgetExhausted Status = "budget_exhausted"
)

// Problem is a rectangle cover instance.
type Problem struct {
	Targets   []Point
	Forbidden []Point
}

// Options bounds the exact search. Zero means no limit.
type Options struct {
	MaxArea  int   // accept only solutions with total area <= MaxArea
	MaxRects int   // accept only solutions using at most MaxRects rectangles
	MaxNodes int64 // interrupt the search after MaxNodes branch nodes
}

// Evidence records which rectangle covers a given target point.
type Evidence struct {
	Target Point
	Rect   int // index into Solution.Rects
}

// Solution is the result of a solve.
type Solution struct {
	Status   Status
	Rects    []Rect
	Evidence []Evidence // one entry per target, in input order
	Area     int        // sum of rectangle areas; overlaps charged repeatedly
	Nodes    int64      // branch nodes explored
}

// ErrTooManyCandidates is returned by SolveExhaustive when the candidate set
// is too large for subset enumeration.
var ErrTooManyCandidates = errors.New("rectcover: too many candidates for exhaustive search")

// maxExhaustiveCandidates bounds subset enumeration (2^N subsets).
const maxExhaustiveCandidates = 22

// normalize deduplicates targets (preserving first-seen order) and reports
// whether any target is also forbidden.
func normalize(p Problem) ([]Point, []Point, bool) {
	seenT := make(map[Point]bool, len(p.Targets))
	targets := make([]Point, 0, len(p.Targets))
	for _, t := range p.Targets {
		if !seenT[t] {
			seenT[t] = true
			targets = append(targets, t)
		}
	}
	seenF := make(map[Point]bool, len(p.Forbidden))
	forbidden := make([]Point, 0, len(p.Forbidden))
	conflict := false
	for _, f := range p.Forbidden {
		if seenT[f] {
			conflict = true
		}
		if !seenF[f] {
			seenF[f] = true
			forbidden = append(forbidden, f)
		}
	}
	return targets, forbidden, conflict
}

// EnumerateCandidates returns every axis-aligned lattice rectangle that
// contains at least one target and no forbidden point, with each edge tight
// against a target's coordinate. Any optimal solution can be rewritten using
// only such rectangles: shrinking a rectangle's edges onto the targets it
// covers never loses coverage, never adds forbidden points, and never
// increases area.
func EnumerateCandidates(p Problem) []Rect {
	targets, forbidden, _ := normalize(p)
	if len(targets) == 0 {
		return nil
	}
	xs := make([]int, 0, len(targets))
	ys := make([]int, 0, len(targets))
	seenX := make(map[int]bool)
	seenY := make(map[int]bool)
	for _, t := range targets {
		if !seenX[t.X] {
			seenX[t.X] = true
			xs = append(xs, t.X)
		}
		if !seenY[t.Y] {
			seenY[t.Y] = true
			ys = append(ys, t.Y)
		}
	}
	sort.Ints(xs)
	sort.Ints(ys)

	var cands []Rect
	for _, x1 := range xs {
		for _, x2 := range xs {
			if x2 < x1 {
				continue
			}
			for _, y1 := range ys {
				for _, y2 := range ys {
					if y2 < y1 {
						continue
					}
					r := Rect{MinX: x1, MinY: y1, MaxX: x2, MaxY: y2}
					hasTarget := false
					for _, t := range targets {
						if r.Contains(t) {
							hasTarget = true
							break
						}
					}
					if !hasTarget {
						continue
					}
					bad := false
					for _, f := range forbidden {
						if r.Contains(f) {
							bad = true
							break
						}
					}
					if !bad {
						cands = append(cands, r)
					}
				}
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		ai, aj := cands[i].Area(), cands[j].Area()
		if ai != aj {
			return ai < aj
		}
		if cands[i].MinX != cands[j].MinX {
			return cands[i].MinX < cands[j].MinX
		}
		if cands[i].MinY != cands[j].MinY {
			return cands[i].MinY < cands[j].MinY
		}
		if cands[i].MaxX != cands[j].MaxX {
			return cands[i].MaxX < cands[j].MaxX
		}
		return cands[i].MaxY < cands[j].MaxY
	})
	return cands
}

// Solve exactly minimizes (total area, rectangle count) by depth-first
// branch and bound over the enumerated candidate rectangles.
func Solve(p Problem, opt Options) Solution {
	targets, _, conflict := normalize(p)
	if conflict {
		return Solution{Status: StatusInfeasible}
	}
	if len(targets) == 0 {
		return Solution{Status: StatusOptimal}
	}
	cands := EnumerateCandidates(p)

	s := &solver{
		targets: targets,
		cands:   cands,
		candOf:  make([][]int, len(targets)),
		minArea: make([]int, len(targets)),
		opt:     opt,
	}
	for i, t := range targets {
		best := 0
		for ci, r := range cands {
			if r.Contains(t) {
				s.candOf[i] = append(s.candOf[i], ci)
				if best == 0 || r.Area() < best {
					best = r.Area()
				}
			}
		}
		if len(s.candOf[i]) == 0 {
			// Unreachable without a forbidden conflict should not happen
			// (the 1x1 rectangle on the target is always a candidate), but
			// guard anyway: this target is uncoverable.
			return Solution{Status: StatusInfeasible}
		}
		s.minArea[i] = best
	}

	covered := make([]bool, len(targets))
	s.dfs(covered, 0, 0, nil)

	sol := Solution{Nodes: s.nodes}
	switch {
	case s.best == nil && s.interrupted:
		sol.Status = StatusBudgetExhausted
	case s.best == nil:
		sol.Status = StatusInfeasible
	case s.interrupted:
		sol.Status = StatusBudgetExhausted
		sol.Rects, sol.Area = s.materialize()
	default:
		sol.Status = StatusOptimal
		sol.Rects, sol.Area = s.materialize()
	}
	if sol.Rects != nil {
		sol.Evidence = buildEvidence(targets, sol.Rects)
	}
	return sol
}

type solver struct {
	targets []Point
	cands   []Rect
	candOf  [][]int // candidate indices covering each target
	minArea []int   // smallest candidate area covering each target
	opt     Options

	best      []int // best candidate-index selection so far
	bestArea  int
	bestCount int

	nodes       int64
	interrupted bool
}

func (s *solver) better(area, count int) bool {
	if s.best == nil {
		return true
	}
	if area != s.bestArea {
		return area < s.bestArea
	}
	return count < s.bestCount
}

// cannotBeat reports whether any completion of the current partial solution
// is guaranteed not to improve on the incumbent.
func (s *solver) cannotBeat(curArea, curCount, extraMinArea int) bool {
	if s.best == nil {
		return false
	}
	lo := curArea + extraMinArea
	if lo != s.bestArea {
		return lo > s.bestArea
	}
	return curCount+1 >= s.bestCount
}

func (s *solver) dfs(covered []bool, nCovered, curArea int, chosen []int) {
	s.nodes++
	if s.opt.MaxNodes > 0 && s.nodes > s.opt.MaxNodes {
		s.interrupted = true
		return
	}
	if s.interrupted {
		return
	}
	if nCovered == len(s.targets) {
		if s.better(curArea, len(chosen)) {
			s.best = append([]int(nil), chosen...)
			s.bestArea = curArea
			s.bestCount = len(chosen)
		}
		return
	}
	// Pick the uncovered target with the fewest covering candidates.
	t := -1
	for i := range s.targets {
		if !covered[i] && (t == -1 || len(s.candOf[i]) < len(s.candOf[t])) {
			t = i
		}
	}
	if s.cannotBeat(curArea, len(chosen), s.minArea[t]) {
		return
	}
	if s.opt.MaxRects > 0 && len(chosen) >= s.opt.MaxRects {
		return
	}
	for _, ci := range s.candOf[t] {
		r := s.cands[ci]
		nextArea := curArea + r.Area()
		if s.opt.MaxArea > 0 && nextArea > s.opt.MaxArea {
			continue
		}
		if !s.better(nextArea, len(chosen)+1) {
			continue
		}
		var newly []int
		for j, tp := range s.targets {
			if !covered[j] && r.Contains(tp) {
				covered[j] = true
				newly = append(newly, j)
			}
		}
		s.dfs(covered, nCovered+len(newly), nextArea, append(chosen, ci))
		for _, j := range newly {
			covered[j] = false
		}
		if s.interrupted {
			return
		}
	}
}

func (s *solver) materialize() ([]Rect, int) {
	rects := make([]Rect, len(s.best))
	area := 0
	for i, ci := range s.best {
		rects[i] = s.cands[ci]
		area += s.cands[ci].Area()
	}
	return rects, area
}

// buildEvidence assigns each target to the first rectangle covering it.
func buildEvidence(targets []Point, rects []Rect) []Evidence {
	ev := make([]Evidence, len(targets))
	for i, t := range targets {
		ev[i].Target = t
		ev[i].Rect = -1
		for ri, r := range rects {
			if r.Contains(t) {
				ev[i].Rect = ri
				break
			}
		}
	}
	return ev
}

// Verify checks that sol covers every target, keeps every rectangle free of
// forbidden points, and reports an area consistent with its rectangles.
func (p Problem) Verify(sol Solution) error {
	targets, forbidden, conflict := normalize(p)
	if conflict {
		return errors.New("rectcover: target coincides with a forbidden point")
	}
	for _, r := range sol.Rects {
		if r.MinX > r.MaxX || r.MinY > r.MaxY {
			return errors.New("rectcover: malformed rectangle")
		}
		for _, f := range forbidden {
			if r.Contains(f) {
				return errors.New("rectcover: rectangle contains a forbidden point")
			}
		}
	}
	covered := make([]bool, len(targets))
	for _, r := range sol.Rects {
		for i, t := range targets {
			if r.Contains(t) {
				covered[i] = true
			}
		}
	}
	for i, ok := range covered {
		if !ok {
			return errors.New("rectcover: uncovered target (gap) at " + pointString(targets[i]))
		}
	}
	area := 0
	for _, r := range sol.Rects {
		area += r.Area()
	}
	if area != sol.Area {
		return errors.New("rectcover: reported area does not match rectangles")
	}
	if len(sol.Evidence) != len(targets) {
		return errors.New("rectcover: evidence missing for some targets")
	}
	for _, e := range sol.Evidence {
		if e.Rect < 0 || e.Rect >= len(sol.Rects) || !sol.Rects[e.Rect].Contains(e.Target) {
			return errors.New("rectcover: evidence rectangle does not cover its target")
		}
	}
	return nil
}

func pointString(p Point) string {
	return "(" + itoa(p.X) + "," + itoa(p.Y) + ")"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// SolveExhaustive finds the optimum by enumerating every candidate subset.
// It is intended for cross-checking Solve on small grids and fails with
// ErrTooManyCandidates when the instance has more than 24 candidates.
func SolveExhaustive(p Problem) (Solution, error) {
	targets, _, conflict := normalize(p)
	if conflict {
		return Solution{Status: StatusInfeasible}, nil
	}
	if len(targets) == 0 {
		return Solution{Status: StatusOptimal}, nil
	}
	cands := EnumerateCandidates(p)
	if len(cands) > maxExhaustiveCandidates {
		return Solution{}, ErrTooManyCandidates
	}
	n := len(cands)
	bestArea, bestCount := 0, 0
	var bestMask uint64
	found := false
	for mask := uint64(1); mask < uint64(1)<<uint(n); mask++ {
		area := 0
		count := 0
		ok := true
		cov := make([]bool, len(targets))
		ncov := 0
		for ci := 0; ci < n; ci++ {
			if mask&(uint64(1)<<uint(ci)) == 0 {
				continue
			}
			area += cands[ci].Area()
			count++
			if found && (area > bestArea || (area == bestArea && count >= bestCount)) {
				ok = false
				break
			}
			for i, t := range targets {
				if !cov[i] && cands[ci].Contains(t) {
					cov[i] = true
					ncov++
				}
			}
		}
		if !ok || ncov != len(targets) {
			continue
		}
		if !found || area < bestArea || (area == bestArea && count < bestCount) {
			found = true
			bestArea = area
			bestCount = count
			bestMask = mask
		}
	}
	if !found {
		return Solution{Status: StatusInfeasible}, nil
	}
	sol := Solution{Status: StatusOptimal, Area: bestArea}
	for ci := 0; ci < n; ci++ {
		if bestMask&(uint64(1)<<uint(ci)) != 0 {
			sol.Rects = append(sol.Rects, cands[ci])
		}
	}
	sol.Evidence = buildEvidence(targets, sol.Rects)
	return sol, nil
}
