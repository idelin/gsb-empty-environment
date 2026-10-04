package zerosum

import (
	"math/big"
	"math/rand"
	"testing"
)

func mustGame(t *testing.T, a [][]int64) *Game {
	t.Helper()
	g, err := NewGameFromInts(a)
	if err != nil {
		t.Fatalf("NewGameFromInts: %v", err)
	}
	return g
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

// checkSolution asserts the solution is an exact, verifiable optimum with
// the expected value, and that its certificate bounds coincide.
func checkSolution(t *testing.T, g *Game, s *Solution, wantValue string) {
	t.Helper()
	if s.Value.Cmp(rat(wantValue)) != 0 {
		t.Fatalf("value = %s, want %s", s.Value, wantValue)
	}
	if !isDistribution(s.Row) {
		t.Fatalf("row strategy is not a probability distribution: %v", s.Row)
	}
	if !isDistribution(s.Col) {
		t.Fatalf("column strategy is not a probability distribution: %v", s.Col)
	}
	cert, ok := g.Verify(s.Row, s.Col, s.Value)
	if !ok {
		t.Fatalf("solution failed verification: %s", cert.String())
	}
	if cert.Lower.Cmp(s.Value) != 0 || cert.Upper.Cmp(s.Value) != 0 {
		t.Fatalf("certificate bounds %s .. %s do not meet at value %s",
			cert.Lower, cert.Upper, s.Value)
	}
}

// checkBothSolvers runs the simplex solver and support enumeration and
// requires them to agree exactly on the value.
func checkBothSolvers(t *testing.T, g *Game, wantValue string) *Solution {
	t.Helper()
	s := g.Solve()
	checkSolution(t, g, s, wantValue)
	enum := g.EnumerateOptima()
	if len(enum) == 0 {
		t.Fatalf("support enumeration found no optimal solution")
	}
	for _, es := range enum {
		checkSolution(t, g, es, wantValue)
	}
	if enum[0].Value.Cmp(s.Value) != 0 {
		t.Fatalf("simplex value %s != enumeration value %s", s.Value, enum[0].Value)
	}
	return s
}

func TestPureSaddlePoint(t *testing.T) {
	g := mustGame(t, [][]int64{
		{2, 3},
		{1, 4},
	})
	row, col, v, ok := g.SaddlePoint()
	if !ok || row != 0 || col != 0 || v.Cmp(rat("2")) != 0 {
		t.Fatalf("saddle point = (%d,%d,%v,%v)", row, col, v, ok)
	}
	s := checkBothSolvers(t, g, "2")
	// The optimal strategies must be pure.
	if s.Row[0].Cmp(rat("1")) != 0 || s.Row[1].Sign() != 0 {
		t.Fatalf("row strategy not pure: %v", s.Row)
	}
	if s.Col[0].Cmp(rat("1")) != 0 || s.Col[1].Sign() != 0 {
		t.Fatalf("column strategy not pure: %v", s.Col)
	}
}

func TestMatchingPennies(t *testing.T) {
	g := mustGame(t, [][]int64{
		{1, -1},
		{-1, 1},
	})
	if _, _, _, ok := g.SaddlePoint(); ok {
		t.Fatal("matching pennies has no saddle point")
	}
	s := checkBothSolvers(t, g, "0")
	half := rat("1/2")
	for _, p := range s.Row {
		if p.Cmp(half) != 0 {
			t.Fatalf("row strategy = %v, want uniform", s.Row)
		}
	}
	for _, p := range s.Col {
		if p.Cmp(half) != 0 {
			t.Fatalf("column strategy = %v, want uniform", s.Col)
		}
	}
}

func TestRockPaperScissors(t *testing.T) {
	g := mustGame(t, [][]int64{
		{0, -1, 1},
		{1, 0, -1},
		{-1, 1, 0},
	})
	s := checkBothSolvers(t, g, "0")
	third := rat("1/3")
	for i, p := range s.Row {
		if p.Cmp(third) != 0 {
			t.Fatalf("row[%d] = %s, want 1/3", i, p)
		}
	}
}

func TestAllNegativePayoffs(t *testing.T) {
	g := mustGame(t, [][]int64{
		{-5, -2},
		{-3, -4},
	})
	// Exact mixed solution: v = -7/2, x = (1/4, 3/4), y = (1/2, 1/2).
	s := checkBothSolvers(t, g, "-7/2")
	if s.Row[0].Cmp(rat("1/4")) != 0 || s.Row[1].Cmp(rat("3/4")) != 0 {
		t.Fatalf("row strategy = %v, want [1/4 3/4]", s.Row)
	}
	if s.Col[0].Cmp(rat("1/2")) != 0 || s.Col[1].Cmp(rat("1/2")) != 0 {
		t.Fatalf("column strategy = %v, want [1/2 1/2]", s.Col)
	}
}

func TestAllNegativeSaddle(t *testing.T) {
	g := mustGame(t, [][]int64{
		{-4, -1},
		{-7, -3},
	})
	// Row mins: -4, -7 -> max-min = -4 (row 0).
	// Col maxes: -4, -1 -> min-max = -4 (col 0). Saddle at (0,0).
	checkBothSolvers(t, g, "-4")
}

func TestDuplicateRowsAndColumns(t *testing.T) {
	g := mustGame(t, [][]int64{
		{1, -1, -1},
		{-1, 1, 1},
		{1, -1, -1}, // duplicate of row 0; cols 1,2 identical
	})
	s := checkBothSolvers(t, g, "0")
	if len(s.Row) != 3 || len(s.Col) != 3 {
		t.Fatalf("strategy dimensions wrong: %d x %d", len(s.Row), len(s.Col))
	}
}

func TestMultipleOptima(t *testing.T) {
	// Row player has a continuum of optima: any mix of rows 0,1,2 with
	// equal weight on rows 0 and 1 guarantees v = 1.
	g := mustGame(t, [][]int64{
		{2, 0},
		{0, 2},
		{1, 1},
	})
	s := checkBothSolvers(t, g, "1")
	if got := g.rowGuarantee(s.Row); got.Cmp(rat("1")) != 0 {
		t.Fatalf("row guarantee = %s, want 1", got)
	}
	enum := g.EnumerateOptima()
	if len(enum) < 2 {
		t.Fatalf("expected multiple distinct optimal strategies, got %d", len(enum))
	}
	// Distinct optima must actually differ as strategies.
	seen := make(map[string]bool)
	for _, es := range enum {
		seen[solutionKey(es)] = true
	}
	if len(seen) != len(enum) {
		t.Fatal("enumeration returned duplicate solutions")
	}
}

func TestSingleCell(t *testing.T) {
	g := mustGame(t, [][]int64{{-3}})
	checkBothSolvers(t, g, "-3")
}

func TestNonSquare(t *testing.T) {
	// 3x2 game, value 2 with uniform mixes on the first two rows/columns.
	g := mustGame(t, [][]int64{
		{3, 1},
		{1, 3},
		{2, 2},
	})
	checkBothSolvers(t, g, "2")

	// 2x3 game: column player should never use the dominated column.
	g2 := mustGame(t, [][]int64{
		{4, 0, 3},
		{0, 4, 3},
	})
	checkBothSolvers(t, g2, "2")
}

func TestDominatedColumnDiscarded(t *testing.T) {
	// Column 2 is weakly dominated by a 1/3-2/3 mix of columns 0,1;
	// the simplex optimum must not need it.
	g := mustGame(t, [][]int64{
		{1, 4, 3},
		{5, 2, 3},
	})
	s := checkBothSolvers(t, g, "3")
	if s.Col[2].Sign() != 0 {
		t.Fatalf("dominated column played with probability %s", s.Col[2])
	}
}

func TestVerifyRejectsFalseClaims(t *testing.T) {
	g := mustGame(t, [][]int64{
		{1, -1},
		{-1, 1},
	})
	x := []*big.Rat{rat("1/2"), rat("1/2")}
	y := []*big.Rat{rat("1/2"), rat("1/2")}

	if _, ok := g.Verify(x, y, rat("1")); ok {
		t.Fatal("accepted inflated value claim")
	}
	if _, ok := g.Verify(x, y, rat("-1")); ok {
		t.Fatal("accepted deflated value claim")
	}
	// Not a distribution: sums to 1/2.
	bad := []*big.Rat{rat("1/4"), rat("1/4")}
	if _, ok := g.Verify(bad, y, rat("0")); ok {
		t.Fatal("accepted strategy not summing to 1")
	}
	// Not a distribution: negative entry.
	neg := []*big.Rat{rat("3/2"), rat("-1/2")}
	if _, ok := g.Verify(neg, y, rat("0")); ok {
		t.Fatal("accepted strategy with negative probability")
	}
	// Suboptimal row strategy: pure row 0 only guarantees -1.
	pure := []*big.Rat{rat("1"), rat("0")}
	if _, ok := g.Verify(pure, y, rat("0")); ok {
		t.Fatal("accepted suboptimal row strategy")
	}
	// Certificate of the true optimum must be accepted.
	if _, ok := g.Verify(x, y, rat("0")); !ok {
		t.Fatal("rejected the true optimum")
	}
}

func TestCertificateIsSelfContained(t *testing.T) {
	g := mustGame(t, [][]int64{
		{0, 3, -2},
		{-1, 0, 4},
		{2, -3, 0},
	})
	s := g.Solve()
	// Recompute the bounds from scratch, as an independent verifier would.
	lower := g.rowGuarantee(s.Cert.Row)
	upper := g.colCap(s.Cert.Col)
	if lower.Cmp(s.Cert.Lower) != 0 || upper.Cmp(s.Cert.Upper) != 0 {
		t.Fatal("certificate bounds are not reproducible")
	}
	if lower.Cmp(upper) != 0 {
		t.Fatalf("bounds do not coincide: %s vs %s", lower, upper)
	}
}

func TestRandomGamesSolversAgree(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		m := 1 + rng.Intn(4)
		n := 1 + rng.Intn(4)
		a := make([][]int64, m)
		for i := range a {
			a[i] = make([]int64, n)
			for j := range a[i] {
				// Include negatives and duplicates-heavy range.
				a[i][j] = int64(rng.Intn(9) - 4)
			}
		}
		g := mustGame(t, a)
		s := g.Solve()
		checkSolution(t, g, s, s.Value.RatString())
		enum := g.EnumerateOptima()
		if len(enum) == 0 {
			t.Fatalf("trial %d: enumeration found nothing (matrix %v)", trial, a)
		}
		for _, es := range enum {
			if es.Value.Cmp(s.Value) != 0 {
				t.Fatalf("trial %d: enumeration value %s != simplex value %s (matrix %v)",
					trial, es.Value, s.Value, a)
			}
			checkSolution(t, g, es, s.Value.RatString())
		}
		// Value must lie within the pure-strategy security bounds.
		lo, hi := g.ValueBounds()
		if s.Value.Cmp(lo) < 0 || s.Value.Cmp(hi) > 0 {
			t.Fatalf("trial %d: value %s outside [%s, %s]", trial, s.Value, lo, hi)
		}
	}
}

func TestRationalInput(t *testing.T) {
	g, err := NewGame([][]*big.Rat{
		{rat("1/3"), rat("-2/5")},
		{rat("-1/7"), rat("3/11")},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := g.Solve()
	checkSolution(t, g, s, s.Value.RatString())
	enum := g.EnumerateOptima()
	if len(enum) == 0 || enum[0].Value.Cmp(s.Value) != 0 {
		t.Fatal("enumeration disagrees on rational input")
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := NewGame(nil); err == nil {
		t.Fatal("accepted empty matrix")
	}
	if _, err := NewGame([][]*big.Rat{{rat("1")}, {rat("1"), rat("2")}}); err == nil {
		t.Fatal("accepted ragged matrix")
	}
	if _, err := NewGame([][]*big.Rat{{nil}}); err == nil {
		t.Fatal("accepted nil entry")
	}
}
