package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Interval is a closed integer interval [Lo, Hi].
type Interval struct {
	Lo int64
	Hi int64
}

func (iv *Interval) UnmarshalJSON(data []byte) error {
	var pair [2]int64
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("interval must be [lo, hi]: %w", err)
	}
	iv.Lo, iv.Hi = pair[0], pair[1]
	return nil
}

func (iv Interval) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]int64{iv.Lo, iv.Hi})
}

// Candidate is a stabbing point with a non-negative cost.
type Candidate struct {
	Point int64 `json:"point"`
	Cost  int64 `json:"cost"`
}

// Input is the JSON problem description.
type Input struct {
	Intervals  []Interval  `json:"intervals"`
	Candidates []Candidate `json:"candidates"`
}

// problem is the normalized, solver-ready form of an Input.
type problem struct {
	intervals  []Interval // sorted by (Hi, Lo)
	origIdx    []int      // origIdx[i] = index of intervals[i] in the input
	points     []int64    // distinct, useful candidate points, ascending
	costs      []int64    // costs[j] = cost of points[j]
	cover      [][]uint64 // cover[j] = bitmask of intervals stabbed by points[j]
	candOf     [][]int    // candOf[i] = point indices stabbing intervals[i], by (cost, point)
	minCost    []int64    // minCost[i] = cheapest candidate stabbing intervals[i]
	nWords     int        // number of uint64 words per coverage mask
	infeasible []int      // original indices of intervals with no candidate
	dropped    int        // candidates dropped because they stab nothing
}

func setBit(w []uint64, i int) { w[i>>6] |= 1 << (uint(i) & 63) }

func testBit(w []uint64, i int) bool { return w[i>>6]>>(uint(i)&63)&1 == 1 }

func orInto(dst, src []uint64) {
	for i := range dst {
		dst[i] |= src[i]
	}
}

func equalBits(a, b []uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prepare validates the input, merges duplicate points (keeping the cheapest
// cost), sorts intervals by right endpoint, drops useless candidates, and
// precomputes coverage structures. Intervals containing no candidate point
// are collected as infeasibility evidence.
func prepare(in *Input) (*problem, error) {
	for i, iv := range in.Intervals {
		if iv.Lo > iv.Hi {
			return nil, fmt.Errorf("interval %d: lo > hi (%d > %d)", i, iv.Lo, iv.Hi)
		}
	}
	minCostByPoint := map[int64]int64{}
	for _, c := range in.Candidates {
		if c.Cost < 0 {
			return nil, fmt.Errorf("candidate at point %d: negative cost %d", c.Point, c.Cost)
		}
		if old, ok := minCostByPoint[c.Point]; !ok || c.Cost < old {
			minCostByPoint[c.Point] = c.Cost
		}
	}
	pts := make([]int64, 0, len(minCostByPoint))
	for pt := range minCostByPoint {
		pts = append(pts, pt)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })

	n := len(in.Intervals)
	p := &problem{nWords: (n + 63) / 64}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		ia, ib := in.Intervals[order[a]], in.Intervals[order[b]]
		if ia.Hi != ib.Hi {
			return ia.Hi < ib.Hi
		}
		return ia.Lo < ib.Lo
	})
	p.intervals = make([]Interval, n)
	p.origIdx = make([]int, n)
	for i, oi := range order {
		p.intervals[i] = in.Intervals[oi]
		p.origIdx[i] = oi
	}

	for _, pt := range pts {
		mask := make([]uint64, p.nWords)
		for i, iv := range p.intervals {
			if iv.Lo <= pt && pt <= iv.Hi {
				setBit(mask, i)
			}
		}
		useful := false
		for _, w := range mask {
			if w != 0 {
				useful = true
				break
			}
		}
		if !useful {
			p.dropped++
			continue
		}
		p.points = append(p.points, pt)
		p.costs = append(p.costs, minCostByPoint[pt])
		p.cover = append(p.cover, mask)
	}

	p.candOf = make([][]int, n)
	p.minCost = make([]int64, n)
	for i := 0; i < n; i++ {
		iv := p.intervals[i]
		for j, pt := range p.points {
			if iv.Lo <= pt && pt <= iv.Hi {
				p.candOf[i] = append(p.candOf[i], j)
			}
		}
		sort.Slice(p.candOf[i], func(a, b int) bool {
			ca, cb := p.costs[p.candOf[i][a]], p.costs[p.candOf[i][b]]
			if ca != cb {
				return ca < cb
			}
			return p.points[p.candOf[i][a]] < p.points[p.candOf[i][b]]
		})
		if len(p.candOf[i]) == 0 {
			p.infeasible = append(p.infeasible, p.origIdx[i])
		} else {
			p.minCost[i] = p.costs[p.candOf[i][0]]
		}
	}
	sort.Ints(p.infeasible)
	return p, nil
}
