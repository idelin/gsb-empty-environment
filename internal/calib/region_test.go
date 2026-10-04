package calib

import (
	"math/big"
	"testing"
)

func mkc(id string, t int64, lo, hi string) Constraint {
	return Constraint{
		ID: id, T: RatInt(t),
		Lo: mustParse(lo), Hi: mustParse(hi),
	}
}

func mustParse(s string) *big.Rat {
	r, err := ParseRat(s)
	if err != nil {
		panic(err)
	}
	return r
}

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := ParseRat(s)
	if err != nil {
		t.Fatalf("bad rat %q: %v", s, err)
	}
	return r
}

func eqRat(t *testing.T, got *big.Rat, want string) {
	t.Helper()
	w := mustRat(t, want)
	if got == nil || got.Cmp(w) != 0 {
		t.Fatalf("got %v, want %v", RatStr(got), want)
	}
}

// Hand-computed: points (0,[10,12]) and (100,[110,114]).
func TestSolveBasic(t *testing.T) {
	cs := []Constraint{mkc("p0", 0, "10", "12"), mkc("p1", 100, "110", "114")}
	reg := Solve(cs, Interval{})
	if !reg.Feasible || !reg.IntFeasible {
		t.Fatalf("expected feasible, got %+v", reg)
	}
	eqRat(t, reg.A.Lo, "49/50")
	eqRat(t, reg.A.Hi, "26/25")
	eqRat(t, reg.B.Lo, "10")
	eqRat(t, reg.B.Hi, "12")
	if reg.BIntLo.String() != "10" || reg.BIntHi.String() != "12" {
		t.Fatalf("int offsets [%v, %v], want [10, 12]", reg.BIntLo, reg.BIntHi)
	}
}

func TestSolveInfeasibleSameInstant(t *testing.T) {
	cs := []Constraint{mkc("a", 0, "0", "1"), mkc("b", 0, "5", "6")}
	reg := Solve(cs, Interval{})
	if reg.Feasible {
		t.Fatalf("expected infeasible, got %+v", reg)
	}
}

func TestIntegerOffsetInfeasible(t *testing.T) {
	// b must lie in [1/3, 1/2]: rationally feasible, no integer offset.
	cs := []Constraint{mkc("a", 0, "1/3", "1/2")}
	reg := Solve(cs, Interval{})
	if !reg.Feasible {
		t.Fatalf("expected rationally feasible")
	}
	if reg.IntFeasible {
		t.Fatalf("expected integer-offset infeasible, got %+v", reg)
	}
}

func TestSolveUnbounded(t *testing.T) {
	reg := Solve(nil, Interval{})
	if !reg.Feasible || !reg.IntFeasible {
		t.Fatalf("empty constraints must be feasible")
	}
	if reg.A.Lo != nil || reg.A.Hi != nil || reg.B.Lo != nil || reg.B.Hi != nil {
		t.Fatalf("empty constraints must be unbounded, got %+v", reg)
	}
	if reg.BIntLo != nil || reg.BIntHi != nil {
		t.Fatalf("empty constraints: int offsets unbounded")
	}
}

func TestDriftBoundsRespected(t *testing.T) {
	cs := []Constraint{mkc("p0", 0, "10", "12"), mkc("p1", 100, "110", "114")}
	bounds := Interval{Lo: RatInt(1), Hi: RatInt(2)}
	reg := Solve(cs, bounds)
	eqRat(t, reg.A.Lo, "1")
	eqRat(t, reg.A.Hi, "26/25")
}

func TestEventIntervalBasic(t *testing.T) {
	cs := []Constraint{mkc("p0", 0, "10", "12"), mkc("p1", 100, "110", "114")}
	reg := Solve(cs, Interval{})
	iv := EventInterval(cs, Interval{}, reg, RatInt(50))
	eqRat(t, iv.Lo, "60")
	eqRat(t, iv.Hi, "63")
}

func TestEventIntervalAtCalibrationPoint(t *testing.T) {
	cs := []Constraint{mkc("p0", 0, "10", "12"), mkc("p1", 100, "110", "114")}
	reg := Solve(cs, Interval{})
	iv := EventInterval(cs, Interval{}, reg, RatInt(100))
	eqRat(t, iv.Lo, "110")
	eqRat(t, iv.Hi, "114")
}

func TestMaxCompatibleSubset(t *testing.T) {
	cs := []Constraint{
		mkc("c0", 0, "0", "2"),
		mkc("c1", 0, "1", "3"),
		mkc("c2", 0, "5", "6"),
	}
	kept, dropped := MaxCompatibleSubset(cs, Interval{})
	if len(kept) != 2 || len(dropped) != 1 || dropped[0].ID != "c2" {
		t.Fatalf("kept=%v dropped=%v", constraintIDs(kept), constraintIDs(dropped))
	}
	if !Solve(kept, Interval{}).IntFeasible {
		t.Fatalf("kept subset must be feasible")
	}
}

func TestMaxCompatibleSubsetAllFeasible(t *testing.T) {
	cs := []Constraint{mkc("p0", 0, "10", "12"), mkc("p1", 100, "110", "114")}
	kept, dropped := MaxCompatibleSubset(cs, Interval{})
	if len(dropped) != 0 || len(kept) != 2 {
		t.Fatalf("expected no drops, got %v", constraintIDs(dropped))
	}
}

func constraintIDs(cs []Constraint) []string {
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestEventIntervalUnbounded(t *testing.T) {
	// Single calibration point: drift and offset are both unbounded, so any
	// event with tau != 0 has an unbounded global-time interval.
	cs := []Constraint{mkc("p0", 5, "10", "12")}
	reg := Solve(cs, Interval{})
	if !reg.IntFeasible {
		t.Fatalf("expected feasible")
	}
	iv := EventInterval(cs, Interval{}, reg, RatInt(7))
	if iv.Lo != nil || iv.Hi != nil {
		t.Fatalf("expected unbounded interval, got [%v, %v]", RatStr(iv.Lo), RatStr(iv.Hi))
	}
	// At the calibrated instant itself the interval is exactly [10, 12].
	iv = EventInterval(cs, Interval{}, reg, RatInt(5))
	eqRat(t, iv.Lo, "10")
	eqRat(t, iv.Hi, "12")
}
