// Package main implements a grayscale (canary) rollout allocator.
//
// Problem: given up to 20 user groups (each indivisible, currently on the
// old or new version), assign every group wholly to one version so that the
// number of users on the new version falls inside an exact interval
// [lo, hi], while migrating as few users as possible (tie-break: fewer
// moved groups). If no whole-group assignment can hit the interval, produce
// a verifiable infeasibility proof instead of splitting groups.
//
// Method: meet-in-the-middle. Enumerate both halves (2^(n/2) each), sort one
// half by new-version user count, and answer "min migration cost with sum in
// [lo-s, hi-s]" for every sum s of the other half via a sparse table RMQ.
// Exact and fast: O(2^(n/2) * n) time, n <= 20.
package main

import (
	"errors"
	"fmt"
	"sort"
)

// MaxGroups is the hard limit on the number of user groups.
const MaxGroups = 20

// Group is an indivisible set of users currently on one version.
type Group struct {
	ID    string `json:"id"`
	Size  int64  `json:"size"`
	OnNew bool   `json:"on_new"` // currently on the new version
}

// Assignment is one feasible solution.
type Assignment struct {
	OnNew       []bool // per-group decision, aligned with the input order
	NewUsers    int64  // users on the new version
	MovedUsers  int64  // users whose group changed version
	MovedGroups int    // groups whose version changed
}

// Proof certifies that no whole-group assignment lands inside [Lo, Hi].
type Proof struct {
	Lo, Hi int64
	// Largest reachable new-version user count strictly below Lo (-1 if none).
	Below int64
	// Smallest reachable new-version user count strictly above Hi (-1 if none).
	Above int64
	// GCD of all group sizes when > 1: every reachable count is a multiple
	// of it, which alone proves infeasibility when the interval contains
	// no such multiple. 1 when the modular argument does not apply.
	GCD int64
}

// Describe renders the proof as a human-readable explanation.
func (p Proof) Describe() string {
	s := fmt.Sprintf("no whole-group assignment reaches the exact interval [%d, %d]", p.Lo, p.Hi)
	switch {
	case p.Below >= 0 && p.Above >= 0:
		s += fmt.Sprintf(": reachable new-version user counts jump from %d to %d, skipping the entire interval", p.Below, p.Above)
	case p.Below >= 0:
		s += fmt.Sprintf(": the largest reachable count is %d, below the interval", p.Below)
	case p.Above >= 0:
		s += fmt.Sprintf(": the smallest reachable count is %d, above the interval", p.Above)
	default:
		s += ": no users exist at all"
	}
	if p.GCD > 1 {
		s += fmt.Sprintf("; additionally every reachable count is a multiple of %d (gcd of all group sizes)", p.GCD)
	}
	return s
}

// entry is one enumerated half-assignment.
type entry struct {
	sum   int64  // new-version users contributed by this half
	cost  int64  // users migrated by this half
	moved int    // groups migrated by this half
	mask  uint32 // bit i set => half group i assigned to the new version
}

// better reports whether a strictly beats b: fewer migrated users, then
// fewer migrated groups, then the smaller mask for determinism.
func better(a, b entry) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.moved != b.moved {
		return a.moved < b.moved
	}
	return a.mask < b.mask
}

// enumerate lists all 2^count assignments of groups[offset:offset+count].
func enumerate(groups []Group, offset, count int) []entry {
	es := make([]entry, 1<<uint(count))
	for m := range es {
		e := entry{mask: uint32(m)}
		for i := 0; i < count; i++ {
			g := groups[offset+i]
			onNew := (uint32(m)>>uint(i))&1 == 1
			if onNew {
				e.sum += g.Size
			}
			if onNew != g.OnNew {
				e.cost += g.Size
				e.moved++
			}
		}
		es[m] = e
	}
	return es
}

// dedupe sorts by sum and keeps the best entry per reachable sum.
func dedupe(es []entry) []entry {
	sort.Slice(es, func(i, j int) bool {
		if es[i].sum != es[j].sum {
			return es[i].sum < es[j].sum
		}
		return better(es[i], es[j])
	})
	out := es[:0]
	for _, e := range es {
		if n := len(out); n > 0 && out[n-1].sum == e.sum {
			if better(e, out[n-1]) {
				out[n-1] = e
			}
		} else {
			out = append(out, e)
		}
	}
	return out
}

// rmq answers range-minimum queries (by better) over a static slice.
type rmq struct {
	vals  []entry
	logs  []int
	table [][]int // table[k][i] = index of best entry in vals[i:i+2^k]
}

func newRMQ(vals []entry) *rmq {
	n := len(vals)
	r := &rmq{vals: vals, logs: make([]int, n+1)}
	for i := 2; i <= n; i++ {
		r.logs[i] = r.logs[i/2] + 1
	}
	levels := r.logs[n] + 1
	r.table = make([][]int, levels)
	r.table[0] = make([]int, n)
	for i := 0; i < n; i++ {
		r.table[0][i] = i
	}
	for k := 1; k < levels; k++ {
		span := 1 << uint(k)
		row := make([]int, n-span+1)
		half := span >> 1
		for i := range row {
			a, b := r.table[k-1][i], r.table[k-1][i+half]
			if better(vals[b], vals[a]) {
				a = b
			}
			row[i] = a
		}
		r.table[k] = row
	}
	return r
}

// query returns the best entry in vals[l..r] inclusive; l must be <= r.
func (r *rmq) query(l, rr int) entry {
	k := r.logs[rr-l+1]
	a := r.table[k][l]
	b := r.table[k][rr-(1<<uint(k))+1]
	if better(r.vals[b], r.vals[a]) {
		return r.vals[b]
	}
	return r.vals[a]
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Solve finds the minimum-migration whole-group assignment whose new-version
// user count lies in [lo, hi]. On success it returns the assignment and a nil
// proof; on failure it returns a nil assignment and a proof of infeasibility.
func Solve(groups []Group, lo, hi int64) (*Assignment, *Proof, error) {
	if len(groups) == 0 {
		return nil, nil, errors.New("no groups given")
	}
	if len(groups) > MaxGroups {
		return nil, nil, fmt.Errorf("too many groups: %d > %d", len(groups), MaxGroups)
	}
	if lo > hi {
		return nil, nil, fmt.Errorf("empty interval: lo=%d > hi=%d", lo, hi)
	}
	var total int64
	g := int64(0)
	for i, gr := range groups {
		if gr.Size <= 0 {
			return nil, nil, fmt.Errorf("group %q has non-positive size %d", gr.ID, gr.Size)
		}
		if gr.ID == "" {
			return nil, nil, fmt.Errorf("group %d has empty id", i)
		}
		total += gr.Size
		g = gcd(g, gr.Size)
	}
	// Clamp the interval to what is physically possible.
	if lo < 0 {
		lo = 0
	}
	if hi > total {
		hi = total
	}

	half := len(groups) / 2
	left := dedupe(enumerate(groups, 0, half))
	right := dedupe(enumerate(groups, half, len(groups)-half))
	table := newRMQ(right)

	found := false
	var best, bestL, bestR entry
	for _, l := range left {
		// Need right sum r with lo <= l.sum+r <= hi.
		rl := sort.Search(len(right), func(i int) bool { return right[i].sum >= lo-l.sum })
		rr := sort.Search(len(right), func(i int) bool { return right[i].sum > hi-l.sum }) - 1
		if rl > rr {
			continue
		}
		cand := table.query(rl, rr)
		tot := entry{
			sum:   l.sum + cand.sum,
			cost:  l.cost + cand.cost,
			moved: l.moved + cand.moved,
			mask:  l.mask | cand.mask<<uint(half),
		}
		if !found || better(tot, best) {
			found, best, bestL, bestR = true, tot, l, cand
		}
	}

	if found {
		a := &Assignment{
			OnNew:       make([]bool, len(groups)),
			NewUsers:    bestL.sum + bestR.sum,
			MovedUsers:  bestL.cost + bestR.cost,
			MovedGroups: bestL.moved + bestR.moved,
		}
		for i := 0; i < half; i++ {
			a.OnNew[i] = (bestL.mask>>uint(i))&1 == 1
		}
		for i := half; i < len(groups); i++ {
			a.OnNew[i] = (bestR.mask>>uint(i-half))&1 == 1
		}
		return a, nil, nil
	}

	// Infeasible: build the gap certificate. For every reachable left sum,
	// the closest right sums just outside the interval give the tightest
	// reachable counts below lo and above hi.
	proof := &Proof{Lo: lo, Hi: hi, Below: -1, Above: -1, GCD: g}
	for _, l := range left {
		i := sort.Search(len(right), func(i int) bool { return right[i].sum >= lo-l.sum })
		if i > 0 { // right[i-1].sum < lo-l.sum => combined count below lo
			if v := l.sum + right[i-1].sum; v > proof.Below {
				proof.Below = v
			}
		}
		j := sort.Search(len(right), func(i int) bool { return right[i].sum > hi-l.sum })
		if j < len(right) { // right[j].sum > hi-l.sum => combined count above hi
			if v := l.sum + right[j].sum; proof.Above < 0 || v < proof.Above {
				proof.Above = v
			}
		}
	}
	return nil, proof, nil
}
