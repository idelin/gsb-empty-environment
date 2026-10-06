package gcrt

import (
	"math/big"
	"math/rand"
	"testing"
)

// enumLimit caps the period for brute-force enumeration.
const enumLimit = 200000

// bruteForce enumerates one full period [0, L), where L is the lcm of the
// moduli (1 for the empty system), and returns the set of residues satisfying
// every constraint. Only suitable for small moduli.
func bruteForce(cs []Constraint) (sols map[int64]bool, period int64) {
	period = lcmOf(cs)
	sols = make(map[int64]bool)
	for x := int64(0); x < period; x++ {
		ok := true
		for _, c := range cs {
			m := c.Mod.Int64()
			r := c.Res.Int64() % m
			if r < 0 {
				r += m
			}
			if x%m != r {
				ok = false
				break
			}
		}
		if ok {
			sols[x] = true
		}
	}
	return sols, period
}

// lcmOf returns the lcm of the constraint moduli (1 for the empty system).
func lcmOf(cs []Constraint) int64 {
	period := int64(1)
	for _, c := range cs {
		m := c.Mod.Int64()
		period = period * m / gcd64(period, m)
	}
	return period
}

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// checkAgainstEnumeration verifies a solver result against brute force.
func checkAgainstEnumeration(t *testing.T, cs []Constraint, sol Solution) {
	t.Helper()
	sols, period := bruteForce(cs)
	if len(sols) == 0 {
		if sol.OK {
			t.Fatalf("solver claims feasible (%v) but enumeration finds nothing", sol)
		}
		checkEarliestConflict(t, cs, sol.Conflict)
		return
	}
	if !sol.OK {
		t.Fatalf("solver claims infeasible (conflict %v) but enumeration finds %v", sol.Conflict, sols)
	}
	if sol.Period.Int64() != period {
		t.Fatalf("period = %d, want lcm %d", sol.Period, period)
	}
	if sol.Res.Sign() < 0 || sol.Res.Cmp(sol.Period) >= 0 {
		t.Fatalf("representative %s not minimal non-negative in [0, %s)", sol.Res, sol.Period)
	}
	if _, ok := sols[sol.Res.Int64()]; !ok {
		t.Fatalf("representative %s is not a solution", sol.Res)
	}
	for x := int64(0); x < period; x++ {
		_, want := sols[x]
		if got := sol.Contains(big.NewInt(x)); got != want {
			t.Fatalf("Contains(%d) = %v, want %v", x, got, want)
		}
	}
}

// checkEarliestConflict verifies that the reported pair is genuinely
// contradictory and is the earliest such pair in insertion order.
func checkEarliestConflict(t *testing.T, cs []Constraint, got [2]int64) {
	t.Helper()
	var want [2]int64
	found := false
	for j := 0; j < len(cs) && !found; j++ {
		for i := 0; i < j; i++ {
			if !pairwiseConsistent(cs[i], cs[j]) {
				want = [2]int64{cs[i].ID, cs[j].ID}
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("solver reported conflict %v but constraints are pairwise consistent", got)
	}
	if got != want {
		t.Fatalf("conflict pair = %v, want earliest %v", got, want)
	}
}

func TestEmptySystemIsAllIntegers(t *testing.T) {
	sol := SolveConstraints(nil)
	if !sol.OK || sol.Res.Sign() != 0 || sol.Period.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("empty system = %v, want x ≡ 0 (mod 1)", sol)
	}
	for _, x := range []int64{-1000000, -7, -1, 0, 1, 42, 999983} {
		if !sol.Contains(big.NewInt(x)) {
			t.Fatalf("empty system should contain %d", x)
		}
	}
}

func TestRandomSystemsAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 2000; trial++ {
		n := 1 + rng.Intn(7)
		var cs []Constraint
		for attempt := 0; attempt < 100; attempt++ {
			cs = make([]Constraint, n)
			for i := range cs {
				cs[i] = Constraint{
					ID:  int64(i + 1),
					Mod: big.NewInt(int64(1 + rng.Intn(60))),
					Res: big.NewInt(int64(rng.Intn(201) - 100)), // negative residues included
				}
			}
			if lcmOf(cs) <= enumLimit {
				break
			}
		}
		checkAgainstEnumeration(t, cs, SolveConstraints(cs))
	}
}

func TestMergePairsAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 5000; trial++ {
		cs := []Constraint{
			{ID: 1, Mod: big.NewInt(int64(1 + rng.Intn(40))), Res: big.NewInt(int64(rng.Intn(81) - 40))},
			{ID: 2, Mod: big.NewInt(int64(1 + rng.Intn(40))), Res: big.NewInt(int64(rng.Intn(81) - 40))},
		}
		checkAgainstEnumeration(t, cs, SolveConstraints(cs))
	}
}

func TestKnownConflictPair(t *testing.T) {
	// x ≡ 1 (mod 4), x ≡ 3 (mod 6), x ≡ 2 (mod 8):
	// constraints 1 and 3 conflict (1 ≢ 2 mod gcd(4,8)=4); the earliest
	// pair must be reported even though constraint 2 is compatible with 1.
	cs := []Constraint{
		{ID: 10, Mod: big.NewInt(4), Res: big.NewInt(1)},
		{ID: 20, Mod: big.NewInt(6), Res: big.NewInt(3)},
		{ID: 30, Mod: big.NewInt(8), Res: big.NewInt(2)},
	}
	sol := SolveConstraints(cs)
	if sol.OK {
		t.Fatalf("expected infeasible, got %v", sol)
	}
	if sol.Conflict != [2]int64{10, 30} {
		t.Fatalf("conflict = %v, want [10 30]", sol.Conflict)
	}
}

func TestBigIntegersAndNegativeResidues(t *testing.T) {
	// m1 = 3 * 2^100, m2 = 5 * 2^90; gcd = 2^90, lcm = 15 * 2^100.
	pow100 := new(big.Int).Lsh(big.NewInt(1), 100)
	pow90 := new(big.Int).Lsh(big.NewInt(1), 90)
	m1 := new(big.Int).Mul(big.NewInt(3), pow100)
	m2 := new(big.Int).Mul(big.NewInt(5), pow90)
	// Pick a large target x and derive residues, one of them negative.
	x := new(big.Int).Add(pow100, big.NewInt(123456789))
	r1 := new(big.Int).Mod(x, m1)
	r2 := new(big.Int).Neg(new(big.Int).Sub(m2, new(big.Int).Mod(x, m2))) // negative form of x mod m2
	cs := []Constraint{
		{ID: 1, Mod: m1, Res: r1},
		{ID: 2, Mod: m2, Res: r2},
	}
	sol := SolveConstraints(cs)
	if !sol.OK {
		t.Fatalf("expected feasible, got conflict %v", sol.Conflict)
	}
	wantPeriod := new(big.Int).Mul(big.NewInt(15), pow100)
	if sol.Period.Cmp(wantPeriod) != 0 {
		t.Fatalf("period = %s, want %s", sol.Period, wantPeriod)
	}
	if !sol.Contains(x) {
		t.Fatalf("solution %v does not contain constructed x = %s", sol, x)
	}
	miss := new(big.Int).Add(x, big.NewInt(1))
	if sol.Contains(miss) && new(big.Int).Mod(miss, wantPeriod).Cmp(sol.Res) != 0 {
		t.Fatalf("solution contains non-solution x+1")
	}
	// Incompatible big system must report the pair.
	bad := []Constraint{
		{ID: 7, Mod: m1, Res: r1},
		{ID: 9, Mod: m2, Res: new(big.Int).Add(r2, big.NewInt(1))},
	}
	if sol := SolveConstraints(bad); sol.OK || sol.Conflict != [2]int64{7, 9} {
		t.Fatalf("bad system = %v, want conflict [7 9]", sol)
	}
}
