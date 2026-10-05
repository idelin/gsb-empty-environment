package exactmat

import (
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// bareissDet is an independent determinant oracle: fraction-free Bareiss
// elimination with full pivoting, a different algorithm from the library's
// Gaussian elimination, used to cross-check every maintained determinant.
func bareissDet(a [][]*big.Rat) *big.Rat {
	n := len(a)
	m := copyMat(a)
	sign := int64(1)
	prev := big.NewRat(1, 1)
	for k := 0; k < n-1; k++ {
		pr, pc := -1, -1
		for r := k; r < n && pr < 0; r++ {
			for c := k; c < n; c++ {
				if m[r][c].Sign() != 0 {
					pr, pc = r, c
					break
				}
			}
		}
		if pr < 0 {
			return big.NewRat(0, 1)
		}
		if pr != k {
			m[k], m[pr] = m[pr], m[k]
			sign = -sign
		}
		if pc != k {
			for r := 0; r < n; r++ {
				m[r][k], m[r][pc] = m[r][pc], m[r][k]
			}
			sign = -sign
		}
		for i := k + 1; i < n; i++ {
			for j := k + 1; j < n; j++ {
				t := new(big.Rat).Mul(m[i][j], m[k][k])
				t.Sub(t, new(big.Rat).Mul(m[i][k], m[k][j]))
				t.Quo(t, prev)
				m[i][j] = t
			}
		}
		prev = new(big.Rat).Set(m[k][k])
	}
	det := new(big.Rat).Set(m[n-1][n-1])
	if sign < 0 {
		det.Neg(det)
	}
	return det
}

// echelonRank is an independent rank oracle using plain row echelon form.
func echelonRank(a [][]*big.Rat) int {
	m := copyMat(a)
	n := len(m)
	cols := len(m[0])
	rank := 0
	for col := 0; col < cols && rank < n; col++ {
		piv := -1
		for r := rank; r < n; r++ {
			if m[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		m[rank], m[piv] = m[piv], m[rank]
		for r := rank + 1; r < n; r++ {
			if m[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(m[r][col], m[rank][col])
			for c := col; c < cols; c++ {
				m[r][c].Sub(m[r][c], new(big.Rat).Mul(f, m[rank][c]))
			}
		}
		rank++
	}
	return rank
}

func snapshot(m *Matrix) [][]*big.Rat {
	rows := make([][]*big.Rat, m.N())
	for i := range rows {
		rows[i] = m.Row(i)
	}
	return rows
}

func randRat(rng *rand.Rand) *big.Rat {
	num := int64(rng.Intn(11) - 5)
	den := int64(1 + rng.Intn(4))
	return big.NewRat(num, den)
}

func randRow(rng *rand.Rand, n int) []*big.Rat {
	row := make([]*big.Rat, n)
	for j := range row {
		row[j] = randRat(rng)
	}
	return row
}

func randMatrix(rng *rand.Rand, n int) [][]*big.Rat {
	rows := make([][]*big.Rat, n)
	for i := range rows {
		rows[i] = randRow(rng, n)
	}
	return rows
}

func TestRandomRowUpdatesCrossChecked(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 150; trial++ {
		n := 2 + rng.Intn(5)
		m, err := NewMatrix(randMatrix(rng, n))
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		for step := 0; step < 25; step++ {
			i := rng.Intn(n)
			var newRow []*big.Rat
			switch rng.Intn(5) {
			case 0: // force singularity: duplicate another row
				newRow = m.Row(rng.Intn(n))
			case 1: // force singularity: zero row
				newRow = make([]*big.Rat, n)
				for j := range newRow {
					newRow[j] = big.NewRat(0, 1)
				}
			default:
				newRow = randRow(rng, n)
			}
			if err := m.ReplaceRow(i, newRow); err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}

			snap := snapshot(m)
			wantDet := bareissDet(snap)
			if got := m.Det(); got.Cmp(wantDet) != 0 {
				t.Fatalf("trial %d step %d: det = %s, full elimination says %s",
					trial, step, got.RatString(), wantDet.RatString())
			}
			if got, want := m.Invertible(), wantDet.Sign() != 0; got != want {
				t.Fatalf("trial %d step %d: Invertible = %v, want %v", trial, step, got, want)
			}
			if got, want := m.Rank(), echelonRank(snap); got != want {
				t.Fatalf("trial %d step %d: rank = %d, want %d", trial, step, got, want)
			}

			b := randRow(rng, n)
			res, err := m.Solve(b)
			if err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
			if m.Invertible() && res.Kind != Unique {
				t.Fatalf("trial %d step %d: invertible but kind = %v", trial, step, res.Kind)
			}
			if !m.Invertible() && res.Kind == Unique {
				t.Fatalf("trial %d step %d: singular but kind = Unique", trial, step)
			}
			if !m.CheckSolution(res, b) {
				t.Fatalf("trial %d step %d: result failed substitution check:\n%s", trial, step, res)
			}
		}
	}
}

func TestZeroDenominatorFallsBackAndRecovers(t *testing.T) {
	m, err := NewFromInts([][]int64{{1, 2}, {3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Det(); got.Cmp(big.NewRat(-2, 1)) != 0 {
		t.Fatalf("det = %s, want -2", got)
	}

	// Replace row 0 with row 1: Sherman-Morrison denominator is exactly 0.
	if err := m.ReplaceRow(0, []*big.Rat{big.NewRat(3, 1), big.NewRat(4, 1)}); err != nil {
		t.Fatal(err)
	}
	if m.Invertible() {
		t.Fatal("expected singular after duplicating a row")
	}
	if got := m.Det(); got.Sign() != 0 {
		t.Fatalf("det = %s, want 0", got)
	}
	if m.Rank() != 1 {
		t.Fatalf("rank = %d, want 1", m.Rank())
	}

	// Recover to invertible from the singular state.
	if err := m.ReplaceRow(0, []*big.Rat{big.NewRat(1, 1), big.NewRat(2, 1)}); err != nil {
		t.Fatal(err)
	}
	if !m.Invertible() {
		t.Fatal("expected recovery to invertible")
	}
	if got := m.Det(); got.Cmp(big.NewRat(-2, 1)) != 0 {
		t.Fatalf("det = %s, want -2 after recovery", got)
	}
}

func TestSolveUniqueSubstitutesBack(t *testing.T) {
	m, err := NewFromInts([][]int64{{2, 1, -1}, {-3, -1, 2}, {-2, 1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	b := []*big.Rat{big.NewRat(8, 1), big.NewRat(-11, 1), big.NewRat(-3, 1)}
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Unique {
		t.Fatalf("kind = %v, want Unique", res.Kind)
	}
	want := []*big.Rat{big.NewRat(2, 1), big.NewRat(3, 1), big.NewRat(-1, 1)}
	if !vecEqual(res.X, want) {
		t.Fatalf("x = %v, want %v", res.X, want)
	}
	if !m.CheckSolution(res, b) {
		t.Fatal("solution failed substitution check")
	}
}

func TestNoSolutionEvidence(t *testing.T) {
	m, err := NewFromInts([][]int64{{1, 2}, {2, 4}})
	if err != nil {
		t.Fatal(err)
	}
	b := []*big.Rat{big.NewRat(3, 1), big.NewRat(7, 1)}
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != NoSolution {
		t.Fatalf("kind = %v, want NoSolution", res.Kind)
	}
	if !strings.Contains(res.Evidence, "0 =") {
		t.Fatalf("evidence lacks the contradictory equation: %q", res.Evidence)
	}
	if !m.CheckSolution(res, b) {
		t.Fatal("no-solution evidence failed independent rank check")
	}
}

func TestUnderdeterminedStructure(t *testing.T) {
	m, err := NewFromInts([][]int64{{1, 2}, {2, 4}})
	if err != nil {
		t.Fatal(err)
	}
	b := []*big.Rat{big.NewRat(3, 1), big.NewRat(6, 1)}
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Underdetermined {
		t.Fatalf("kind = %v, want Underdetermined", res.Kind)
	}
	if res.Rank != 1 || res.Nullity != 1 || len(res.NullSpace) != 1 {
		t.Fatalf("rank=%d nullity=%d basis=%d, want 1/1/1", res.Rank, res.Nullity, len(res.NullSpace))
	}
	if !m.CheckSolution(res, b) {
		t.Fatal("underdetermined solution failed substitution check")
	}
	// Every point on the solution line must also satisfy the system.
	lam := big.NewRat(-7, 3)
	point := make([]*big.Rat, 2)
	for j := range point {
		point[j] = new(big.Rat).Add(res.X[j], new(big.Rat).Mul(lam, res.NullSpace[0][j]))
	}
	if !vecEqual(matVec(snapshot(m), point), b) {
		t.Fatal("affine combination of particular + null space is not a solution")
	}
}

func hilbert(n int) [][]*big.Rat {
	rows := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		rows[i] = make([]*big.Rat, n)
		for j := 0; j < n; j++ {
			rows[i][j] = big.NewRat(1, int64(i+j+1))
		}
	}
	return rows
}

func TestTinyDeterminantIsNotZero(t *testing.T) {
	// det(H_8) ~ 2.7e-33: far below any float tolerance, but exactly nonzero.
	m, err := NewMatrix(hilbert(8))
	if err != nil {
		t.Fatal(err)
	}
	if !m.Invertible() {
		t.Fatal("Hilbert-8 must be invertible under exact arithmetic")
	}
	if m.Det().Sign() <= 0 {
		t.Fatalf("det = %s, want a positive exact rational", m.Det().RatString())
	}
	if got, want := m.Det(), bareissDet(snapshot(m)); got.Cmp(want) != 0 {
		t.Fatalf("det = %s, Bareiss says %s", got.RatString(), want.RatString())
	}

	b := make([]*big.Rat, 8)
	for i := range b {
		b[i] = big.NewRat(1, 1)
	}
	res, err := m.Solve(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Unique || !m.CheckSolution(res, b) {
		t.Fatalf("unexpected solve outcome: %v", res.Kind)
	}

	// A near-cancellation row replacement: new row 0 differs from row 1 of H
	// by a tiny rational in one entry. The result stays exactly invertible.
	row := m.Row(1)
	row[0] = new(big.Rat).Add(row[0], big.NewRat(1, 1))
	row[0].Quo(row[0], big.NewRat(1<<40, 1))
	if err := m.ReplaceRow(0, row); err != nil {
		t.Fatal(err)
	}
	if !m.Invertible() {
		t.Fatal("tiny perturbation must not be rounded to singular")
	}
	if got, want := m.Det(), bareissDet(snapshot(m)); got.Cmp(want) != 0 {
		t.Fatalf("det = %s, Bareiss says %s", got.RatString(), want.RatString())
	}
}

func TestExactRationalsNoFloatTraps(t *testing.T) {
	// det = 1/10^30 - 1: a float computation near the pivot could suggest 0
	// or -1; exact arithmetic keeps the tiny term.
	pow30 := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	tiny := new(big.Rat).SetFrac(big.NewInt(1), pow30)
	m, err := NewMatrix([][]*big.Rat{
		{new(big.Rat).Set(tiny), big.NewRat(1, 1)},
		{big.NewRat(1, 1), big.NewRat(1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Rat).Sub(tiny, big.NewRat(1, 1))
	if got := m.Det(); got.Cmp(want) != 0 {
		t.Fatalf("det = %s, want %s", got.RatString(), want.RatString())
	}
	if !m.Invertible() {
		t.Fatal("matrix with det = 1/10^30 - 1 must be invertible")
	}

	// Replacing a row to make the determinant exactly 1/10^30.
	if err := m.ReplaceRow(1, []*big.Rat{big.NewRat(0, 1), big.NewRat(1, 1)}); err != nil {
		t.Fatal(err)
	}
	if got := m.Det(); got.Cmp(tiny) != 0 {
		t.Fatalf("det = %s, want exactly 1/10^30", got.RatString())
	}
	if !m.Invertible() {
		t.Fatal("det = 1/10^30 is nonzero and must not be treated as zero")
	}
}

func TestInputValidation(t *testing.T) {
	if _, err := NewMatrix(nil); err == nil {
		t.Fatal("expected error for empty matrix")
	}
	if _, err := NewFromInts([][]int64{{1, 2}, {3}}); err == nil {
		t.Fatal("expected error for non-square input")
	}
	m, err := NewFromInts([][]int64{{1, 0}, {0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceRow(2, []*big.Rat{big.NewRat(1, 1), big.NewRat(0, 1)}); err == nil {
		t.Fatal("expected error for out-of-range row index")
	}
	if err := m.ReplaceRow(0, []*big.Rat{big.NewRat(1, 1)}); err == nil {
		t.Fatal("expected error for wrong row length")
	}
	if _, err := m.Solve([]*big.Rat{big.NewRat(1, 1)}); err == nil {
		t.Fatal("expected error for wrong rhs length")
	}
	// Failed calls must leave the matrix untouched.
	if got := m.Det(); got.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("det = %s after rejected calls, want 1", got)
	}
}
