package main

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates every price vector in [-B, B]^n and returns the true
// lexicographic optimum: feasibility, min number of changes, min total
// absolute deviation among min-change solutions. The test generators keep
// |a_i| <= 4 and |bounds| <= 4, so any feasible instance has an optimal
// solution with |p_i| <= (n+2)*4 <= 24 < B.
func bruteForce(p Problem, B int64) (feasible bool, minChanges int, minDev int64) {
	n := p.N
	minChanges = n + 1
	minDev = int64(1) << 62
	cur := make([]int64, n)
	// constraints grouped by the larger endpoint for early pruning
	type pc struct{ c Constraint }
	var walk func(i int)
	walk = func(i int) {
		if i > 0 {
			j := i - 1
			if p.Locked[j] && cur[j] != p.A[j] {
				return
			}
			for _, c := range p.Cons {
				mx := c.U
				if c.V > mx {
					mx = c.V
				}
				if mx != j {
					continue // not fully assigned yet
				}
				d := cur[c.V] - cur[c.U]
				if c.HasHi && d > c.Hi {
					return
				}
				if c.HasLo && d < c.Lo {
					return
				}
			}
		}
		if i == n {
			ch := 0
			var dev int64
			for j := 0; j < n; j++ {
				if cur[j] != p.A[j] {
					ch++
					dev += abs64(cur[j] - p.A[j])
				}
			}
			feasible = true
			if ch < minChanges || (ch == minChanges && dev < minDev) {
				minChanges = ch
				minDev = dev
			}
			return
		}
		for v := -B; v <= B; v++ {
			cur[i] = v
			walk(i + 1)
		}
	}
	walk(0)
	return
}

func checkPrices(t *testing.T, p Problem, prices []int64) {
	t.Helper()
	for i := 0; i < p.N; i++ {
		if p.Locked[i] && prices[i] != p.A[i] {
			t.Errorf("locked item %d changed: %d -> %d", i+1, p.A[i], prices[i])
		}
	}
	for k, c := range p.Cons {
		d := prices[c.V] - prices[c.U]
		if c.HasHi && d > c.Hi {
			t.Errorf("constraint #%d violated: p%d - p%d = %d > %d", k+1, c.V+1, c.U+1, d, c.Hi)
		}
		if c.HasLo && d < c.Lo {
			t.Errorf("constraint #%d violated: p%d - p%d = %d < %d", k+1, c.V+1, c.U+1, d, c.Lo)
		}
	}
}

func verifyCycle(t *testing.T, p Problem, s Solution) {
	t.Helper()
	cyc := s.Cycle
	if len(cyc) == 0 {
		t.Fatal("infeasible but empty certificate")
	}
	var sum int64
	for k, e := range cyc {
		sum += e.w
		nxt := cyc[(k+1)%len(cyc)]
		if e.to != nxt.from {
			t.Fatalf("certificate is not a cycle: edge %d ends at %d, next starts at %d", k, e.to, nxt.from)
		}
		switch e.kind {
		case edgeConHi:
			c := p.Cons[e.idx]
			if e.from != c.U || e.to != c.V || e.w != c.Hi {
				t.Fatalf("bad constraint-hi edge in certificate: %+v", e)
			}
		case edgeConLo:
			c := p.Cons[e.idx]
			if e.from != c.V || e.to != c.U || e.w != -c.Lo {
				t.Fatalf("bad constraint-lo edge in certificate: %+v", e)
			}
		case edgeFixHi:
			if e.from != p.N || e.to != e.idx || e.w != p.A[e.idx] || !p.Locked[e.idx] {
				t.Fatalf("bad fixed-hi edge in certificate: %+v", e)
			}
		case edgeFixLo:
			if e.to != p.N || e.from != e.idx || e.w != -p.A[e.idx] || !p.Locked[e.idx] {
				t.Fatalf("bad fixed-lo edge in certificate: %+v", e)
			}
		}
	}
	if sum >= 0 {
		t.Fatalf("certificate cycle sum %d is not negative", sum)
	}
	if sum != s.CycleSum {
		t.Fatalf("reported cycle sum %d != actual %d", s.CycleSum, sum)
	}
}

func randomProblem(rng *rand.Rand, n int) Problem {
	p := Problem{N: n, A: make([]int64, n), Locked: make([]bool, n)}
	for i := range p.A {
		p.A[i] = int64(rng.Intn(5))
		p.Locked[i] = rng.Intn(3) == 0
	}
	m := rng.Intn(2 * n)
	for k := 0; k < m; k++ {
		u := rng.Intn(n)
		v := rng.Intn(n)
		lo := int64(rng.Intn(9) - 4)
		hi := int64(rng.Intn(9) - 4)
		if lo > hi {
			lo, hi = hi, lo
		}
		c := Constraint{U: u, V: v, Lo: lo, Hi: hi, HasLo: true, HasHi: true}
		if rng.Intn(4) == 0 {
			c.HasLo = false
		}
		if rng.Intn(4) == 0 {
			c.HasHi = false
		}
		if !c.HasLo && !c.HasHi {
			c.HasHi = true
		}
		p.Cons = append(p.Cons, c)
	}
	return p
}

func runAgainstBruteForce(t *testing.T, p Problem) {
	t.Helper()
	const B = 26
	wantFeas, wantCh, wantDev := bruteForce(p, B)
	sol := Solve(p)
	if sol.Feasible != wantFeas {
		t.Fatalf("feasibility mismatch: solver=%v brute=%v\nproblem: %+v", sol.Feasible, wantFeas, p)
	}
	if !wantFeas {
		verifyCycle(t, p, sol)
		return
	}
	if sol.Changes != wantCh {
		t.Fatalf("changes mismatch: solver=%d brute=%d\nproblem: %+v", sol.Changes, wantCh, p)
	}
	if sol.Deviation != wantDev {
		t.Fatalf("deviation mismatch: solver=%d brute=%d\nproblem: %+v", sol.Deviation, wantDev, p)
	}
	checkPrices(t, p, sol.Prices)
	gotCh := 0
	var gotDev int64
	for i := 0; i < p.N; i++ {
		if sol.Prices[i] != p.A[i] {
			gotCh++
			gotDev += abs64(sol.Prices[i] - p.A[i])
		}
	}
	if gotCh != sol.Changes || gotDev != sol.Deviation {
		t.Fatalf("reported (%d changes, dev %d) but prices give (%d, %d)",
			sol.Changes, sol.Deviation, gotCh, gotDev)
	}
}

func TestRandomSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 150; iter++ {
		p := randomProblem(rng, 1+rng.Intn(3)) // n in 1..3
		runAgainstBruteForce(t, p)
	}
}

func TestRandomN4(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 25; iter++ {
		p := randomProblem(rng, 4)
		runAgainstBruteForce(t, p)
	}
}

// TestRandomHeavy is a long randomized cross-check against brute force;
// run explicitly with: go test -run TestRandomHeavy
func TestRandomHeavy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy randomized test in short mode")
	}
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 2000; iter++ {
		p := randomProblem(rng, 1+rng.Intn(3))
		runAgainstBruteForce(t, p)
	}
	for iter := 0; iter < 150; iter++ {
		p := randomProblem(rng, 4)
		runAgainstBruteForce(t, p)
	}
}

func TestLockedConflictCertificate(t *testing.T) {
	// Both items locked at 5, but 1 <= p2 - p1 <= 3 is required.
	p := Problem{
		N:      2,
		A:      []int64{5, 5},
		Locked: []bool{true, true},
		Cons:   []Constraint{{U: 0, V: 1, Lo: 1, Hi: 3, HasLo: true, HasHi: true}},
	}
	sol := Solve(p)
	if sol.Feasible {
		t.Fatal("expected INFEASIBLE")
	}
	verifyCycle(t, p, sol)
}

func TestNoConstraints(t *testing.T) {
	p := Problem{
		N:      3,
		A:      []int64{4, 7, 1},
		Locked: []bool{false, true, false},
	}
	sol := Solve(p)
	if !sol.Feasible || sol.Changes != 0 || sol.Deviation != 0 {
		t.Fatalf("expected no-change solution, got %+v", sol)
	}
	for i, v := range sol.Prices {
		if v != p.A[i] {
			t.Fatalf("price %d changed without need: %v", i, sol.Prices)
		}
	}
}

// TestLexicographicOrder makes sure the solver minimizes the change COUNT
// first: the minimum total adjustment (10) is also achievable by changing
// two items at once, but the answer must change exactly one.
func TestLexicographicOrder(t *testing.T) {
	// a = (0, 100), constraint p2 - p1 <= 90.
	// Keep item1: p2 = 90, dev 10. Keep item2: p1 = 10, dev 10.
	// Changing both (e.g. p1=5, p2=95) also costs 10, but 1 change wins.
	p := Problem{
		N:      2,
		A:      []int64{0, 100},
		Locked: []bool{false, false},
		Cons:   []Constraint{{U: 0, V: 1, Hi: 90, HasHi: true}},
	}
	sol := Solve(p)
	if !sol.Feasible || sol.Changes != 1 || sol.Deviation != 10 {
		t.Fatalf("got %+v, want 1 change with deviation 10", sol)
	}
	checkPrices(t, p, sol.Prices)
}

// TestChainForcesTwoChanges: p2 >= p1+5 and p3 >= p2+5 cannot be repaired by
// changing any single item, so 2 changes are required; the best repair is
// (-5, 0, 5) with total deviation 10.
func TestChainForcesTwoChanges(t *testing.T) {
	p := Problem{
		N:      3,
		A:      []int64{0, 0, 0},
		Locked: []bool{false, false, false},
		Cons: []Constraint{
			{U: 0, V: 1, Lo: 5, HasLo: true},
			{U: 1, V: 2, Lo: 5, HasLo: true},
		},
	}
	sol := Solve(p)
	if !sol.Feasible || sol.Changes != 2 || sol.Deviation != 10 {
		t.Fatalf("got %+v, want 2 changes with deviation 10", sol)
	}
	checkPrices(t, p, sol.Prices)
}

// TestEighteenItems exercises the enumeration near the size limit: 9
// conflicting pairs, each forcing at least one change -> exactly 9 changes.
func TestEighteenItems(t *testing.T) {
	n := 18
	p := Problem{N: n, A: make([]int64, n), Locked: make([]bool, n)}
	for i := 0; i < n; i++ {
		p.A[i] = int64(i)
	}
	for p2 := 0; p2 < 9; p2++ {
		u, v := 2*p2, 2*p2+1
		want := p.A[v] - p.A[u] + 3 // force a difference off by 3
		p.Cons = append(p.Cons, Constraint{U: u, V: v, Lo: want, Hi: want, HasLo: true, HasHi: true})
	}
	sol := Solve(p)
	if !sol.Feasible {
		t.Fatalf("expected feasible, got certificate %+v", sol.Cycle)
	}
	if sol.Changes != 9 {
		t.Fatalf("expected 9 changes, got %d", sol.Changes)
	}
	if sol.Deviation != 27 { // each pair: shift one endpoint by 3
		t.Fatalf("expected deviation 27, got %d", sol.Deviation)
	}
	checkPrices(t, p, sol.Prices)
}
