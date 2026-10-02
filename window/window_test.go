package window

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func almostEq(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func checkVector(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len=%d want %d", name, len(got), len(want))
	}
	for i := range got {
		if !almostEq(got[i], want[i]) {
			t.Fatalf("%s: row %d got %v want %v (got[:8]=%v want[:8]=%v)",
				name, i, got[i], want[i], got[:min(8, len(got))], want[:min(8, len(want))])
		}
	}
}

// rowsFrame is shorthand for ROWS BETWEEN off PRECEDING AND CURRENT ROW.
func rowsFrame(off float64) Frame {
	return Frame{Mode: Rows,
		Start: Bound{Kind: Preceding, Offset: off},
		End:   Bound{Kind: CurrentRow}}
}

func TestSumRowsFrameMultiPartition(t *testing.T) {
	in := &Input{
		PartitionKeys: []string{"a", "a", "a", "b", "b"},
		OrderKeys:     []float64{1, 2, 3, 1, 2},
		Values:        []float64{10, 20, 30, 1, 2},
	}
	got, err := Sum(in, rowsFrame(1)) // ROWS 1 PRECEDING .. CURRENT ROW
	if err != nil {
		t.Fatal(err)
	}
	checkVector(t, "sum rows", got, []float64{10, 30, 50, 1, 3})
}

func TestSumRowsFollowingBothDirections(t *testing.T) {
	in := &Input{
		PartitionKeys: []string{"a", "a", "a", "a"},
		OrderKeys:     []float64{1, 2, 3, 4},
		Values:        []float64{1, 2, 4, 8},
	}
	// ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING
	f := Frame{Mode: Rows,
		Start: Bound{Kind: Preceding, Offset: 1},
		End:   Bound{Kind: Following, Offset: 1}}
	got, err := Sum(in, f)
	if err != nil {
		t.Fatal(err)
	}
	checkVector(t, "sum bothdir", got, []float64{3, 7, 14, 12})
}

func TestSumUnboundedFrames(t *testing.T) {
	in := &Input{
		PartitionKeys: []string{"a", "a", "a"},
		OrderKeys:     []float64{1, 2, 3},
		Values:        []float64{1, 2, 4},
	}
	// UNBOUNDED PRECEDING .. CURRENT ROW: running total.
	got, err := Sum(in, Frame{Mode: Rows,
		Start: Bound{Kind: UnboundedPreceding}, End: Bound{Kind: CurrentRow}})
	if err != nil {
		t.Fatal(err)
	}
	checkVector(t, "running total", got, []float64{1, 3, 7})

	// CURRENT ROW .. UNBOUNDED FOLLOWING: reverse running total.
	got, err = Sum(in, Frame{Mode: Rows,
		Start: Bound{Kind: CurrentRow}, End: Bound{Kind: UnboundedFollowing}})
	if err != nil {
		t.Fatal(err)
	}
	checkVector(t, "reverse total", got, []float64{7, 6, 4})
}

func TestSumRangeFrame(t *testing.T) {
	// RANGE 2 PRECEDING .. CURRENT ROW on keys with gaps and ties.
	in := &Input{
		PartitionKeys: []string{"a", "a", "a", "a", "a", "b"},
		OrderKeys:     []float64{1, 2, 2, 5, 6, 1},
		Values:        []float64{1, 2, 4, 8, 16, 100},
	}
	f := Frame{Mode: Range,
		Start: Bound{Kind: Preceding, Offset: 2},
		End:   Bound{Kind: CurrentRow}}
	got, err := Sum(in, f)
	if err != nil {
		t.Fatal(err)
	}
	// key1: [−1,1] -> 1 ; key2: [0,2] -> 1+2+4=7 (twice) ;
	// key5: [3,5] -> 8 ; key6: [4,6] -> 8+16=24 ; b: 100
	checkVector(t, "sum range", got, []float64{1, 7, 7, 8, 24, 100})
}

func TestAvgRowsFrame(t *testing.T) {
	in := &Input{
		PartitionKeys: []string{"a", "a", "a"},
		OrderKeys:     []float64{1, 2, 3},
		Values:        []float64{3, 6, 9},
	}
	got, err := Avg(in, rowsFrame(1))
	if err != nil {
		t.Fatal(err)
	}
	checkVector(t, "avg", got, []float64{3, 4.5, 7.5})
}

func TestAvgEmptyFrameIsNaN(t *testing.T) {
	in := &Input{
		PartitionKeys: []string{"a", "a"},
		OrderKeys:     []float64{1, 2},
		Values:        []float64{5, 6},
	}
	// Row 0 has no preceding row: its frame is empty.
	f := Frame{Mode: Rows,
		Start: Bound{Kind: Preceding, Offset: 1},
		End:   Bound{Kind: Preceding, Offset: 1}}
	got, err := Avg(in, f)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(got[0]) {
		t.Fatalf("expected NaN for empty frame, got %v", got[0])
	}
	if !almostEq(got[1], 5) {
		t.Fatalf("got %v want 5", got[1])
	}
}

func TestRankWithTies(t *testing.T) {
	pk := []string{"a", "a", "a", "a", "b", "b"}
	ok := []float64{10, 10, 20, 30, 5, 5}
	got, err := Rank(pk, ok)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{1, 1, 3, 4, 1, 1}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rank row %d got %d want %d (%v)", i, got[i], want[i], got)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	bad := &Input{PartitionKeys: []string{"a"}, OrderKeys: []float64{1}, Values: []float64{1, 2}}
	if _, err := Sum(bad, rowsFrame(1)); err == nil {
		t.Fatal("expected length-mismatch error")
	}
	unsorted := &Input{PartitionKeys: []string{"a", "a"}, OrderKeys: []float64{2, 1}, Values: []float64{1, 1}}
	if _, err := Sum(unsorted, rowsFrame(1)); err == nil {
		t.Fatal("expected unsorted-order-key error")
	}
	ok := &Input{PartitionKeys: []string{"a"}, OrderKeys: []float64{1}, Values: []float64{1}}
	badFrame := Frame{Mode: Rows,
		Start: Bound{Kind: Following, Offset: 1},
		End:   Bound{Kind: Preceding, Offset: 1}}
	if _, err := Sum(ok, badFrame); err == nil {
		t.Fatal("expected inverted-frame error")
	}
	badRange := Frame{Mode: Range,
		Start: Bound{Kind: Preceding, Offset: 1},
		End:   Bound{Kind: Preceding, Offset: 5}}
	if _, err := Sum(ok, badRange); err == nil {
		t.Fatal("expected nearer-start-than-end error")
	}
}

// naiveSum recomputes every frame from scratch: the O(n*k) reference.
func naiveSum(in *Input, f Frame) []float64 {
	n := len(in.Values)
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		lo, hi := i, i
		if f.Mode == Rows {
			lo = i - int(f.Start.Offset)
			for lo < i && (lo < 0 || in.PartitionKeys[lo] != in.PartitionKeys[i]) {
				lo++
			}
		} else {
			sv := in.OrderKeys[i] - f.Start.Offset
			lo = i
			for lo > 0 && in.PartitionKeys[lo-1] == in.PartitionKeys[i] && in.OrderKeys[lo-1] >= sv {
				lo--
			}
		}
		sum := 0.0
		for j := lo; j <= hi; j++ {
			sum += in.Values[j]
		}
		out[i] = sum
	}
	return out
}

// genTimeSeries builds stores x days of synthetic sales data.
func genTimeSeries(stores, days int, seed int64) *Input {
	n := stores * days
	in := &Input{
		PartitionKeys: make([]string, n),
		OrderKeys:     make([]float64, n),
		Values:        make([]float64, n),
	}
	rng := rand.New(rand.NewSource(seed))
	i := 0
	for s := 0; s < stores; s++ {
		key := fmt.Sprintf("store-%03d", s)
		for d := 0; d < days; d++ {
			in.PartitionKeys[i] = key
			in.OrderKeys[i] = float64(d)
			in.Values[i] = math.Round(rng.Float64()*10000) / 100
			i++
		}
	}
	return in
}

// Acceptance: SUM(sales) OVER (PARTITION BY store ORDER BY date
// ROWS 10 PRECEDING) over 100k rows, verified against the naive
// per-row recomputation.
func TestSlidingSum100kRows(t *testing.T) {
	in := genTimeSeries(50, 2000, 42) // 100k rows
	f := rowsFrame(10)
	got, err := Sum(in, f)
	if err != nil {
		t.Fatal(err)
	}
	want := naiveSum(in, f)
	checkVector(t, "100k sliding sum", got, want)
}

func TestSlidingSumRange100kRows(t *testing.T) {
	in := genTimeSeries(50, 2000, 7)
	f := Frame{Mode: Range,
		Start: Bound{Kind: Preceding, Offset: 10},
		End:   Bound{Kind: CurrentRow}}
	got, err := Sum(in, f)
	if err != nil {
		t.Fatal(err)
	}
	want := naiveSum(in, f)
	checkVector(t, "100k range sum", got, want)
}

func BenchmarkSlidingSum100k(b *testing.B) {
	in := genTimeSeries(50, 2000, 42)
	f := rowsFrame(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Sum(in, f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNaiveSum100k(b *testing.B) {
	in := genTimeSeries(50, 2000, 42)
	f := rowsFrame(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		naiveSum(in, f)
	}
}

// Wide frames show the O(n) vs O(n*k) complexity gap.
func BenchmarkSlidingSum100kWide1000(b *testing.B) {
	in := genTimeSeries(50, 2000, 42)
	f := rowsFrame(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Sum(in, f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNaiveSum100kWide1000(b *testing.B) {
	in := genTimeSeries(50, 2000, 42)
	f := rowsFrame(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		naiveSum(in, f)
	}
}
