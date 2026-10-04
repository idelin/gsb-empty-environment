// Package intervalstat maintains a dynamic multiset of half-open integer
// intervals [start, end) and answers coverage-length queries: the total
// covered length and the length covered by at least K intervals.
//
// Coordinates are arbitrary int64 values (negative and very large values
// included). The implementation keeps a sweep-line delta map keyed by
// endpoint, so memory and query cost depend on the number of distinct
// endpoints, never on the coordinate range.
package intervalstat

import (
	"errors"
	"math/bits"
	"sort"
)

var (
	// ErrInvalidInterval is returned when start >= end.
	ErrInvalidInterval = errors.New("intervalstat: invalid interval, require start < end")
	// ErrIDNotFound is returned by Remove when the id has no live copy.
	ErrIDNotFound = errors.New("intervalstat: id has no interval to remove")
	// ErrInvalidK is returned when K < 1.
	ErrInvalidK = errors.New("intervalstat: k must be >= 1")
	// ErrOverflow is returned if a length computation overflows uint64.
	// It cannot trigger for disjoint segments inside the int64 range, but
	// every addition is checked defensively.
	ErrOverflow = errors.New("intervalstat: arithmetic overflow")
)

type interval struct {
	start, end int64
}

// Counter is a dynamic multiset of half-open intervals keyed by id.
// The zero value is ready to use.
type Counter struct {
	deltas map[int64]int64       // endpoint -> net (starts - ends)
	active map[string][]interval // id -> stack of live copies
	pts    []int64               // sorted distinct endpoints, valid when !dirty
	cover  []int64               // cover[i] = intervals covering [pts[i], pts[i+1])
	dirty  bool
}

// New returns an empty Counter.
func New() *Counter {
	return &Counter{
		deltas: make(map[int64]int64),
		active: make(map[string][]interval),
	}
}

// Add inserts one copy of [start, end) under id. The same id may be added
// any number of times; each Add is an independent copy.
func (c *Counter) Add(id string, start, end int64) error {
	if start >= end {
		return ErrInvalidInterval
	}
	c.lazyInit()
	c.bump(start, 1)
	c.bump(end, -1)
	c.active[id] = append(c.active[id], interval{start, end})
	c.dirty = true
	return nil
}

// Remove deletes the most recently added live copy of id. Removing an id
// with no live copy is a no-op that reports ErrIDNotFound and leaves all
// statistics unchanged.
func (c *Counter) Remove(id string) error {
	c.lazyInit()
	list := c.active[id]
	if len(list) == 0 {
		return ErrIDNotFound
	}
	iv := list[len(list)-1]
	list = list[:len(list)-1]
	if len(list) == 0 {
		delete(c.active, id)
	} else {
		c.active[id] = list
	}
	c.bump(iv.start, -1)
	c.bump(iv.end, 1)
	c.dirty = true
	return nil
}

// TotalCovered returns the length of the union of all live intervals.
func (c *Counter) TotalCovered() (uint64, error) {
	return c.coveredAtLeast(1)
}

// CoveredAtLeast returns the length covered by at least k live intervals.
func (c *Counter) CoveredAtLeast(k int) (uint64, error) {
	if k < 1 {
		return 0, ErrInvalidK
	}
	return c.coveredAtLeast(int64(k))
}

// Len reports the number of live interval copies.
func (c *Counter) Len() int {
	n := 0
	for _, list := range c.active {
		n += len(list)
	}
	return n
}

func (c *Counter) lazyInit() {
	if c.deltas == nil {
		c.deltas = make(map[int64]int64)
	}
	if c.active == nil {
		c.active = make(map[string][]interval)
	}
}

func (c *Counter) bump(coord int64, d int64) {
	v := c.deltas[coord] + d
	if v == 0 {
		delete(c.deltas, coord)
	} else {
		c.deltas[coord] = v
	}
}

// rebuild recomputes the sorted endpoint list and per-segment coverage
// counts after any mutation.
func (c *Counter) rebuild() {
	if !c.dirty {
		return
	}
	c.pts = c.pts[:0]
	for p := range c.deltas {
		c.pts = append(c.pts, p)
	}
	sort.Slice(c.pts, func(i, j int) bool { return c.pts[i] < c.pts[j] })
	if cap(c.cover) < len(c.pts) {
		c.cover = make([]int64, len(c.pts))
	}
	c.cover = c.cover[:len(c.pts)]
	var run int64
	for i, p := range c.pts {
		run += c.deltas[p]
		c.cover[i] = run
	}
	c.dirty = false
}

func (c *Counter) coveredAtLeast(k int64) (uint64, error) {
	c.lazyInit()
	c.rebuild()
	var total uint64
	for i := 0; i+1 < len(c.pts); i++ {
		if c.cover[i] < k {
			continue
		}
		// pts[i+1] > pts[i], so the unsigned difference is exact even
		// when the segment straddles the int64 sign boundary.
		seg := uint64(c.pts[i+1]) - uint64(c.pts[i])
		sum, carry := bits.Add64(total, seg, 0)
		if carry != 0 {
			return 0, ErrOverflow
		}
		total = sum
	}
	return total, nil
}
