package main

import (
	"fmt"
	"sort"
	"strings"
)

// Tables holds the old and new next-hop tables.
// Old[i][d] / New[i][d] is the next-hop node of i toward destination d.
// A value of -1 means "no route" (blackhole). The diagonal is ignored.
type Tables struct {
	N   int
	Old [][]int
	New [][]int
}

// Planner searches for loop-free update plans.
type Planner struct {
	n    int
	old  [][]int
	new  [][]int
	diff []int // nodes whose row changed and thus need an update
}

func NewPlanner(t Tables) (*Planner, error) {
	if t.N < 1 || t.N > 18 {
		return nil, fmt.Errorf("nodes must be in [1,18], got %d", t.N)
	}
	if len(t.Old) != t.N || len(t.New) != t.N {
		return nil, fmt.Errorf("old/new must have exactly %d rows", t.N)
	}
	for i := 0; i < t.N; i++ {
		if len(t.Old[i]) != t.N || len(t.New[i]) != t.N {
			return nil, fmt.Errorf("row %d must have exactly %d entries", i, t.N)
		}
		for d := 0; d < t.N; d++ {
			for _, v := range []int{t.Old[i][d], t.New[i][d]} {
				if v < -1 || v >= t.N {
					return nil, fmt.Errorf("entry [%d][%d]=%d out of range", i, d, v)
				}
			}
		}
	}
	p := &Planner{n: t.N, old: t.Old, new: t.New}
	for i := 0; i < t.N; i++ {
		changed := false
		for d := 0; d < t.N; d++ {
			if t.Old[i][d] != t.New[i][d] {
				changed = true
				break
			}
		}
		if changed {
			p.diff = append(p.diff, i)
		}
	}
	return p, nil
}

// nextHop returns the next hop of node i toward dest under the given state
// (bit i of mask set => node i already uses its new row).
func (p *Planner) nextHop(i, dest int, mask uint32) int {
	if i == dest {
		return dest
	}
	if mask&(uint32(1)<<uint(i)) != 0 {
		return p.new[i][dest]
	}
	return p.old[i][dest]
}

// Trace follows next hops from src toward dest under state mask.
// It returns the path taken; if a forwarding loop is found, cycle holds the
// looping suffix; if a blackhole (-1) is hit, stuck is true.
func (p *Planner) Trace(mask uint32, dest, src int) (path []int, cycle []int, stuck bool) {
	seen := make(map[int]int, p.n)
	cur := src
	for {
		if cur == dest {
			path = append(path, cur)
			return path, nil, false
		}
		if pos, ok := seen[cur]; ok {
			return path, append([]int(nil), path[pos:]...), false
		}
		seen[cur] = len(path)
		path = append(path, cur)
		nh := p.nextHop(cur, dest, mask)
		if nh == -1 {
			return path, nil, true
		}
		cur = nh
	}
}

// DestEvidence summarizes reachability/loop detection for one destination.
type DestEvidence struct {
	Dest  int
	OK    bool
	Loops [][]int // detected forwarding loops
	Stuck [][]int // paths ending at a blackhole
	Paths [][]int // successful paths src -> ... -> dest
}

// StateEvidence is the full per-destination evidence for one state.
type StateEvidence struct {
	Mask  uint32
	Dests []DestEvidence
}

// Check evaluates state mask: safe iff every source reaches every destination
// with no forwarding loop and no blackhole.
func (p *Planner) Check(mask uint32) (bool, *StateEvidence) {
	ev := &StateEvidence{Mask: mask}
	safe := true
	for d := 0; d < p.n; d++ {
		de := DestEvidence{Dest: d, OK: true}
		for s := 0; s < p.n; s++ {
			if s == d {
				continue
			}
			path, cycle, stuck := p.Trace(mask, d, s)
			switch {
			case cycle != nil:
				de.OK = false
				de.Loops = append(de.Loops, cycle)
			case stuck:
				de.OK = false
				de.Stuck = append(de.Stuck, path)
			default:
				de.Paths = append(de.Paths, path)
			}
		}
		if !de.OK {
			safe = false
		}
		ev.Dests = append(ev.Dests, de)
	}
	return safe, ev
}

// fullMask has a bit set for every node (used for the all-new state).
// checkFast is an allocation-free safety check used inside search loops.
// Semantics are identical to Check but no evidence is collected.
func (p *Planner) checkFast(mask uint32) bool {
	var stamp [18]int
	gen := 0
	for d := 0; d < p.n; d++ {
		for s := 0; s < p.n; s++ {
			if s == d {
				continue
			}
			gen++
			cur := s
			for cur != d {
				if cur == -1 {
					return false // blackhole
				}
				if stamp[cur] == gen {
					return false // forwarding loop
				}
				stamp[cur] = gen
				cur = p.nextHop(cur, d, mask)
			}
		}
	}
	return true
}

func (p *Planner) fullMask() uint32 {
	return (uint32(1) << uint(p.n)) - 1
}

// needMask has a bit set for every node that actually needs an update.
func (p *Planner) needMask() uint32 {
	var m uint32
	for _, v := range p.diff {
		m |= uint32(1) << uint(v)
	}
	return m
}

// Plan is a sequence of synchronized update groups; each group is applied
// atomically, groups are applied in order.
type Plan struct {
	Groups  [][]int  // nodes updated together in each step
	States  []uint32 // state mask after each step
	MaxSize int      // largest group size (the "synchronization cost")
}

// FindPlan computes an update plan minimizing the largest synchronized group
// size (primary) and the number of groups (secondary). A plan with MaxSize==1
// is a safe per-node update order. Feasibility is guaranteed whenever the
// all-new state is safe (apply everything in one group as a fallback).
func (p *Planner) FindPlan() (Plan, bool) {
	k := len(p.diff)
	if k == 0 {
		return Plan{}, true
	}
	if order, states, ok := p.FindOrder(); ok {
		groups := make([][]int, len(order))
		for i, v := range order {
			groups[i] = []int{v}
		}
		return Plan{Groups: groups, States: states, MaxSize: 1}, true
	}
	for s := 2; s <= k; s++ {
		if groups, states, ok := p.bfsLimited(s); ok {
			return Plan{Groups: groups, States: states, MaxSize: s}, true
		}
	}
	return Plan{}, false
}

// FindOrder returns a safe per-node update order if one exists. It uses DFS
// with a dead-state memo, which finds an order quickly without enumerating
// the whole safe-state space.
func (p *Planner) FindOrder() ([]int, []uint32, bool) {
	if len(p.diff) == 0 {
		return nil, nil, true
	}
	need := p.needMask()
	dead := make(map[uint32]bool)
	var search func(mask uint32) ([]int, []uint32, bool)
	search = func(mask uint32) ([]int, []uint32, bool) {
		if mask == need {
			return []int{}, []uint32{}, true
		}
		if dead[mask] {
			return nil, nil, false
		}
		for _, v := range p.diff {
			bit := uint32(1) << uint(v)
			if mask&bit != 0 {
				continue
			}
			nm := mask | bit
			if !p.checkFast(nm) {
				continue
			}
			if order, states, ok := search(nm); ok {
				return append([]int{v}, order...), append([]uint32{nm}, states...), true
			}
		}
		dead[mask] = true
		return nil, nil, false
	}
	return search(0)
}

// bfsLimited searches for a plan where every synchronized group has size at
// most maxSize. BFS level order yields the minimum number of groups.
func (p *Planner) bfsLimited(maxSize int) ([][]int, []uint32, bool) {
	full := p.needMask()
	prev := map[uint32]uint32{0: 0} // state -> previous state
	move := map[uint32]uint32{0: 0} // state -> subset applied in the move
	queue := []uint32{0}
	for len(queue) > 0 {
		mask := queue[0]
		queue = queue[1:]
		rem := full &^ mask
		for _, sub := range subsetsLimited(rem, maxSize) {
			nm := mask | sub
			if _, seen := prev[nm]; seen {
				continue
			}
			if !p.checkFast(nm) {
				continue
			}
			prev[nm] = mask
			move[nm] = sub
			if nm == full {
				return p.reconstruct(full, prev, move)
			}
			queue = append(queue, nm)
		}
	}
	return nil, nil, false
}

func (p *Planner) reconstruct(goal uint32, prev, move map[uint32]uint32) ([][]int, []uint32, bool) {
	var groups [][]int
	var states []uint32
	for m := goal; m != 0; {
		sub := move[m]
		var group []int
		for i := 0; i < p.n; i++ {
			if sub&(uint32(1)<<uint(i)) != 0 {
				group = append(group, i)
			}
		}
		groups = append([][]int{group}, groups...)
		states = append([]uint32{m}, states...)
		m = prev[m]
	}
	return groups, states, true
}

// subsetsLimited returns all non-empty subsets of mask with popcount at most
// maxSize, ordered by decreasing popcount so BFS tends to reach the goal
// quickly.
func subsetsLimited(mask uint32, maxSize int) []uint32 {
	var out []uint32
	for sub := mask; sub != 0; sub = (sub - 1) & mask {
		if popcount(sub) <= maxSize {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ci, cj := popcount(out[i]), popcount(out[j])
		if ci != cj {
			return ci > cj
		}
		return out[i] < out[j]
	})
	return out
}

func popcount(v uint32) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}

// --- evidence formatting ---

func pathStr(path []int) string {
	parts := make([]string, len(path))
	for i, v := range path {
		parts[i] = fmt.Sprintf("n%d", v)
	}
	return strings.Join(parts, " -> ")
}

// FormatEvidence renders per-destination reachability / loop-detection
// evidence for a state. When verbose is false, successful destinations are
// summarized on one line; failures are always detailed.
func (p *Planner) FormatEvidence(ev *StateEvidence, verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  state: updated={%s}\n", p.updatedList(ev.Mask))
	for _, de := range ev.Dests {
		sources := p.n - 1
		if sources < 0 {
			sources = 0
		}
		if de.OK {
			if verbose {
				fmt.Fprintf(&b, "  dst n%d: OK, %d/%d sources reach destination\n", de.Dest, len(de.Paths), sources)
				for _, path := range de.Paths {
					fmt.Fprintf(&b, "    reach: %s\n", pathStr(path))
				}
			} else {
				fmt.Fprintf(&b, "  dst n%d: OK, %d/%d sources reach destination, no loop detected\n", de.Dest, len(de.Paths), sources)
			}
			continue
		}
		fmt.Fprintf(&b, "  dst n%d: UNSAFE\n", de.Dest)
		for _, c := range de.Loops {
			fmt.Fprintf(&b, "    LOOP detected: %s -> (back to n%d)\n", pathStr(c), c[0])
		}
		for _, s := range de.Stuck {
			fmt.Fprintf(&b, "    BLACKHOLE: %s -> no route\n", pathStr(s))
		}
		if verbose {
			for _, path := range de.Paths {
				fmt.Fprintf(&b, "    reach: %s\n", pathStr(path))
			}
		}
	}
	return b.String()
}

func (p *Planner) updatedList(mask uint32) string {
	var parts []string
	for i := 0; i < p.n; i++ {
		if mask&(uint32(1)<<uint(i)) != 0 {
			parts = append(parts, fmt.Sprintf("n%d", i))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
