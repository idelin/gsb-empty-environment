package median

import (
	"math/rand"
	"sort"
	"testing"
)

// reference computes the lower median and the two absolute-deviation
// sums from a sorted copy of the active values.
func reference(vals map[int]int64) (median, left, right int64, ok bool) {
	if len(vals) == 0 {
		return 0, 0, 0, false
	}
	arr := make([]int64, 0, len(vals))
	for _, v := range vals {
		arr = append(arr, v)
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i] < arr[j] })
	k := (len(arr) + 1) / 2 // lower half size, includes the median
	median = arr[k-1]
	for _, v := range arr[:k] {
		left += median - v
	}
	for _, v := range arr[k:] {
		right += v - median
	}
	return median, left, right, true
}

func checkQuery(t *testing.T, tr *Tracker, vals map[int]int64) {
	t.Helper()
	m, l, r, ok := tr.Query()
	em, el, er, eok := reference(vals)
	if ok != eok || m != em || l != el || r != er {
		t.Fatalf("Query() = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
			m, l, r, ok, em, el, er, eok)
	}
}

func TestBasic(t *testing.T) {
	tr := New()
	if _, _, _, ok := tr.Query(); ok {
		t.Fatal("empty tracker should report ok=false")
	}
	vals := map[int]int64{}
	for i, v := range []int64{5, 1, 9, 3, 7, 7, -4} {
		if err := tr.Add(i, v); err != nil {
			t.Fatal(err)
		}
		vals[i] = v
		checkQuery(t, tr, vals)
	}
	for _, id := range []int{0, 3, 6, 1, 2, 4, 5} {
		if err := tr.Remove(id); err != nil {
			t.Fatal(err)
		}
		delete(vals, id)
		checkQuery(t, tr, vals)
	}
}

func TestDuplicateAndMissing(t *testing.T) {
	tr := New()
	if err := tr.Add(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := tr.Add(1, 20); err != ErrDuplicate {
		t.Fatalf("Add duplicate = %v, want ErrDuplicate", err)
	}
	if err := tr.Remove(2); err != ErrNotFound {
		t.Fatalf("Remove missing = %v, want ErrNotFound", err)
	}
	if err := tr.Remove(1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Remove(1); err != ErrNotFound {
		t.Fatalf("Remove twice = %v, want ErrNotFound", err)
	}
}

// TestRandomAgainstSortedArray cross-checks every query against a
// sorted-array reference under random add/remove workloads.
func TestRandomAgainstSortedArray(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		tr := New()
		vals := map[int]int64{}
		nextID := 0
		for step := 0; step < 2000; step++ {
			if len(vals) == 0 || rng.Intn(3) > 0 {
				id := nextID
				nextID++
				// Small value range forces many equal values.
				v := int64(rng.Intn(21) - 10)
				if err := tr.Add(id, v); err != nil {
					t.Fatal(err)
				}
				vals[id] = v
			} else {
				ids := make([]int, 0, len(vals))
				for id := range vals {
					ids = append(ids, id)
				}
				id := ids[rng.Intn(len(ids))]
				if err := tr.Remove(id); err != nil {
					t.Fatal(err)
				}
				delete(vals, id)
			}
			checkQuery(t, tr, vals)
		}
	}
}

// TestAlternatingExtremes repeatedly adds and removes the same extreme
// value, verifying results after every operation.
func TestAlternatingExtremes(t *testing.T) {
	for _, extreme := range []int64{-1 << 40, 1 << 40} {
		tr := New()
		vals := map[int]int64{}
		// Base set.
		for i := 0; i < 50; i++ {
			v := int64(i*7%13) - 6
			if err := tr.Add(i, v); err != nil {
				t.Fatal(err)
			}
			vals[i] = v
		}
		checkQuery(t, tr, vals)
		id := 1000
		for round := 0; round < 500; round++ {
			if err := tr.Add(id, extreme); err != nil {
				t.Fatal(err)
			}
			vals[id] = extreme
			checkQuery(t, tr, vals)
			if err := tr.Remove(id); err != nil {
				t.Fatal(err)
			}
			delete(vals, id)
			checkQuery(t, tr, vals)
		}
	}
}

// TestRebuildConsistency verifies that queries are identical before and
// after the automatic cleanup, and that the physical node bound holds.
func TestRebuildConsistency(t *testing.T) {
	tr := New()
	vals := map[int]int64{}
	for i := 0; i < 100; i++ {
		v := int64(i*37%50) - 25
		if err := tr.Add(i, v); err != nil {
			t.Fatal(err)
		}
		vals[i] = v
	}
	// Accumulate tombstones well past the rebuild threshold.
	for i := 0; i < 100; i++ {
		id := 1000 + i
		if err := tr.Add(id, int64(i)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Remove(id); err != nil {
			t.Fatal(err)
		}
	}
	// Automatic cleanup must have kept physical nodes within the bound.
	if got, limit := tr.physical(), 2*tr.Len()+slack; got > limit {
		t.Fatalf("physical nodes = %d, exceeds limit %d", got, limit)
	}
	m1, l1, r1, ok1 := tr.Query()
	checkQuery(t, tr, vals)

	// Force an explicit rebuild and confirm results do not change.
	tr.rebuild()
	if tr.physical() != tr.Len() {
		t.Fatalf("after rebuild physical = %d, want %d", tr.physical(), tr.Len())
	}
	m2, l2, r2, ok2 := tr.Query()
	if m1 != m2 || l1 != l2 || r1 != r2 || ok1 != ok2 {
		t.Fatalf("rebuild changed result: (%d,%d,%d,%v) vs (%d,%d,%d,%v)",
			m1, l1, r1, ok1, m2, l2, r2, ok2)
	}
	checkQuery(t, tr, vals)
}

// TestEqualValuesDistinguishedByID ensures equal values are tracked
// independently by ID.
func TestEqualValuesDistinguishedByID(t *testing.T) {
	tr := New()
	vals := map[int]int64{}
	for i := 0; i < 10; i++ {
		if err := tr.Add(i, 7); err != nil {
			t.Fatal(err)
		}
		vals[i] = 7
	}
	checkQuery(t, tr, vals)
	for i := 0; i < 10; i += 2 {
		if err := tr.Remove(i); err != nil {
			t.Fatal(err)
		}
		delete(vals, i)
		checkQuery(t, tr, vals)
	}
}

// TestIDReuseWithDifferentValues removes an ID and re-adds it with a
// different value, ensuring tombstones never discard the new node.
func TestIDReuseWithDifferentValues(t *testing.T) {
	tr := New()
	vals := map[int]int64{}
	for i := 1; i <= 30; i++ {
		v := int64(i * 3)
		if err := tr.Add(i, v); err != nil {
			t.Fatal(err)
		}
		vals[i] = v
	}
	// Repeatedly recycle the same IDs with changed values.
	for round := 0; round < 200; round++ {
		for _, id := range []int{7, 15, 23} {
			if err := tr.Remove(id); err != nil {
				t.Fatal(err)
			}
			delete(vals, id)
			checkQuery(t, tr, vals)
			v := int64((round+id)*11%61) - 30
			if err := tr.Add(id, v); err != nil {
				t.Fatal(err)
			}
			vals[id] = v
			checkQuery(t, tr, vals)
		}
	}
}
