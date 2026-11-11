package netsec

import "testing"

func TestDBMGuardAndReset(t *testing.T) {
	// 两只时钟从零点出发，流逝后要求 x1 == 5，再把 x1 归零。
	z := initialZone(2)
	up := z.up()
	if !up.feasible() {
		t.Fatal("up zone infeasible")
	}
	if !up.constrainGuard(Guard{Clock: 1, Op: EQ, Const: 5}) {
		t.Fatal("guard x1==5 made zone infeasible")
	}
	if !up.contains([]float64{5, 5}, Eps/10) {
		t.Fatal("synchronous clocks should contain (5,5)")
	}
	if up.contains([]float64{5, 4}, Eps/10) {
		t.Fatal("zone must reject (5,4)")
	}

	r := up.reset([]int{1})
	if !r.contains([]float64{5, 0}, Eps/10) {
		t.Fatal("reset should yield (5,0)")
	}
	if r.contains([]float64{3, 1}, Eps/10) {
		t.Fatal("reset clock must stay zero")
	}
}

func TestDBMInfeasibleGuard(t *testing.T) {
	z := initialZone(1)
	up := z.up()
	// 同时要求 x > 3 与 x < 2 必为空。
	if up.constrainGuard(Guard{Clock: 0, Op: GT, Const: 3}) == false {
		t.Fatal("first guard unexpectedly infeasible")
	}
	if up.constrainGuard(Guard{Clock: 0, Op: LT, Const: 2}) {
		t.Fatal("contradictory guards should make zone infeasible")
	}
}

func TestDBMKeyStable(t *testing.T) {
	a := initialZone(2)
	b := initialZone(2)
	if a.key() != b.key() {
		t.Fatal("identical zones must have equal keys")
	}
	c := a.up().reset([]int{0})
	if c.key() == a.key() {
		t.Fatal("reset-after-up must change the zone key")
	}
}
