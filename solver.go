package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"sort"
)

// Point is an integer lattice point. In JSON it may be written either as
// [x, y] or as {"x": x, "y": y}; it is always marshaled as [x, y].
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// UnmarshalJSON accepts [x, y] or {"x": x, "y": y}.
func (p *Point) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var pair [2]int
		if err := json.Unmarshal(data, &pair); err != nil {
			return err
		}
		p.X, p.Y = pair[0], pair[1]
		return nil
	}
	var obj struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	p.X, p.Y = obj.X, obj.Y
	return nil
}

// MarshalJSON emits [x, y].
func (p Point) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]int{p.X, p.Y})
}

// Rect is an axis-aligned rectangle over lattice points, inclusive on all sides.
type Rect struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

// Area is the number of lattice points inside the rectangle.
func (r Rect) Area() int64 {
	return int64(r.X2-r.X1+1) * int64(r.Y2-r.Y1+1)
}

func (r Rect) Contains(p Point) bool {
	return p.X >= r.X1 && p.X <= r.X2 && p.Y >= r.Y1 && p.Y <= r.Y2
}

// Cost is the lexicographic objective: minimize Area first, then Count.
// Overlapping rectangles are charged repeatedly (sum of individual areas).
type Cost struct {
	Area  int64
	Count int64
}

func lessCost(a, b Cost) bool {
	if a.Area != b.Area {
		return a.Area < b.Area
	}
	return a.Count < b.Count
}

// ---- bitset over target indices ----

type bitset struct {
	w []uint64
}

func newBitset(n int) bitset {
	return bitset{w: make([]uint64, (n+63)/64)}
}

func (b bitset) set(i int) {
	b.w[i>>6] |= 1 << (uint(i) & 63)
}

func (b bitset) test(i int) bool {
	return b.w[i>>6]&(1<<(uint(i)&63)) != 0
}

func (b bitset) equal(o bitset) bool {
	if len(b.w) != len(o.w) {
		return false
	}
	for i := range b.w {
		if b.w[i] != o.w[i] {
			return false
		}
	}
	return true
}

func (b bitset) key() string {
	buf := make([]byte, 8*len(b.w))
	for i, x := range b.w {
		binary.LittleEndian.PutUint64(buf[i*8:], x)
	}
	return string(buf)
}

// ---- candidates ----

type candidate struct {
	rect  Rect
	cover bitset // targets covered
	area  int64
}

// enumerateCandidates lists every axis-aligned lattice rectangle that contains
// at least one target and no forbidden point. An optimal rectangle can always
// be shrunk until each side passes through a target coordinate (shrinking never
// introduces a forbidden point), so only target x/y coordinates are considered
// as rectangle sides.
func enumerateCandidates(targets, forbidden []Point) []candidate {
	xs := uniqueSortedCoords(targets, func(p Point) int { return p.X })
	ys := uniqueSortedCoords(targets, func(p Point) int { return p.Y })

	var out []candidate
	for xi := 0; xi < len(xs); xi++ {
		for xj := xi; xj < len(xs); xj++ {
			for yi := 0; yi < len(ys); yi++ {
				for yj := yi; yj < len(ys); yj++ {
					r := Rect{X1: xs[xi], Y1: ys[yi], X2: xs[xj], Y2: ys[yj]}
					if rectHitsAny(r, forbidden) {
						continue
					}
					cov := newBitset(len(targets))
					any := false
					for i, t := range targets {
						if r.Contains(t) {
							cov.set(i)
							any = true
						}
					}
					if !any {
						continue
					}
					out = append(out, candidate{rect: r, cover: cov, area: r.Area()})
				}
			}
		}
	}
	return out
}

func uniqueSortedCoords(pts []Point, f func(Point) int) []int {
	seen := make(map[int]bool)
	var vals []int
	for _, p := range pts {
		v := f(p)
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	sort.Ints(vals)
	return vals
}

func rectHitsAny(r Rect, pts []Point) bool {
	for _, p := range pts {
		if r.Contains(p) {
			return true
		}
	}
	return false
}

// dedupeCandidates keeps, for each distinct coverage set, only a minimum-area
// rectangle. Any solution using a larger rectangle with identical coverage can
// swap in the smaller one without worsening the objective.
func dedupeCandidates(cands []candidate) []candidate {
	best := make(map[string]int) // coverage key -> index into out
	var out []candidate
	for _, c := range cands {
		k := c.cover.key()
		if idx, ok := best[k]; !ok {
			best[k] = len(out)
			out = append(out, c)
		} else if c.area < out[idx].area {
			out[idx] = c
		}
	}
	return out
}

// pruneDominated removes candidate i when some other candidate j covers a
// superset of i's targets with area <= i's area (strictly better in at least
// one respect). O(C^2 * words); only invoked for small candidate sets.
func pruneDominated(cands []candidate) []candidate {
	n := len(cands)
	dominated := make([]bool, n)
	for i := 0; i < n; i++ {
		if dominated[i] {
			continue
		}
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if cands[j].area > cands[i].area {
				continue
			}
			superset := true
			for w := range cands[i].cover.w {
				if cands[i].cover.w[w]&^cands[j].cover.w[w] != 0 {
					superset = false
					break
				}
			}
			if !superset {
				continue
			}
			strict := cands[j].area < cands[i].area
			if !strict {
				for w := range cands[i].cover.w {
					if cands[j].cover.w[w]&^cands[i].cover.w[w] != 0 {
						strict = true
						break
					}
				}
			}
			if strict {
				dominated[i] = true
				break
			}
		}
	}
	var out []candidate
	for i, c := range cands {
		if !dominated[i] {
			out = append(out, c)
		}
	}
	return out
}
