package psdcomp

import (
	"math/big"
	"math/rand"
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

func matFromStrings(t *testing.T, rows ...[]string) *Matrix {
	t.Helper()
	n := len(rows)
	m := NewMatrix(n)
	for i, row := range rows {
		if len(row) != n {
			t.Fatalf("ragged matrix")
		}
		for j, s := range row {
			m.A[i][j] = rat(t, s)
		}
	}
	return m
}

func TestDetKnownValues(t *testing.T) {
	cases := []struct {
		rows [][]string
		want string
	}{
		{[][]string{{"5"}}, "5"},
		{[][]string{{"1", "2"}, {"3", "4"}}, "-2"},
		{[][]string{{"1/2", "1/3"}, {"1/4", "1/5"}}, "1/60"}, // 1/10 - 1/12
		{[][]string{{"1", "2", "2"}, {"2", "1", "2"}, {"2", "2", "1"}}, "5"},
		{[][]string{{"0", "1"}, {"1", "0"}}, "-1"},
		{[][]string{{"2", "0", "0", "0"}, {"0", "3", "0", "0"}, {"0", "0", "1/2", "0"}, {"0", "0", "0", "7"}}, "21"},
		{[][]string{{"1", "1", "1", "1"}, {"1", "1", "1", "1"}, {"1", "1", "1", "1"}, {"1", "1", "1", "1"}}, "0"},
	}
	for i, c := range cases {
		m := matFromStrings(t, c.rows...)
		if got := DetBareiss(m); got.Cmp(rat(t, c.want)) != 0 {
			t.Errorf("case %d: DetBareiss = %s, want %s", i, got.RatString(), c.want)
		}
		if got := DetGauss(m); got.Cmp(rat(t, c.want)) != 0 {
			t.Errorf("case %d: DetGauss = %s, want %s", i, got.RatString(), c.want)
		}
	}
}

func randomRat(r *rand.Rand, maxInt int64) *big.Rat {
	num := r.Int63n(2*maxInt+1) - maxInt
	den := r.Int63n(maxInt) + 1
	return big.NewRat(num, den)
}

func randomSymmetric(r *rand.Rand, n int) *Matrix {
	m := NewMatrix(n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			m.Set(i, j, randomRat(r, 5))
		}
	}
	return m
}

func TestDetAlgorithmsAgree(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		n := 1 + r.Intn(MaxN)
		m := randomSymmetric(r, n)
		b := DetBareiss(m)
		g := DetGauss(m)
		if b.Cmp(g) != 0 {
			t.Fatalf("trial %d: bareiss=%s gauss=%s", trial, b.RatString(), g.RatString())
		}
	}
}
