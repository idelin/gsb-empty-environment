package calib

import (
	"math/big"
	"reflect"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational " + s)
	}
	return r
}

func pt(id, ref, lo, hi string) Point {
	return Point{ID: id, Ref: rat(ref), Lo: rat(lo), Hi: rat(hi)}
}

func TestFeasibleChannel(t *testing.T) {
	points := []Point{
		pt("a0", "0", "3", "7"),
		pt("a1", "10", "22", "28"),
		pt("a2", "20", "43", "47"),
	}
	c := Calibrate(points)
	if !c.Feasible {
		t.Fatalf("expected feasible, got incompatible=%v", c.Incompatible)
	}
	if c.OffsetLo.String() != "3" || c.OffsetHi.String() != "7" {
		t.Errorf("offset range = [%s, %s], want [3, 7]", c.OffsetLo, c.OffsetHi)
	}
	if c.DriftLo.RatString() != "9/5" || c.DriftHi.RatString() != "11/5" {
		t.Errorf("drift range = [%s, %s], want [9/5, 11/5]", c.DriftLo, c.DriftHi)
	}
}

func TestIntegerOffsetTightensRange(t *testing.T) {
	// Rational bounds allow beta in [0.5, 2.5]; integers leave {1, 2}.
	points := []Point{
		pt("p0", "0", "1/2", "5/2"),
		pt("p1", "10", "10", "30"),
	}
	c := Calibrate(points)
	if !c.Feasible {
		t.Fatal("expected feasible")
	}
	if c.OffsetLo.String() != "1" || c.OffsetHi.String() != "2" {
		t.Errorf("offset range = [%s, %s], want [1, 2]", c.OffsetLo, c.OffsetHi)
	}
}

func TestIntegerOffsetInfeasible(t *testing.T) {
	// Rational bounds give beta in [0.2, 0.8]: feasible over the reals but
	// no integer offset exists.
	points := []Point{
		pt("p0", "0", "1/5", "4/5"),
		pt("p1", "10", "10", "12"),
	}
	c := Calibrate(points)
	if c.Feasible {
		t.Fatal("expected infeasible due to integer offset")
	}
	if len(c.Incompatible) == 0 {
		t.Fatal("expected incompatible point set")
	}
}

func TestIncompatibleSubsetIsMinimal(t *testing.T) {
	points := []Point{
		pt("b0", "0", "0", "2"),
		pt("b1", "10", "100", "102"),
		pt("b2", "20", "103", "105"),
	}
	c := Calibrate(points)
	if c.Feasible {
		t.Fatal("expected infeasible")
	}
	// Every pair alone is feasible; all three are needed.
	want := []string{"b0", "b1", "b2"}
	if !reflect.DeepEqual(c.Incompatible, want) {
		t.Errorf("incompatible = %v, want %v", c.Incompatible, want)
	}
	for _, drop := range want {
		var sub []Point
		for _, p := range points {
			if p.ID != drop {
				sub = append(sub, p)
			}
		}
		if !feasible(sub) {
			t.Errorf("subset without %s should be feasible", drop)
		}
	}
}

func TestEqualRefDisjointIntervals(t *testing.T) {
	points := []Point{
		pt("x", "5", "0", "1"),
		pt("y", "5", "3", "4"),
	}
	if feasible(points) {
		t.Fatal("equal-ref disjoint intervals must be infeasible")
	}
}

func TestDriftUnboundedWithoutPositiveRef(t *testing.T) {
	c := Calibrate([]Point{pt("z", "0", "3", "5")})
	if !c.Feasible {
		t.Fatal("expected feasible")
	}
	if c.DriftLo != nil || c.DriftHi != nil {
		t.Errorf("drift should be unconstrained, got [%v, %v]", c.DriftLo, c.DriftHi)
	}
}
