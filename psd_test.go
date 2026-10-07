package psdcomp

import (
	"math/big"
	"math/rand"
	"testing"
)

// The canonical counterexample against "diagonal >= 0 and det >= 0
// suffices": [[1,2,2],[2,1,2],[2,2,1]] has positive diagonal and
// determinant 5 > 0, yet the principal minor on {0,1} equals -3.
func TestDiagAndDetAreNotEnough(t *testing.T) {
	m := matFromStrings(t,
		[]string{"1", "2", "2"},
		[]string{"2", "1", "2"},
		[]string{"2", "2", "1"},
	)
	for i := 0; i < 3; i++ {
		if m.Get(i, i).Sign() <= 0 {
			t.Fatalf("test premise broken: diagonal not positive")
		}
	}
	if DetBareiss(m).Sign() <= 0 {
		t.Fatalf("test premise broken: determinant not positive")
	}
	if IsPSDMinors(m) {
		t.Fatalf("minors criterion wrongly accepts an indefinite matrix")
	}
	if IsPSDElimination(m) {
		t.Fatalf("elimination criterion wrongly accepts an indefinite matrix")
	}
	// The violating minor must be reported in the certificate data.
	found := false
	for _, rep := range PrincipalMinors(m, DetBareiss) {
		if rep.Det.Sign() < 0 {
			found = true
			if len(rep.Indices) != 2 || rep.Det.Cmp(big.NewRat(-3, 1)) != 0 {
				t.Fatalf("unexpected negative minor %v", rep)
			}
		}
	}
	if !found {
		t.Fatalf("no negative principal minor reported")
	}
}

func TestEliminationEdgeCases(t *testing.T) {
	cases := []struct {
		rows [][]string
		psd  bool
	}{
		{[][]string{{"0"}}, true},
		{[][]string{{"0", "0"}, {"0", "0"}}, true},
		{[][]string{{"0", "0"}, {"0", "1"}}, true},  // zero pivot, PSD
		{[][]string{{"0", "1"}, {"1", "0"}}, false}, // zero pivot, indefinite
		{[][]string{{"0", "1"}, {"1", "5"}}, false}, // det -1
		{[][]string{{"1", "0"}, {"0", "0"}}, true},
		{[][]string{{"-1", "0"}, {"0", "2"}}, false}, // negative pivot
		{[][]string{{"2", "1", "0"}, {"1", "2", "1"}, {"0", "1", "2"}}, true},
		{[][]string{{"1", "1", "1"}, {"1", "1", "1"}, {"1", "1", "1"}}, true}, // rank 1 PSD
	}
	for i, c := range cases {
		m := matFromStrings(t, c.rows...)
		if got := IsPSDElimination(m); got != c.psd {
			t.Errorf("case %d: IsPSDElimination = %v, want %v", i, got, c.psd)
		}
		if got := IsPSDMinors(m); got != c.psd {
			t.Errorf("case %d: IsPSDMinors = %v, want %v", i, got, c.psd)
		}
	}
}

// The two independent PSD predicates must agree on random symmetric
// matrices, and must both accept every matrix of the form A^T A.
func TestPSDPredicatesAgree(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 1000; trial++ {
		n := 1 + r.Intn(MaxN)
		m := randomSymmetric(r, n)
		a, b := IsPSDMinors(m), IsPSDElimination(m)
		if a != b {
			t.Fatalf("trial %d: minors=%v elimination=%v\n%s", trial, a, b, m.String())
		}
	}
	for trial := 0; trial < 300; trial++ {
		n := 1 + r.Intn(MaxN)
		a := NewMatrix(n)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				a.A[i][j] = randomRat(r, 4)
			}
		}
		// m = A^T A is always PSD.
		m := NewMatrix(n)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				acc := new(big.Rat)
				for k := 0; k < n; k++ {
					acc.Add(acc, new(big.Rat).Mul(a.A[k][i], a.A[k][j]))
				}
				m.Set(i, j, acc)
			}
		}
		if !IsPSDMinors(m) {
			t.Fatalf("trial %d: minors criterion rejects A^T A", trial)
		}
		if !IsPSDElimination(m) {
			t.Fatalf("trial %d: elimination criterion rejects A^T A", trial)
		}
	}
}
