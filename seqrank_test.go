package seqrank

import (
	"math/big"
	"testing"
)

func ratEq(a *big.Rat, num, den int64) bool {
	return a != nil && a.Cmp(new(big.Rat).SetFrac64(num, den)) == 0
}

func TestDoubledRanksTies(t *testing.T) {
	dr := doubledRanks([]float64{5, 5, 1, 2})
	// Ranks are 7/2, 7/2, 1, 2; doubled they are exact integers.
	want := []int64{7, 7, 2, 4}
	for i, w := range want {
		if dr[i] != w {
			t.Fatalf("doubled rank[%d] = %d, want %d", i, dr[i], w)
		}
	}
}

// values 1,2,3,4 with 2 vs 2: only the full prefix can reach dev 2,
// and only assignments {0,1} and {2,3} cross.
func TestEnumerateSmall(t *testing.T) {
	res, err := Enumerate(Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     2, N2: 2,
		Looks: []Look{
			{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
			{Prefix: 3, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
			{Prefix: 4, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || res.Total != 6 || res.Processed != 6 {
		t.Fatalf("meta: %+v", res)
	}
	if res.FirstCross[0] != 0 || res.FirstCross[1] != 0 || res.FirstCross[2] != 2 {
		t.Fatalf("first crossing counts: %v", res.FirstCross)
	}
	if res.Never != 4 {
		t.Fatalf("never = %d, want 4", res.Never)
	}
	if !ratEq(res.CrossProbability(), 1, 3) {
		t.Fatalf("cross prob = %v, want 1/3", res.CrossProbability())
	}
}

// Equality with the threshold counts as crossing (two-sided).
func TestEqualityCountsAsCrossing(t *testing.T) {
	res, err := Enumerate(Spec{
		Values: []float64{1, 2},
		N1:     1, N2: 1,
		Looks: []Look{{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(1, 2)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstCross[0] != 2 || res.Never != 0 {
		t.Fatalf("got %v / never %d, want 2 / 0", res.FirstCross, res.Never)
	}
}

// A prefix in which one group is empty is not inspected.
func TestEmptyGroupPrefixNotInspected(t *testing.T) {
	res, err := Enumerate(Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     1, N2: 3,
		Looks: []Look{{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(0, 1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Group-1 sample at position 0 or 1: inspected, dev 1/2 >= 0 crosses.
	// Group-1 sample at position 2 or 3: prefix has empty group 1, skipped.
	if res.FirstCross[0] != 2 || res.Never != 2 {
		t.Fatalf("got %v / never %d, want [2] / 2", res.FirstCross, res.Never)
	}
}

// Ties share average ranks but remain distinct identities: C(2,1)=2
// assignments are enumerated even though the values are identical.
func TestTiesCountByIdentity(t *testing.T) {
	res, err := Enumerate(Spec{
		Values: []float64{1, 1},
		N1:     1, N2: 1,
		Looks: []Look{{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(0, 1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || res.FirstCross[0] != 2 {
		t.Fatalf("total %d, first %v; want 2 assignments, both crossing at dev 0",
			res.Total, res.FirstCross)
	}
}

// A rational scale standardizes the deviation before comparison.
func TestRationalScale(t *testing.T) {
	// Prefix 4, group1={0,1}: R=3, E=5, |dev|=2; scale 4 -> 1/2.
	// Threshold 1/2 crosses, threshold 3/4 does not.
	cross, err := Enumerate(Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     2, N2: 2,
		Looks: []Look{{Prefix: 4, Scale: Rat(4, 1), Threshold: Rat(1, 2)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cross.FirstCross[0] != 2 {
		t.Fatalf("scaled crossing count = %v, want [2]", cross.FirstCross)
	}
	noCross, err := Enumerate(Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     2, N2: 2,
		Looks: []Look{{Prefix: 4, Scale: Rat(4, 1), Threshold: Rat(3, 4)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if noCross.Never != 6 {
		t.Fatalf("never = %d, want 6", noCross.Never)
	}
}

// Budget truncation keeps partial counts but flags the result as not
// exact; probability helpers must refuse to answer.
func TestBudgetTruncation(t *testing.T) {
	spec := Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     2, N2: 2,
		Looks:  []Look{{Prefix: 4, Scale: Rat(1, 1), Threshold: Rat(2, 1)}},
		Budget: 3,
	}
	res, err := Enumerate(spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete || res.Processed != 3 || res.Total != 6 {
		t.Fatalf("meta: %+v", res)
	}
	// Gosper order starts 0011, 0101, 0110: only {0,1} crosses.
	if res.FirstCross[0] != 1 || res.Never != 2 {
		t.Fatalf("partial counts: %v / never %d", res.FirstCross, res.Never)
	}
	if res.CrossProbability() != nil || res.FirstCrossingDistribution() != nil {
		t.Fatal("partial enumeration must not yield exact probabilities")
	}
}

// The ever-crossing probability is not the product of per-look
// marginal p-values: here every assignment crosses somewhere (exact
// probability 1) while each marginal is 4/6.
func TestNoIndependentPValueMultiplication(t *testing.T) {
	spec := Spec{
		Values: []float64{1, 2, 3, 4},
		N1:     2, N2: 2,
		Looks: []Look{
			{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(1, 2)},
			{Prefix: 4, Scale: Rat(1, 1), Threshold: Rat(1, 2)},
		},
	}
	res, err := Enumerate(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !ratEq(res.CrossProbability(), 1, 1) {
		t.Fatalf("exact ever-cross prob = %v, want 1", res.CrossProbability())
	}
	// Marginal crossing probability at each single look is 4/6.
	for _, li := range []int{0, 1} {
		single, err := Enumerate(Spec{
			Values: spec.Values, N1: 2, N2: 2,
			Looks: []Look{spec.Looks[li]},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !ratEq(single.CrossProbability(), 2, 3) {
			t.Fatalf("look %d marginal = %v, want 2/3", li, single.CrossProbability())
		}
	}
	// (2/3)*(2/3) = 4/9 would badly misstate the joint probability 1.
}

func TestStoppingTime(t *testing.T) {
	values := []float64{1, 2, 3, 4}
	looks := []Look{
		{Prefix: 2, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
		{Prefix: 4, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
	}
	// Assignment {0,1}: prefix-2 look not inspected (group 2 empty
	// there), crosses at the prefix-4 look.
	idx, crossed, err := StoppingTime(values, []bool{true, true, false, false}, looks)
	if err != nil {
		t.Fatal(err)
	}
	if !crossed || idx != 1 {
		t.Fatalf("got look %d crossed %v, want 1 true", idx, crossed)
	}
	// Assignment {0,3}: never crosses.
	idx, crossed, err = StoppingTime(values, []bool{true, false, false, true}, looks)
	if err != nil {
		t.Fatal(err)
	}
	if crossed || idx != -1 {
		t.Fatalf("got look %d crossed %v, want -1 false", idx, crossed)
	}
}

// First crossing time distribution plus never-crossing mass sums to 1.
func TestFirstCrossingDistribution(t *testing.T) {
	res, err := Enumerate(Spec{
		Values: []float64{4, 1, 3, 2, 5},
		N1:     2, N2: 3,
		Looks: []Look{
			{Prefix: 3, Scale: Rat(1, 1), Threshold: Rat(3, 2)},
			{Prefix: 5, Scale: Rat(1, 1), Threshold: Rat(2, 1)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := res.FirstCrossingDistribution()
	if len(d) != 3 {
		t.Fatalf("distribution length %d, want 3", len(d))
	}
	sum := new(big.Rat)
	for _, p := range d {
		sum.Add(sum, p)
	}
	if !ratEq(sum, 1, 1) {
		t.Fatalf("distribution sums to %v, want 1", sum)
	}
	var countSum int64
	for _, c := range res.FirstCross {
		countSum += c
	}
	if countSum+res.Never != res.Total {
		t.Fatalf("counts %d + never %d != total %d", countSum, res.Never, res.Total)
	}
}

func TestValidation(t *testing.T) {
	if _, err := Enumerate(Spec{Values: []float64{1}, N1: 1, N2: 1}); err == nil {
		t.Fatal("expected group-size mismatch error")
	}
	if _, err := Enumerate(Spec{
		Values: []float64{1, 2}, N1: 1, N2: 1,
		Looks: []Look{{Prefix: 2, Scale: Rat(0, 1), Threshold: Rat(1, 1)}},
	}); err == nil {
		t.Fatal("expected non-positive scale error")
	}
	if _, err := Enumerate(Spec{
		Values: []float64{1, 2}, N1: 1, N2: 1,
		Looks: []Look{{Prefix: 3, Scale: Rat(1, 1), Threshold: Rat(1, 1)}},
	}); err == nil {
		t.Fatal("expected prefix range error")
	}
}
