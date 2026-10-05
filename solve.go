package main

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"sort"
)

// ErrInfeasible means no placement satisfies capacity and CoG constraints.
var ErrInfeasible = errors.New("infeasible: no placement satisfies the capacity and center-of-gravity constraints")

// errNodeLimit aborts the search when the node budget is exhausted.
type errNodeLimit struct{ nodes int }

func (e errNodeLimit) Error() string {
	return fmt.Sprintf("search node limit exceeded (%d); raise max_nodes or simplify the instance", e.nodes)
}

// binInfo records, for one feasible item subset (bitmask over solver item
// order), the minimum eccentricity achievable by some slot assignment whose
// center of gravity lies in the safe interval, plus a witness assignment.
type binInfo struct {
	ecc   *big.Rat // min |cog - center| over feasible assignments
	cog   *big.Rat // center of gravity of the witness assignment
	slots []uint8  // witness: slot index per item, items in increasing index order
}

type solver struct {
	items    []Item // sorted by weight desc, then id
	pos      []*big.Rat
	cap      *big.Rat
	lo, hi   *big.Rat
	center   *big.Rat
	n, p     int
	nodes    int
	maxNodes int
	prod     [][]*big.Rat // prod[i][j] = weight_i * pos_j
	capMul   []*big.Rat   // capMul[b] = capacity * b

	weight   []*big.Rat // weight[mask] = total weight of item set mask
	popcount []int
	bins     []*binInfo // bins[mask] != nil iff mask is a feasible bin content

	partFailed []uint32 // per mask, bitset of dead binsLeft values, per partition round
}

const defaultMaxNodes = 50000000

func newSolver(in *Input) *solver {
	items := append([]Item(nil), in.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		if c := items[i].Weight.Cmp(items[j].Weight); c != 0 {
			return c > 0
		}
		return items[i].ID < items[j].ID
	})
	maxNodes := in.MaxNodes
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	s := &solver{
		items:    items,
		pos:      in.Positions,
		cap:      in.Capacity,
		lo:       in.Lo,
		hi:       in.Hi,
		center:   in.Center,
		n:        len(items),
		p:        len(in.Positions),
		maxNodes: maxNodes,
	}
	size := 1 << uint(s.n)
	s.bins = make([]*binInfo, size)
	s.weight = make([]*big.Rat, size)
	s.popcount = make([]int, size)
	s.weight[0] = new(big.Rat)
	for mask := 1; mask < size; mask++ {
		low := mask & (-mask)
		bit := bits.TrailingZeros32(uint32(mask))
		prev := mask ^ low
		s.weight[mask] = new(big.Rat).Add(s.weight[prev], s.items[bit].Weight)
		s.popcount[mask] = s.popcount[prev] + 1
	}
	s.prod = make([][]*big.Rat, s.n)
	for i := range s.prod {
		s.prod[i] = make([]*big.Rat, s.p)
		for j := range s.prod[i] {
			s.prod[i][j] = new(big.Rat).Mul(s.items[i].Weight, s.pos[j])
		}
	}
	s.capMul = make([]*big.Rat, s.n+1)
	for b := 0; b <= s.n; b++ {
		s.capMul[b] = new(big.Rat).Mul(s.cap, big.NewRat(int64(b), 1))
	}
	return s
}

func (s *solver) tick() {
	s.nodes++
	if s.nodes > s.maxNodes {
		panic(errNodeLimit{nodes: s.nodes})
	}
}

// Solve finds a placement using the fewest bins and, among those, one with the
// minimum possible maximum eccentricity. All constraint checks use exact
// rational arithmetic; no floating-point tolerance is involved.
func Solve(in *Input) (sol *Solution, retErr error) {
	s := newSolver(in)
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(errNodeLimit); ok {
				retErr = e
				return
			}
			panic(r)
		}
	}()

	if s.n == 0 {
		return &Solution{
			Status:                 "optimal",
			BinsUsed:               0,
			MaxEccentricity:        "0",
			MaxEccentricityDecimal: dec(new(big.Rat)),
			Bins:                   []BinReport{},
			Stats:                  Stats{Nodes: s.nodes},
		}, nil
	}

	// Step 1: enumerate every feasible bin content (subset + slot assignment)
	// exactly once, recording the minimum-eccentricity witness per subset.
	s.precompute()

	full := uint32(1<<uint(s.n)) - 1
	total := s.weight[full]
	lb := ceilDivRat(total, s.cap)
	if t := (s.n + s.p - 1) / s.p; t > lb {
		lb = t
	}

	// Step 2: minimize the number of bins (set partition over feasible masks).
	var masks []uint32
	ok := false
	k := lb
	for ; k <= s.n; k++ {
		s.partFailed = make([]uint32, 1<<uint(s.n))
		if masks, ok = s.partition(full, k, nil); ok {
			break
		}
	}
	if !ok {
		return nil, ErrInfeasible
	}

	// Step 3: minimize the maximum eccentricity. A partition with max ecc < L
	// exists iff every bin can be chosen with minEcc < L, and that predicate
	// is monotone in L, so binary search the distinct minEcc values.
	vals := s.sortedEccValues()
	bestIdx := -1
	lo, hi := 0, len(vals)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		s.partFailed = make([]uint32, 1<<uint(s.n))
		if _, ok := s.partition(full, k, vals[mid]); ok {
			bestIdx = mid
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	if bestIdx >= 0 {
		s.partFailed = make([]uint32, 1<<uint(s.n))
		if m, ok := s.partition(full, k, vals[bestIdx]); ok {
			masks = m
		}
	}

	return s.buildSolution(masks), nil
}

// precompute evaluates every candidate item subset (capacity- and size-
// feasible) and solves its exact slot-assignment problem once, keeping the
// feasible assignment of minimum eccentricity per subset.
func (s *solver) precompute() {
	n := s.n
	full := 1 << uint(n)
	wlo := new(big.Rat)
	whi := new(big.Rat)
	wc := new(big.Rat)
	for mask := 1; mask < full; mask++ {
		s.tick()
		m := uint32(mask)
		if s.popcount[m] > s.p {
			continue
		}
		W := s.weight[m]
		if W.Cmp(s.cap) > 0 {
			continue
		}
		var items []int
		for i := 0; i < n; i++ {
			if m&(1<<uint(i)) != 0 {
				items = append(items, i)
			}
		}
		wlo.Mul(W, s.lo)
		whi.Mul(W, s.hi)
		wc.Mul(W, s.center)
		bestM, assign, ok := s.bestAssignment(items, wlo, whi, wc)
		if !ok {
			continue
		}
		ecc := new(big.Rat).Sub(bestM, wc)
		ecc.Abs(ecc)
		ecc.Quo(ecc, W)
		s.bins[m] = &binInfo{
			ecc:   ecc,
			cog:   new(big.Rat).Quo(bestM, W),
			slots: assign,
		}
	}
}

// bestAssignment solves the exact assignment problem for one item subset:
// place the given items (solver indices, weight-descending) on distinct slots
// so that the total moment M lies in [wlo, whi] and |M - wc| is minimized.
// Returns the best moment and the witness slot per item (parallel to items).
//
// Bounds are exact: with items placed heaviest-first, the additional moment
// of any completion lies between the rearrangement extremes of the remaining
// weights against the remaining slots, so branches whose reachable moment
// range misses the window, or cannot beat the incumbent, are pruned.
func (s *solver) bestAssignment(items []int, wlo, whi, wc *big.Rat) (*big.Rat, []uint8, bool) {
	t := len(items)
	var used uint64
	assign := make([]uint8, t)
	var bestAssign []uint8
	var bestDiff *big.Rat
	var bestM *big.Rat
	m := new(big.Rat)
	free := make([]int, 0, s.p)
	// Per-frame scratch, reused across slot iterations to keep the hot loop
	// free of big.Rat allocations.
	loB := new(big.Rat)
	hiB := new(big.Rat)
	d := new(big.Rat)
	diff := new(big.Rat)

	var rec func(i int)
	rec = func(i int) {
		s.tick()
		if i == t {
			if m.Cmp(wlo) >= 0 && m.Cmp(whi) <= 0 {
				diff.Sub(m, wc)
				diff.Abs(diff)
				if bestDiff == nil || diff.Cmp(bestDiff) < 0 {
					bestDiff = new(big.Rat).Set(diff)
					bestM = new(big.Rat).Set(m)
					bestAssign = append([]uint8(nil), assign...)
				}
			}
			return
		}
		it := items[i]
		for pj := 0; pj < s.p; pj++ {
			if used&(1<<uint(pj)) != 0 {
				continue
			}
			// Reachable-moment range after placing item i on slot pj:
			// remaining weights (desc) paired with remaining slots (asc/desc).
			free = free[:0]
			for q := 0; q < s.p; q++ {
				if q != pj && used&(1<<uint(q)) == 0 {
					free = append(free, q)
				}
			}
			loB.Add(m, s.prod[it][pj])
			hiB.Set(loB)
			for k := i + 1; k < t; k++ {
				loB.Add(loB, s.prod[items[k]][free[k-i-1]])
				hiB.Add(hiB, s.prod[items[k]][free[len(free)-1-(k-i-1)]])
			}
			if hiB.Cmp(wlo) < 0 || loB.Cmp(whi) > 0 {
				continue
			}
			if bestDiff != nil {
				d.SetInt64(0)
				switch {
				case wc.Cmp(loB) < 0:
					d.Sub(loB, wc)
				case wc.Cmp(hiB) > 0:
					d.Sub(wc, hiB)
				}
				if d.Cmp(bestDiff) >= 0 {
					continue
				}
			}
			used |= 1 << uint(pj)
			assign[i] = uint8(pj)
			m.Add(m, s.prod[it][pj])
			rec(i + 1)
			m.Sub(m, s.prod[it][pj])
			used &^= 1 << uint(pj)
		}
	}
	rec(0)
	if bestDiff == nil {
		return nil, nil, false
	}
	return bestM, bestAssign, true
}

func (s *solver) partition(mask uint32, binsLeft int, limit *big.Rat) ([]uint32, bool) {
	if mask == 0 {
		return nil, true
	}
	if binsLeft == 0 {
		return nil, false
	}
	s.tick()

	if s.weight[mask].Cmp(s.capMul[binsLeft]) > 0 {
		return nil, false
	}
	if s.popcount[mask] > binsLeft*s.p {
		return nil, false
	}
	if s.partFailed[mask]>>uint(binsLeft)&1 != 0 {
		return nil, false
	}

	low := mask & (-mask)
	rest := mask ^ low
	for sub := rest; ; sub = (sub - 1) & rest {
		b := sub | low
		if info := s.bins[b]; info != nil && (limit == nil || info.ecc.Cmp(limit) < 0) {
			if rem, ok2 := s.partition(mask^b, binsLeft-1, limit); ok2 {
				return append(rem, b), true
			}
		}
		if sub == 0 {
			break
		}
	}
	s.partFailed[mask] |= 1 << uint(binsLeft)
	return nil, false
}

func (s *solver) sortedEccValues() []*big.Rat {
	seen := map[string]bool{}
	var vals []*big.Rat
	for _, info := range s.bins {
		if info == nil {
			continue
		}
		key := info.ecc.RatString()
		if !seen[key] {
			seen[key] = true
			vals = append(vals, info.ecc)
		}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].Cmp(vals[j]) < 0 })
	return vals
}

// ceilDivRat returns ceil(a/b) for positive rationals.
func ceilDivRat(a, b *big.Rat) int {
	num := new(big.Int).Mul(a.Num(), b.Denom())
	den := new(big.Int).Mul(a.Denom(), b.Num())
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return int(q.Int64())
}

func (s *solver) buildSolution(masks []uint32) *Solution {
	sol := &Solution{Status: "optimal", BinsUsed: len(masks), Stats: Stats{Nodes: s.nodes}}
	maxE := new(big.Rat)
	for i, mask := range masks {
		info := s.bins[mask]
		br := BinReport{Index: i}
		j := 0
		for it := 0; it < s.n; it++ {
			if mask&(1<<uint(it)) == 0 {
				continue
			}
			br.Placements = append(br.Placements, Placement{
				Item:     s.items[it].ID,
				Position: s.pos[info.slots[j]].RatString(),
			})
			j++
		}
		br.TotalWeight = s.weight[mask].RatString()
		br.Cog = info.cog.RatString()
		br.CogDecimal = dec(info.cog)
		br.Eccentricity = info.ecc.RatString()
		br.EccentricityDecimal = dec(info.ecc)
		br.WithinSafeInterval = info.cog.Cmp(s.lo) >= 0 && info.cog.Cmp(s.hi) <= 0
		if info.ecc.Cmp(maxE) > 0 {
			maxE.Set(info.ecc)
		}
		sol.Bins = append(sol.Bins, br)
	}
	sol.MaxEccentricity = maxE.RatString()
	sol.MaxEccentricityDecimal = dec(maxE)
	return sol
}
