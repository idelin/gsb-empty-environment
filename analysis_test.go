package netsec

import (
	"math/big"
	"reflect"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func table(entries ...any) NoiseTable {
	t := NoiseTable{}
	for i := 0; i < len(entries); i += 2 {
		t.Entries = append(t.Entries, Entry{
			Offset: entries[i].(int),
			Prob:   entries[i+1].(*big.Rat),
		})
	}
	return t
}

// symmetric three-point table used by several tests.
func threePoint(t *testing.T) NoiseTable {
	return table(-1, rat(t, "1/4"), 0, rat(t, "1/2"), 1, rat(t, "1/4"))
}

func TestDistributionTruncateAccumulatesBoundaryMass(t *testing.T) {
	tbl := table(-5, rat(t, "1/3"), 0, rat(t, "1/3"), 5, rat(t, "1/3"))
	spec := Spec{Lo: 0, Hi: 2, Mode: Truncate}
	dist, err := tbl.Distribution(0, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]*big.Rat{0: rat(t, "2/3"), 2: rat(t, "1/3")}
	if !reflect.DeepEqual(dist, want) {
		t.Fatalf("got %v, want %v", dist, want)
	}
}

func TestDistributionWrapFoldsModulo(t *testing.T) {
	tbl := threePoint(t)
	spec := Spec{Lo: 0, Hi: 3, Mode: Wrap}
	dist, err := tbl.Distribution(0, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]*big.Rat{0: rat(t, "1/2"), 1: rat(t, "1/4"), 3: rat(t, "1/4")}
	if !reflect.DeepEqual(dist, want) {
		t.Fatalf("got %v, want %v", dist, want)
	}
}

func TestAnalyzeTruncateExactDelta(t *testing.T) {
	spec := Spec{Lo: 0, Hi: 3, Mode: Truncate}
	res, err := Analyze(threePoint(t), spec, rat(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Delta.Cmp(rat(t, "1/4")) != 0 {
		t.Fatalf("delta = %s, want 1/4", res.Delta)
	}
	if res.Count != 0 || res.Direction != Forward {
		t.Fatalf("worst pair = (%d,%s), want (0,forward)", res.Count, res.Direction)
	}
	if !reflect.DeepEqual(res.Set, []int{0}) {
		t.Fatalf("worst set = %v, want [0]", res.Set)
	}
}

func TestAnalyzeWrapExactDelta(t *testing.T) {
	spec := Spec{Lo: 0, Hi: 3, Mode: Wrap}
	res, err := Analyze(threePoint(t), spec, rat(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Delta.Cmp(rat(t, "1/4")) != 0 {
		t.Fatalf("delta = %s, want 1/4", res.Delta)
	}
	if res.Count != 0 || res.Direction != Forward {
		t.Fatalf("worst pair = (%d,%s), want (0,forward)", res.Count, res.Direction)
	}
	if !reflect.DeepEqual(res.Set, []int{3}) {
		t.Fatalf("worst set = %v, want [3]", res.Set)
	}
}

// Non-terminating decimal expansions (thirds) stay exact: the minimal delta
// is exactly 1/3, not a float approximation.
func TestAnalyzeExactWithThirds(t *testing.T) {
	tbl := table(-1, rat(t, "1/3"), 0, rat(t, "1/3"), 1, rat(t, "1/3"))
	spec := Spec{Lo: 0, Hi: 2, Mode: Truncate}
	res, err := Analyze(tbl, spec, rat(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Delta.Cmp(rat(t, "1/3")) != 0 {
		t.Fatalf("delta = %s, want exactly 1/3", res.Delta)
	}
}

func TestCheckClaim(t *testing.T) {
	spec := Spec{Lo: 0, Hi: 3, Mode: Truncate}
	ok, delta, err := CheckClaim(threePoint(t), spec, rat(t, "2"), rat(t, "1/4"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || delta.Cmp(rat(t, "1/4")) != 0 {
		t.Fatalf("claim (2, 1/4): ok=%v delta=%s, want ok=true delta=1/4", ok, delta)
	}
	ok, delta, err = CheckClaim(threePoint(t), spec, rat(t, "2"), rat(t, "1/8"))
	if err != nil {
		t.Fatal(err)
	}
	if ok || delta.Cmp(rat(t, "1/4")) != 0 {
		t.Fatalf("claim (2, 1/8): ok=%v delta=%s, want ok=false delta=1/4", ok, delta)
	}
}

// Exhaustive subset enumeration must agree with the constructive worst set.
func TestExhaustiveDeltaMatchesWorstSet(t *testing.T) {
	tbl := threePoint(t)
	spec := Spec{Lo: 0, Hi: 3, Mode: Truncate}
	rho := rat(t, "3/2")
	for c := spec.Lo; c < spec.Hi; c++ {
		pc, err := tbl.Distribution(c, spec)
		if err != nil {
			t.Fatal(err)
		}
		pn, err := tbl.Distribution(c+1, spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]map[int]*big.Rat{{pc, pn}, {pn, pc}} {
			got := HockeyStick(pair[0], pair[1], rho)
			bf, bfSet, err := ExhaustiveDelta(pair[0], pair[1], rho)
			if err != nil {
				t.Fatal(err)
			}
			if got.Cmp(bf) != 0 {
				t.Fatalf("c=%d: hockey-stick %s != exhaustive %s", c, got, bf)
			}
			if want := WorstSet(pair[0], pair[1], rho); !reflect.DeepEqual(bfSet, want) {
				t.Fatalf("c=%d: exhaustive set %v != worst set %v", c, bfSet, want)
			}
		}
	}
}

func TestValidateRejectsBadTables(t *testing.T) {
	if err := (NoiseTable{}).Validate(); err == nil {
		t.Fatal("empty table accepted")
	}
	bad := table(0, rat(t, "1/2"), 1, rat(t, "2/3"))
	if err := bad.Validate(); err == nil {
		t.Fatal("table with sum != 1 accepted")
	}
	neg := table(0, rat(t, "-1/2"), 1, rat(t, "3/2"))
	if err := neg.Validate(); err == nil {
		t.Fatal("table with negative probability accepted")
	}
}

func TestAnalyzeRejectsBadInput(t *testing.T) {
	tbl := threePoint(t)
	if _, err := Analyze(tbl, Spec{Lo: 0, Hi: 3, Mode: Truncate}, rat(t, "0")); err == nil {
		t.Fatal("non-positive rho accepted")
	}
	if _, err := Analyze(tbl, Spec{Lo: 0, Hi: 0, Mode: Truncate}, rat(t, "1")); err == nil {
		t.Fatal("single-point range accepted")
	}
	if _, err := tbl.Distribution(5, Spec{Lo: 0, Hi: 3, Mode: Truncate}); err == nil {
		t.Fatal("count outside range accepted")
	}
}
