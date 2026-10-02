package windowfn

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func checkVec(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len got %d, want %d", name, len(got), len(want))
	}
	for i := range got {
		if !almostEq(got[i], want[i]) {
			t.Fatalf("%s[%d]: got %v, want %v", name, i, got[i], want[i])
		}
	}
}

// Two partitions, interleaved in input order; results must be written back
// in original row order.
func TestSumRowsPreceding(t *testing.T) {
	store := []string{"b", "a", "a", "b", "a", "b"}
	date := []float64{1, 1, 2, 2, 3, 3}
	sales := []float64{10, 1, 2, 20, 3, 30}

	w := NewWindow(store, date)
	got := w.Sum(sales, RowsPreceding(1))
	// partition a: 1, 1+2=3, 2+3=5 ; partition b: 10, 10+20=30, 20+30=50
	want := []float64{10, 1, 3, 30, 5, 50}
	checkVec(t, "sum", got, want)
}

func TestSumRowsFrameLargerThanPartition(t *testing.T) {
	store := []string{"a", "a", "a"}
	date := []float64{1, 2, 3}
	sales := []float64{1, 2, 3}

	w := NewWindow(store, date)
	got := w.Sum(sales, RowsPreceding(10))
	want := []float64{1, 3, 6} // clamped at partition start
	checkVec(t, "sum", got, want)
}

func TestAvgRowsPreceding(t *testing.T) {
	store := []string{"a", "a", "a", "a"}
	date := []float64{1, 2, 3, 4}
	sales := []float64{2, 4, 6, 8}

	w := NewWindow(store, date)
	got := w.Avg(sales, RowsPreceding(1))
	want := []float64{2, 3, 5, 7}
	checkVec(t, "avg", got, want)
}

func TestSumUnboundedPreceding(t *testing.T) {
	store := []string{"a", "a", "b", "b"}
	date := []float64{1, 2, 1, 2}
	sales := []float64{1, 2, 10, 20}

	w := NewWindow(store, date)
	got := w.Sum(sales, UnboundedRows())
	want := []float64{1, 3, 10, 30} // running totals per partition
	checkVec(t, "sum", got, want)
}

// RANGE must include all peers of the current row and use key distance,
// not row count. Order keys have gaps and duplicates.
func TestSumRangePreceding(t *testing.T) {
	store := []string{"a", "a", "a", "a", "a"}
	date := []float64{1, 1, 2, 5, 6}
	sales := []float64{1, 2, 4, 8, 16}

	w := NewWindow(store, date)
	got := w.Sum(sales, RangePreceding(1))
	// key 1: peers {1,2} -> 3 ; key 2: keys in [1,2] -> 1+2+4=7
	// key 5: keys in [4,5] -> 8 ; key 6: keys in [5,6] -> 8+16=24
	want := []float64{3, 3, 7, 8, 24}
	checkVec(t, "sum", got, want)
}

func TestAvgRangePreceding(t *testing.T) {
	store := []string{"a", "a", "a"}
	date := []float64{1, 2, 3}
	sales := []float64{10, 20, 30}

	w := NewWindow(store, date)
	got := w.Avg(sales, RangePreceding(2))
	want := []float64{10, 15, 20} // (10)/1, (10+20)/2, (10+20+30)/3
	checkVec(t, "avg", got, want)
}

func TestRank(t *testing.T) {
	store := []string{"a", "a", "a", "a", "b", "b"}
	date := []float64{5, 1, 3, 3, 7, 2}

	w := NewWindow(store, date)
	got := w.Rank()
	// partition a sorted keys: 1,3,3,5 -> ranks 1,2,2,4 ; b: 2,7 -> 1,2
	want := []int64{4, 1, 2, 2, 2, 1}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rank[%d]: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestEmptyAndSingleRow(t *testing.T) {
	w := NewWindow(nil, nil)
	if got := w.Sum(nil, RowsPreceding(3)); len(got) != 0 {
		t.Fatalf("empty: got %v", got)
	}
	w = NewWindow([]string{"a"}, []float64{1})
	got := w.Sum([]float64{42}, RowsPreceding(10))
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("single: got %v", got)
	}
}

// naiveSumRows is an independent O(n*k) reference: for every row, rescan
// its frame row by row.
func naiveSumRows(sales []float64, perm []int, parts []Partition, preceding int64) []float64 {
	out := make([]float64, len(sales))
	for _, p := range parts {
		for pos := p.Start; pos < p.End; pos++ {
			lo := pos - int(preceding)
			if lo < p.Start {
				lo = p.Start
			}
			var sum float64
			for k := lo; k <= pos; k++ {
				sum += sales[perm[k]]
			}
			out[perm[pos]] = sum
		}
	}
	return out
}

// Acceptance: 100k time-series rows,
// SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING),
// validated row-by-row against the naive reference.
func TestAcceptance100kRows10Preceding(t *testing.T) {
	const stores = 50
	const days = 2000
	n := stores * days

	rng := rand.New(rand.NewSource(1))
	store := make([]string, n)
	date := make([]float64, n)
	sales := make([]float64, n)
	for i := 0; i < n; i++ {
		store[i] = fmt.Sprintf("store_%02d", rng.Intn(stores))
		date[i] = float64(rng.Intn(days)) // shuffled, duplicates allowed
		sales[i] = rng.Float64() * 100
	}

	w := NewWindow(store, date)
	got := w.Sum(sales, RowsPreceding(10))
	want := naiveSumRows(sales, w.perm, w.parts, 10)

	for i := 0; i < n; i++ {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			t.Fatalf("row %d (store=%s date=%v): got %v, want %v",
				i, store[i], date[i], got[i], want[i])
		}
	}
}

func BenchmarkSumRows10Preceding100k(b *testing.B) {
	const stores = 50
	const days = 2000
	n := stores * days
	rng := rand.New(rand.NewSource(1))
	store := make([]string, n)
	date := make([]float64, n)
	sales := make([]float64, n)
	for i := 0; i < n; i++ {
		store[i] = fmt.Sprintf("store_%02d", i%stores)
		date[i] = float64(i / stores)
		sales[i] = rng.Float64() * 100
	}
	w := NewWindow(store, date)
	frame := RowsPreceding(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Sum(sales, frame)
	}
}
