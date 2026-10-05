package intervalstat

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// refCounter is the reference implementation: per-unit coverage counts
// over a small coordinate domain. Unit x represents [x, x+1).
type refCounter struct {
	units map[int64]int64
}

func newRefCounter() *refCounter { return &refCounter{units: make(map[int64]int64)} }

func (r *refCounter) add(start, end int64) {
	for x := start; x < end; x++ {
		r.units[x]++
	}
}

func (r *refCounter) remove(start, end int64) {
	for x := start; x < end; x++ {
		r.units[x]--
		if r.units[x] == 0 {
			delete(r.units, x)
		}
	}
}

func (r *refCounter) coveredAtLeast(k int64) int64 {
	var n int64
	for _, c := range r.units {
		if c >= k {
			n++
		}
	}
	return n
}

func checkAgainstRef(t *testing.T, c *Counter, r *refCounter, maxK int64, ctx string) {
	t.Helper()
	for k := int64(1); k <= maxK; k++ {
		got := c.CoveredAtLeast(k)
		want := big.NewInt(r.coveredAtLeast(k))
		if got.Cmp(want) != 0 {
			t.Fatalf("%s: CoveredAtLeast(%d) = %s, want %s", ctx, k, got, want)
		}
	}
	if got, want := c.TotalCovered(), big.NewInt(r.coveredAtLeast(1)); got.Cmp(want) != 0 {
		t.Fatalf("%s: TotalCovered() = %s, want %s", ctx, got, want)
	}
}

// TestRandomAgainstRef performs random add/remove operations over a small
// domain (including invalid removes and duplicate copies) and compares
// every query against the per-unit reference after each step.
func TestRandomAgainstRef(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	c := New()
	ref := newRefCounter()
	ids := []string{"a", "b", "c"}

	// Track live copies in the test so we can issue both valid and
	// invalid removes.
	type key struct {
		id         string
		start, end int64
	}
	live := make(map[key]int64)

	for step := 0; step < 4000; step++ {
		start := int64(rng.Intn(41) - 20) // [-20, 20]
		end := int64(rng.Intn(41) - 20)
		if start > end {
			start, end = end, start
		}
		id := ids[rng.Intn(len(ids))]
		op := rng.Intn(3)
		switch {
		case op < 2 || len(live) == 0: // add
			before := c.TotalCovered()
			err := c.Add(id, start, end)
			if start == end {
				if err != ErrInvalidInterval {
					t.Fatalf("step %d: Add empty interval: err=%v", step, err)
				}
				if c.TotalCovered().Cmp(before) != 0 {
					t.Fatalf("step %d: failed Add changed state", step)
				}
				break
			}
			if err != nil {
				t.Fatalf("step %d: Add: %v", step, err)
			}
			ref.add(start, end)
			live[key{id, start, end}]++
		default: // remove, sometimes invalid
			if rng.Intn(4) == 0 {
				// Invalid remove: random (id, interval) likely not live.
				rid := ids[rng.Intn(len(ids))]
				c.Remove(rid, start, end)
				if live[key{rid, start, end}] > 0 {
					// Turned out valid; mirror it.
					ref.remove(start, end)
					live[key{rid, start, end}]--
					if live[key{rid, start, end}] == 0 {
						delete(live, key{rid, start, end})
					}
				}
			} else {
				// Valid remove of a known live copy.
				idx := rng.Intn(len(live))
				var k key
				for k = range live {
					if idx == 0 {
						break
					}
					idx--
				}
				c.Remove(k.id, k.start, k.end)
				ref.remove(k.start, k.end)
				live[k]--
				if live[k] == 0 {
					delete(live, k)
				}
			}
		}
		checkAgainstRef(t, c, ref, 4, fmt.Sprintf("step %d", step))
	}
}

func TestAdjacentEndpoints(t *testing.T) {
	c := New()
	mustAdd(t, c, "x", 0, 5)
	mustAdd(t, c, "x", 5, 10)
	// Half-open: [0,5) and [5,10) tile [0,10) with no gap and no overlap.
	assertBig(t, "TotalCovered", c.TotalCovered(), 10)
	assertBig(t, "CoveredAtLeast(2)", c.CoveredAtLeast(2), 0)

	mustAdd(t, c, "y", 3, 7)
	assertBig(t, "TotalCovered", c.TotalCovered(), 10)
	assertBig(t, "CoveredAtLeast(2)", c.CoveredAtLeast(2), 4) // [3,5) and [5,7)
}

func TestDuplicateIntervalsAndLastCopyRemoval(t *testing.T) {
	c := New()
	mustAdd(t, c, "x", 0, 10)
	mustAdd(t, c, "x", 0, 10) // duplicate copy under the same id
	mustAdd(t, c, "y", 0, 10)

	assertBig(t, "TotalCovered", c.TotalCovered(), 10)
	assertBig(t, "CoveredAtLeast(2)", c.CoveredAtLeast(2), 10)
	assertBig(t, "CoveredAtLeast(3)", c.CoveredAtLeast(3), 10)
	assertBig(t, "CoveredAtLeast(4)", c.CoveredAtLeast(4), 0)

	c.Remove("x", 0, 10) // one copy of x remains
	assertBig(t, "TotalCovered", c.TotalCovered(), 10)
	assertBig(t, "CoveredAtLeast(2)", c.CoveredAtLeast(2), 10)
	assertBig(t, "CoveredAtLeast(3)", c.CoveredAtLeast(3), 0)

	c.Remove("x", 0, 10) // last copy of x
	c.Remove("y", 0, 10) // last copy overall
	assertBig(t, "TotalCovered", c.TotalCovered(), 0)
	assertBig(t, "CoveredAtLeast(1)", c.CoveredAtLeast(1), 0)
	if got := c.LiveCopies("x", 0, 10); got != 0 {
		t.Fatalf("LiveCopies = %d, want 0", got)
	}
}

func TestIllegalRemoveDoesNotChangeResult(t *testing.T) {
	c := New()
	mustAdd(t, c, "x", -5, 5)
	before := c.TotalCovered()

	c.Remove("nobody", -5, 5) // unknown id
	c.Remove("x", 0, 3)       // interval never added under x
	c.Remove("x", 5, -5)      // start >= end
	c.Remove("x", 3, 3)       // empty interval
	c.Remove("x", -5, 5)      // valid: removes the only copy
	c.Remove("x", -5, 5)      // now illegal: no copies left

	assertBig(t, "TotalCovered after all removes", c.TotalCovered(), 0)

	// The first four removes must not have changed anything.
	mustAdd(t, c, "x", -5, 5)
	if got := c.TotalCovered(); got.Cmp(before) != 0 {
		t.Fatalf("TotalCovered = %s, want %s", got, before)
	}
	c.Remove("x", -5, 5)
	c.Remove("x", -5, 5) // second remove of a single copy: no-op
	assertBig(t, "TotalCovered", c.TotalCovered(), 0)
}

func TestNegativeAndHugeCoordinates(t *testing.T) {
	c := New()
	mustAdd(t, c, "neg", math.MinInt64, 0)
	mustAdd(t, c, "pos", 0, math.MaxInt64)

	// Total = 2^63 + (2^63 - 1) = 2^64 - 1, which overflows int64.
	want := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	if got := c.TotalCovered(); got.Cmp(want) != 0 {
		t.Fatalf("TotalCovered = %s, want %s", got, want)
	}
	if got := c.CoveredAtLeast(2); got.Sign() != 0 {
		t.Fatalf("CoveredAtLeast(2) = %s, want 0", got)
	}

	// Overlap exactly at 0 via a straddling interval.
	mustAdd(t, c, "mid", -10, 10)
	assertBig(t, "CoveredAtLeast(2)", c.CoveredAtLeast(2), 20)
	if got := c.TotalCovered(); got.Cmp(want) != 0 {
		t.Fatalf("TotalCovered = %s, want %s", got, want)
	}

	c.Remove("neg", math.MinInt64, 0)
	// Remaining: [0, MaxInt64) plus [-10, 10) -> MaxInt64 + 10 units.
	want2 := new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(10))
	if got := c.TotalCovered(); got.Cmp(want2) != 0 {
		t.Fatalf("TotalCovered = %s, want %s", got, want2)
	}
}

func TestInvalidAddRejected(t *testing.T) {
	c := New()
	for _, iv := range [][2]int64{{5, 5}, {7, 3}, {0, 0}, {math.MaxInt64, math.MinInt64}} {
		if err := c.Add("x", iv[0], iv[1]); err != ErrInvalidInterval {
			t.Fatalf("Add(%d, %d): err = %v, want ErrInvalidInterval", iv[0], iv[1], err)
		}
	}
	assertBig(t, "TotalCovered", c.TotalCovered(), 0)
}

func TestEmptyCounter(t *testing.T) {
	c := New()
	assertBig(t, "TotalCovered", c.TotalCovered(), 0)
	assertBig(t, "CoveredAtLeast(5)", c.CoveredAtLeast(5), 0)
	c.Remove("x", 0, 1) // no-op on empty counter
	assertBig(t, "TotalCovered", c.TotalCovered(), 0)
}

func mustAdd(t *testing.T, c *Counter, id string, start, end int64) {
	t.Helper()
	if err := c.Add(id, start, end); err != nil {
		t.Fatalf("Add(%q, %d, %d): %v", id, start, end, err)
	}
}

func assertBig(t *testing.T, name string, got *big.Int, want int64) {
	t.Helper()
	if got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %s, want %d", name, got, want)
	}
}
