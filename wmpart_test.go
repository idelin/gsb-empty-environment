package wmpart

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestPartitionExample(t *testing.T) {
	res, err := Partition([]int64{1, 2, 3, 10}, []int64{1, 1, 1, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets: []Bucket{
			{Start: 0, End: 3, Median: 2, Cost: 2},
			{Start: 3, End: 4, Median: 10, Cost: 0},
		},
		TotalCost: 2,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestEvenWeightUsesSmallerRepresentative(t *testing.T) {
	res, err := Partition([]int64{10, 20}, []int64{1, 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Buckets[0].Median != 10 {
		t.Fatalf("median = %d, want 10 (smaller representative)", res.Buckets[0].Median)
	}
	if res.TotalCost != 10 {
		t.Fatalf("total cost = %d, want 10", res.TotalCost)
	}
}

func TestDuplicateValuesPreferFewerBuckets(t *testing.T) {
	// One bucket [5,5] already costs 0; splitting adds nothing.
	res, err := Partition([]int64{5, 5}, []int64{1, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets:   []Bucket{{Start: 0, End: 2, Median: 5, Cost: 0}},
		TotalCost: 0,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestZeroWeights(t *testing.T) {
	res, err := Partition([]int64{1, 2, 3, 4}, []int64{0, 0, 0, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets:   []Bucket{{Start: 0, End: 4, Median: 1, Cost: 0}},
		TotalCost: 0,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestZeroWeightElements(t *testing.T) {
	// Only the middle element carries weight; every partition costs 0,
	// so a single bucket wins.
	res, err := Partition([]int64{1, 2, 3}, []int64{0, 5, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets:   []Bucket{{Start: 0, End: 3, Median: 2, Cost: 0}},
		TotalCost: 0,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestTieBreakLexicographicCuts(t *testing.T) {
	// Every 2-bucket split costs 0 and beats the single bucket (cost 3);
	// the smallest cut position {1} must be chosen.
	res, err := Partition([]int64{1, 2, 3, 4}, []int64{1, 0, 0, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets: []Bucket{
			{Start: 0, End: 1, Median: 1, Cost: 0},
			{Start: 1, End: 4, Median: 4, Cost: 0},
		},
		TotalCost: 0,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestKExceedsLength(t *testing.T) {
	res, err := Partition([]int64{1, 2}, []int64{1, 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 2 || res.TotalCost != 0 {
		t.Fatalf("got %+v, want two singleton buckets with cost 0", res)
	}
}

func TestLargeIntegerCosts(t *testing.T) {
	x := []int64{-(1 << 60), 0, 1 << 60}
	w := []int64{3, 1, 3}

	res, err := Partition(x, w, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCost != 6<<60 {
		t.Fatalf("k=1: total cost = %d, want %d", res.TotalCost, int64(6<<60))
	}
	if res.Buckets[0].Median != 0 {
		t.Fatalf("k=1: median = %d, want 0", res.Buckets[0].Median)
	}

	res, err = Partition(x, w, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Buckets: []Bucket{
			{Start: 0, End: 1, Median: -(1 << 60), Cost: 0},
			{Start: 1, End: 3, Median: 1 << 60, Cost: 1 << 60},
		},
		TotalCost: 1 << 60,
	}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("k=2: got %+v, want %+v", res, want)
	}

	for _, k := range []int{1, 2, 3} {
		assertMatchesBruteForce(t, x, w, k)
	}
}

func TestEmptyInput(t *testing.T) {
	res, err := Partition(nil, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCost != 0 || len(res.Buckets) != 0 {
		t.Fatalf("got %+v, want empty result", res)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		x, w []int64
		k    int
		want error
	}{
		{"length mismatch", []int64{1}, []int64{1, 2}, 1, ErrLengthMismatch},
		{"k zero", []int64{1}, []int64{1}, 0, ErrNonPositiveK},
		{"negative weight", []int64{1}, []int64{-1}, 1, ErrNegativeWeight},
		{"unsorted", []int64{2, 1}, []int64{1, 1}, 1, ErrUnsorted},
	}
	for _, c := range cases {
		if _, err := Partition(c.x, c.w, c.k); err != c.want {
			t.Errorf("Partition %s: err = %v, want %v", c.name, err, c.want)
		}
		if _, err := BruteForce(c.x, c.w, c.k); err != c.want {
			t.Errorf("BruteForce %s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func assertMatchesBruteForce(t *testing.T, x, w []int64, k int) {
	t.Helper()
	got, err := Partition(x, w, k)
	if err != nil {
		t.Fatalf("Partition(%v, %v, %d): %v", x, w, k, err)
	}
	want, err := BruteForce(x, w, k)
	if err != nil {
		t.Fatalf("BruteForce(%v, %v, %d): %v", x, w, k, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("x=%v w=%v k=%d:\nPartition:  %+v\nBruteForce: %+v", x, w, k, got, want)
	}
}

// TestPartitionMatchesBruteForce cross-validates the DP against exhaustive
// cut-point enumeration on small random inputs rich in duplicate values
// and zero weights.
func TestPartitionMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(9)
		x := make([]int64, n)
		w := make([]int64, n)
		cur := int64(rng.Intn(3) - 3)
		for i := range x {
			cur += int64(rng.Intn(4)) // frequent duplicate values
			x[i] = cur
			w[i] = int64(rng.Intn(5)) // frequent zero weights
		}
		assertMatchesBruteForce(t, x, w, 1+rng.Intn(n+2))
	}
}

// TestPartitionMatchesBruteForceLargeCosts cross-validates with large
// values whose costs stay well inside int64 but exercise wide arithmetic.
func TestPartitionMatchesBruteForceLargeCosts(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(8)
		x := make([]int64, n)
		w := make([]int64, n)
		cur := -(int64(1) << 50)
		for i := range x {
			cur += rng.Int63n(int64(1) << 50)
			x[i] = cur
			w[i] = int64(rng.Intn(21))
		}
		assertMatchesBruteForce(t, x, w, 1+rng.Intn(n+1))
	}
}
