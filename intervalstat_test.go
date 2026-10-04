package intervalstat

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// refModel is the per-unit reference: cell[x] counts intervals covering
// the unit segment [x, x+1). Only usable on small domains.
type refModel struct {
	cell map[int64]int64
}

func newRefModel() *refModel { return &refModel{cell: make(map[int64]int64)} }

func (r *refModel) add(start, end int64) {
	for x := start; x < end; x++ {
		r.cell[x]++
	}
}

func (r *refModel) remove(start, end int64) {
	for x := start; x < end; x++ {
		r.cell[x]--
		if r.cell[x] == 0 {
			delete(r.cell, x)
		}
	}
}

func (r *refModel) coveredAtLeast(k int64) uint64 {
	var n uint64
	for _, c := range r.cell {
		if c >= k {
			n++
		}
	}
	return n
}

// TestRandomAgainstReference drives random add/remove workloads on a small
// domain and compares every query against the per-unit reference model.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	const (
		domainLo = -16
		domainHi = 16
		rounds   = 40
		ops      = 200
		maxK     = 5
	)
	for round := 0; round < rounds; round++ {
		c := New()
		ref := newRefModel()
		live := make(map[string][]interval) // mirrors Counter semantics
		var ids []string
		for op := 0; op < ops; op++ {
			if rng.Intn(100) < 60 || len(ids) == 0 {
				// add: sometimes reuse an existing id (duplicate copies)
				var id string
				if len(ids) > 0 && rng.Intn(100) < 40 {
					id = ids[rng.Intn(len(ids))]
				} else {
					id = string(rune('a' + rng.Intn(8)))
					ids = append(ids, id)
				}
				a := int64(domainLo + rng.Intn(domainHi-domainLo))
				b := int64(domainLo + rng.Intn(domainHi-domainLo))
				if a == b {
					continue
				}
				if a > b {
					a, b = b, a
				}
				if err := c.Add(id, a, b); err != nil {
					t.Fatalf("Add: %v", err)
				}
				ref.add(a, b)
				live[id] = append(live[id], interval{a, b})
			} else {
				// remove: mostly live ids, sometimes illegal ones
				var id string
				if rng.Intn(100) < 20 {
					id = "no-such-id"
				} else {
					id = ids[rng.Intn(len(ids))]
				}
				err := c.Remove(id)
				list := live[id]
				if len(list) == 0 {
					if !errors.Is(err, ErrIDNotFound) {
						t.Fatalf("illegal remove: got %v, want ErrIDNotFound", err)
					}
					continue // illegal delete must not change anything
				}
				if err != nil {
					t.Fatalf("legal remove: %v", err)
				}
				iv := list[len(list)-1]
				live[id] = list[:len(list)-1]
				ref.remove(iv.start, iv.end)
			}
			got, err := c.TotalCovered()
			if err != nil {
				t.Fatalf("TotalCovered: %v", err)
			}
			if want := ref.coveredAtLeast(1); got != want {
				t.Fatalf("round %d op %d: total = %d, want %d", round, op, got, want)
			}
			for k := 1; k <= maxK; k++ {
				got, err := c.CoveredAtLeast(k)
				if err != nil {
					t.Fatalf("CoveredAtLeast(%d): %v", k, err)
				}
				if want := ref.coveredAtLeast(int64(k)); got != want {
					t.Fatalf("round %d op %d: covered>=%d = %d, want %d", round, op, k, got, want)
				}
			}
		}
	}
}

func mustTotal(t *testing.T, c *Counter) uint64 {
	t.Helper()
	v, err := c.TotalCovered()
	if err != nil {
		t.Fatalf("TotalCovered: %v", err)
	}
	return v
}

func mustK(t *testing.T, c *Counter, k int) uint64 {
	t.Helper()
	v, err := c.CoveredAtLeast(k)
	if err != nil {
		t.Fatalf("CoveredAtLeast(%d): %v", k, err)
	}
	return v
}

func TestAdjacentEndpoints(t *testing.T) {
	c := New()
	// Touching but non-overlapping: [0,5) and [5,10) share only endpoint 5.
	if err := c.Add("a", 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("b", 5, 10); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 10 {
		t.Fatalf("total = %d, want 10", got)
	}
	if got := mustK(t, c, 2); got != 0 {
		t.Fatalf("covered>=2 = %d, want 0", got)
	}
	// Overlapping neighbour: [3,8) overlaps [0,5) on [3,5) and [5,10) on [5,8).
	if err := c.Add("c", 3, 8); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 10 {
		t.Fatalf("total = %d, want 10", got)
	}
	if got := mustK(t, c, 2); got != 5 { // [3,8)
		t.Fatalf("covered>=2 = %d, want 5", got)
	}
	if got := mustK(t, c, 3); got != 0 {
		t.Fatalf("covered>=3 = %d, want 0", got)
	}
}

func TestDuplicateIntervalsAndLastCopy(t *testing.T) {
	c := New()
	// Same id added twice with the same range: two independent copies.
	if err := c.Add("dup", -4, 6); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("dup", -4, 6); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 10 {
		t.Fatalf("total = %d, want 10", got)
	}
	if got := mustK(t, c, 2); got != 10 {
		t.Fatalf("covered>=2 = %d, want 10", got)
	}
	// Remove one copy: still singly covered everywhere.
	if err := c.Remove("dup"); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 10 {
		t.Fatalf("after 1st remove total = %d, want 10", got)
	}
	if got := mustK(t, c, 2); got != 0 {
		t.Fatalf("after 1st remove covered>=2 = %d, want 0", got)
	}
	// Remove the last copy: everything disappears.
	if err := c.Remove("dup"); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 0 {
		t.Fatalf("after last copy removed total = %d, want 0", got)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
	// A third remove is illegal and must not change results.
	if err := c.Remove("dup"); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("3rd remove: got %v, want ErrIDNotFound", err)
	}
	if got := mustTotal(t, c); got != 0 {
		t.Fatalf("after illegal remove total = %d, want 0", got)
	}
}

func TestIllegalRemoveKeepsState(t *testing.T) {
	c := New()
	if err := c.Add("x", 1, 4); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("y", 3, 9); err != nil {
		t.Fatal(err)
	}
	before := mustTotal(t, c)
	beforeK2 := mustK(t, c, 2)
	for _, id := range []string{"missing", "", "X"} {
		if err := c.Remove(id); !errors.Is(err, ErrIDNotFound) {
			t.Fatalf("Remove(%q): got %v, want ErrIDNotFound", id, err)
		}
	}
	if got := mustTotal(t, c); got != before {
		t.Fatalf("total changed after illegal removes: %d, want %d", got, before)
	}
	if got := mustK(t, c, 2); got != beforeK2 {
		t.Fatalf("covered>=2 changed after illegal removes: %d, want %d", got, beforeK2)
	}
	// Legal removes still work afterwards.
	if err := c.Remove("x"); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 6 { // [3,9)
		t.Fatalf("total = %d, want 6", got)
	}
}

func TestNegativeAndHugeCoordinates(t *testing.T) {
	c := New()
	if err := c.Add("neg", -100, -40); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("cross", -5, 5); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, c); got != 70 {
		t.Fatalf("total = %d, want 70", got)
	}
	// Full int64 span: [MinInt64, MaxInt64) has length 2^64-1 == MaxUint64.
	full := New()
	if err := full.Add("big", math.MinInt64, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, full); got != math.MaxUint64 {
		t.Fatalf("full-span total = %d, want %d", got, uint64(math.MaxUint64))
	}
	if got := mustK(t, full, 2); got != 0 {
		t.Fatalf("full-span covered>=2 = %d, want 0", got)
	}
	// One-unit segments at the extreme endpoints.
	edge := New()
	if err := edge.Add("lo", math.MinInt64, math.MinInt64+1); err != nil {
		t.Fatal(err)
	}
	if err := edge.Add("hi", math.MaxInt64-1, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, edge); got != 2 {
		t.Fatalf("edge total = %d, want 2", got)
	}
	// Overlapping extremes: double-covered length across the sign boundary.
	ov := New()
	if err := ov.Add("a", math.MinInt64, 0); err != nil {
		t.Fatal(err)
	}
	if err := ov.Add("b", -10, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if got := mustK(t, ov, 2); got != 10 { // [-10, 0)
		t.Fatalf("covered>=2 = %d, want 10", got)
	}
	if got := mustTotal(t, ov); got != math.MaxUint64 {
		t.Fatalf("union total = %d, want %d", got, uint64(math.MaxUint64))
	}
}

func TestInvalidInputs(t *testing.T) {
	c := New()
	for _, iv := range [][2]int64{{5, 5}, {7, 3}, {0, 0}, {math.MaxInt64, math.MinInt64}} {
		if err := c.Add("bad", iv[0], iv[1]); !errors.Is(err, ErrInvalidInterval) {
			t.Fatalf("Add(%d,%d): got %v, want ErrInvalidInterval", iv[0], iv[1], err)
		}
	}
	if got := mustTotal(t, c); got != 0 {
		t.Fatalf("invalid adds changed state: total = %d", got)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
	if _, err := c.CoveredAtLeast(0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("CoveredAtLeast(0): got %v, want ErrInvalidK", err)
	}
	if _, err := c.CoveredAtLeast(-3); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("CoveredAtLeast(-3): got %v, want ErrInvalidK", err)
	}
}

func TestZeroValueUsable(t *testing.T) {
	var c Counter
	if err := c.Add("z", 2, 7); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, &c); got != 5 {
		t.Fatalf("total = %d, want 5", got)
	}
	if err := c.Remove("z"); err != nil {
		t.Fatal(err)
	}
	if got := mustTotal(t, &c); got != 0 {
		t.Fatalf("total = %d, want 0", got)
	}
}
