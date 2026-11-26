package admission

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func matFromInts(rows ...[]int64) *intMatrix {
	m := newIntMatrix(len(rows), len(rows[0]))
	for i := range rows {
		for j := range rows[i] {
			m.A[i][j].SetInt64(rows[i][j])
		}
	}
	return m
}

func matEqual(a, b *intMatrix) bool {
	if a.Rows != b.Rows || a.Cols != b.Cols {
		return false
	}
	for i := 0; i < a.Rows; i++ {
		for j := 0; j < a.Cols; j++ {
			if a.A[i][j].Cmp(b.A[i][j]) != 0 {
				return false
			}
		}
	}
	return true
}

func matString(m *intMatrix) string {
	s := ""
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			if j > 0 {
				s += " "
			}
			s += m.A[i][j].String()
		}
		s += "\n"
	}
	return s
}

func minorGCDs(m *intMatrix) []*big.Int {
	maxK := minInt(m.Rows, m.Cols)
	out := make([]*big.Int, maxK+1)
	out[0] = big.NewInt(1)
	rows := make([]int, maxK)
	cols := make([]int, maxK)
	var rec func(k, startR, startC int) *big.Int
	rec = func(k, startR, startC int) *big.Int {
		if k == 0 {
			return big.NewInt(1)
		}
		g := new(big.Int)
		var chooseR func(depth, from int)
		chooseR = func(depth, from int) {
			if depth == k {
				var chooseC func(d2, from2 int)
				chooseC = func(d2, from2 int) {
					if d2 == k {
						det := determinantSmall(m, rows, cols, k)
						abs := new(big.Int).Abs(det)
						g.GCD(nil, nil, g, abs)
						return
					}
					for j := from2; j <= m.Cols-(k-d2); j++ {
						cols[d2] = j
						chooseC(d2+1, j+1)
					}
				}
				chooseC(0, 0)
				return
			}
			for i := from; i <= m.Rows-(k-depth); i++ {
				rows[depth] = i
				chooseR(depth+1, i+1)
			}
		}
		chooseR(0, 0)
		return g
	}
	for k := 1; k <= maxK; k++ {
		out[k] = rec(k, 0, 0)
	}
	return out
}

func determinantSmall(m *intMatrix, rows, cols []int, k int) *big.Int {
	if k == 1 {
		return new(big.Int).Set(m.A[rows[0]][cols[0]])
	}
	det := new(big.Int)
	for j := 0; j < k; j++ {
		subRows := make([]int, k-1)
		subCols := make([]int, k-1)
		copy(subRows, rows[1:])
		idx := 0
		for c := 0; c < k; c++ {
			if c == j {
				continue
			}
			subCols[idx] = cols[c]
			idx++
		}
		term := new(big.Int).Mul(m.A[rows[0]][cols[j]], determinantSmall(m, subRows, subCols, k-1))
		if j%2 == 0 {
			det.Add(det, term)
		} else {
			det.Sub(det, term)
		}
	}
	return det
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestSmithNormalFormKnown(t *testing.T) {
	cases := [][][]int64{
		{{2, 4}, {6, 8}},
		{{2, 0}, {0, 6}},
		{{2, 3, 5}},
		{{2}, {3}, {5}},
		{{0, 0}, {0, 0}},
		{{-2, 4}, {6, -8}},
	}
	for _, c := range cases {
		in := matFromInts(c...)
		d, u, v, err := smithNormalForm(in)
		if err != nil {
			t.Fatalf("snf(%v): %v", c, err)
		}
		got := matMul(matMul(u, in), v)
		if !matEqual(d, got) {
			t.Fatalf("UAV != D for %v\nD=\n%sUAV=\n%s", c, matString(d), matString(got))
		}
		for i := 1; i < minInt(d.Rows, d.Cols); i++ {
			if d.A[i-1][i-1].Sign() != 0 && new(big.Int).Mod(d.A[i][i], d.A[i-1][i-1]).Sign() != 0 {
				t.Fatalf("invariant factors not divisibility ordered: %s", matString(d))
			}
		}
	}
}

func TestSmithNormalFormAgainstMinors(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		r := 1 + rng.Intn(4)
		c := 1 + rng.Intn(4)
		m := newIntMatrix(r, c)
		for i := 0; i < r; i++ {
			for j := 0; j < c; j++ {
				m.A[i][j].SetInt64(int64(rng.Intn(11) - 5))
			}
		}
		d, u, v, err := smithNormalForm(m)
		if err != nil {
			t.Fatalf("iter %d snf: %v\n%s", iter, err, matString(m))
		}
		got := matMul(matMul(u, m), v)
		if !matEqual(d, got) {
			t.Fatalf("iter %d UAV mismatch\nA=\n%sD=\n%sUAV=\n%s", iter, matString(m), matString(d), matString(got))
		}
		gcds := minorGCDs(m)
		for k := 1; k <= minInt(r, c); k++ {
			prev := new(big.Int).Set(gcds[k-1])
			if prev.Sign() == 0 {
				t.Fatalf("iter %d zero determinantal divisor", iter)
			}
			want := new(big.Int).Quo(gcds[k], prev)
			if want.Sign() < 0 {
				want.Neg(want)
			}
			if d.A[k-1][k-1].Cmp(want) != 0 {
				t.Fatalf("iter %d k=%d invariant got %s want %s\nA=\n%sD=\n%s", iter, k, d.A[k-1][k-1], want, matString(m), matString(d))
			}
		}
	}
}

func TestSmithNormalFormOrderIndependence(t *testing.T) {
	base := matFromInts([]int64{2, 4, 6}, []int64{6, 8, 2}, []int64{10, 4, 12})
	d0, _, _, err := smithNormalForm(base)
	if err != nil {
		t.Fatal(err)
	}
	for iter := 0; iter < 20; iter++ {
		m := base.clone()
		m.swapRows(0, 1+rngInt(iter, 2))
		m.swapCols(0, 1+rngInt(iter+3, 2))
		d, _, _, err := smithNormalForm(m)
		if err != nil {
			t.Fatal(err)
		}
		if !matEqual(d, d0) {
			t.Fatalf("invariants changed after permutation:\n%s\nvs\n%s", matString(d), matString(d0))
		}
	}
}

func rngInt(seed, n int) int {
	if n <= 1 {
		return 0
	}
	return int(uint32(seed)*2654435761) % n
}

func TestFactorization(t *testing.T) {
	for _, n := range []int64{2, 6, 12, 15, 97, 1 << 20, 999983, 1000003 * 1000033,
		16777213 * 16777199, (1 << 48) - 1} {
		z := big.NewInt(n)
		f, err := factorize(z)
		if err != nil {
			t.Fatalf("factor %d: %v", n, err)
		}
		prod := big.NewInt(1)
		for p, e := range f {
			bp, _ := new(big.Int).SetString(p, 10)
			prod.Mul(prod, new(big.Int).Exp(bp, big.NewInt(int64(e)), nil))
		}
		if prod.Cmp(z) != 0 {
			t.Fatalf("factor %d = %v", n, f)
		}
	}
}

func Example_smithNormalForm() {
	m := matFromInts([]int64{2, 4}, []int64{6, 8})
	d, _, _, _ := smithNormalForm(m)
	fmt.Print(d.A[0][0], " ", d.A[1][1])
	// Output: 2 4
}
