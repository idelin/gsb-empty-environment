// Package intervalstat maintains a dynamic multiset of half-open integer
// intervals [start, end) and answers coverage-length queries: the total
// covered length, and the length covered by at least K intervals.
//
// Coordinates may be negative and arbitrarily large (any int64); the
// implementation works on endpoint events only and never expands
// coordinates into per-unit storage. All length arithmetic is done with
// math/big so results cannot overflow.
package intervalstat

import (
	"errors"
	"math"
	"math/big"
	"sort"
)

// ErrInvalidInterval is returned when start >= end.
var ErrInvalidInterval = errors.New("intervalstat: start must be < end")

// ErrCountOverflow is returned when an internal counter would overflow int64.
var ErrCountOverflow = errors.New("intervalstat: counter overflow")

type interval struct {
	start int64
	end   int64
}

// Counter is a dynamic multiset of half-open intervals keyed by ID.
// The zero value is ready to use.
type Counter struct {
	deltas map[int64]int64               // endpoint -> net (starts - ends)
	points []int64                       // sorted distinct endpoints
	copies map[string]map[interval]int64 // id -> interval -> live copy count
	prefix []int64                       // prefix sums of deltas over points
	dirty  bool                          // prefix needs recomputation
}

// New returns an empty Counter.
func New() *Counter {
	return &Counter{
		deltas: make(map[int64]int64),
		copies: make(map[string]map[interval]int64),
	}
}

// Add inserts one copy of [start, end) under id. The same (id, interval)
// pair may be added multiple times; each copy must be removed separately.
// It returns ErrInvalidInterval if start >= end and leaves the Counter
// unchanged on any error.
func (c *Counter) Add(id string, start, end int64) error {
	if start >= end {
		return ErrInvalidInterval
	}
	if c.deltas == nil {
		c.deltas = make(map[int64]int64)
	}
	if c.copies == nil {
		c.copies = make(map[string]map[interval]int64)
	}
	m := c.copies[id]
	if m == nil {
		m = make(map[interval]int64)
		c.copies[id] = m
	}
	iv := interval{start, end}
	if m[iv] == math.MaxInt64 {
		return ErrCountOverflow
	}
	if err := c.bump(start, 1); err != nil {
		return err
	}
	if err := c.bump(end, -1); err != nil {
		_ = c.bump(start, -1) // roll back; cannot fail
		return err
	}
	m[iv]++
	return nil
}

// Remove deletes one copy of [start, end) previously added under id.
// If no such copy exists (unknown id, unknown interval, start >= end),
// Remove is a no-op and the Counter is left unchanged.
func (c *Counter) Remove(id string, start, end int64) {
	if start >= end {
		return
	}
	m := c.copies[id]
	if m == nil {
		return
	}
	iv := interval{start, end}
	n := m[iv]
	if n == 0 {
		return
	}
	// The mirrored bumps below cannot overflow: they undo a previous Add.
	_ = c.bump(start, -1)
	_ = c.bump(end, 1)
	if n == 1 {
		delete(m, iv)
		if len(m) == 0 {
			delete(c.copies, id)
		}
	} else {
		m[iv] = n - 1
	}
}

// bump adjusts the sweep-line delta at x by d, checking for overflow.
func (c *Counter) bump(x int64, d int64) error {
	cur, ok := c.deltas[x]
	if d > 0 && cur > math.MaxInt64-d {
		return ErrCountOverflow
	}
	if d < 0 && cur < math.MinInt64-d {
		return ErrCountOverflow
	}
	if !ok {
		c.insertPoint(x)
	}
	c.deltas[x] = cur + d
	c.dirty = true
	return nil
}

// insertPoint inserts x into the sorted points slice.
func (c *Counter) insertPoint(x int64) {
	i := sort.Search(len(c.points), func(i int) bool { return c.points[i] >= x })
	c.points = append(c.points, 0)
	copy(c.points[i+1:], c.points[i:])
	c.points[i] = x
}

// refresh recomputes prefix sums of deltas over the sorted endpoints.
// prefix[i] is the number of live intervals covering [points[i], points[i+1]).
func (c *Counter) refresh() {
	if !c.dirty && c.prefix != nil {
		return
	}
	if cap(c.prefix) < len(c.points) {
		c.prefix = make([]int64, len(c.points))
	} else {
		c.prefix = c.prefix[:len(c.points)]
	}
	var sum int64
	for i, p := range c.points {
		// Cannot overflow: |sum| is bounded by the total number of live
		// copies, each of which was overflow-checked at Add time.
		sum += c.deltas[p]
		c.prefix[i] = sum
	}
	c.dirty = false
}

// CoveredAtLeast returns the total length covered by at least k live
// intervals. k < 1 is treated as 1. The result is a *big.Int because the
// answer can exceed int64 (e.g. an interval spanning nearly the whole
// int64 range).
func (c *Counter) CoveredAtLeast(k int64) *big.Int {
	if k < 1 {
		k = 1
	}
	c.refresh()
	total := new(big.Int)
	for i := 0; i+1 < len(c.points); i++ {
		if c.prefix[i] >= k {
			lo := big.NewInt(c.points[i])
			hi := big.NewInt(c.points[i+1])
			total.Add(total, hi.Sub(hi, lo))
		}
	}
	return total
}

// TotalCovered returns the total length covered by at least one interval.
func (c *Counter) TotalCovered() *big.Int {
	return c.CoveredAtLeast(1)
}

// LiveCopies reports how many live copies of [start, end) exist under id.
func (c *Counter) LiveCopies(id string, start, end int64) int64 {
	return c.copies[id][interval{start, end}]
}
