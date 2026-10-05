package matdiag

import (
	"fmt"
	"math/big"
	"testing"
)

// 3 阶 Hilbert 矩阵，b = A·[1,1,1]，精确解应为 [1, 1, 1]。
func TestExactSolveHilbert3(t *testing.T) {
	a := RatMatrix(
		[]string{"1", "1/2", "1/3"},
		[]string{"1/2", "1/3", "1/4"},
		[]string{"1/3", "1/4", "1/5"},
	)
	b := RatVector("11/6", "13/12", "47/60")
	x, err := ExactSolve(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for i, xi := range x {
		if xi.RatString() != "1" {
			t.Fatalf("x[%d] = %s, 期望 1", i, xi.RatString())
		}
	}
}

func TestExactSolveSingular(t *testing.T) {
	a := RatMatrix([]string{"1", "2"}, []string{"2", "4"})
	if _, err := ExactSolve(a, RatVector("3", "6")); err == nil {
		t.Fatal("精确奇异矩阵应返回错误")
	}
}

// 有理参照用于度量浮点解的真实前向误差（Hilbert 8，病态）。
// 精确解为全 1 向量；右端项用 big.Rat 精确累加行元素得到。
func TestRationalReferenceMeasuresForwardError(t *testing.T) {
	n := 8
	rows := make([][]string, n)
	b := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		rows[i] = make([]string, n)
		b[i] = new(big.Rat)
		for j := 0; j < n; j++ {
			rows[i][j] = fmt.Sprintf("1/%d", i+j+1)
			r, _ := new(big.Rat).SetString(rows[i][j])
			b[i].Add(b[i], r)
		}
	}
	a := RatMatrix(rows...)
	exact, err := ExactSolve(a, b)
	if err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(FloatMatrix(a), FloatVector(b))
	fwd := RelForwardError(rep.X, exact)
	if fwd <= 0 || fwd > 1e-6 {
		t.Fatalf("Hilbert(8) 浮点解前向误差应在 (0, 1e-6]，得到 %.3e", fwd)
	}
	if rep.BackwardError > 1e-14 {
		t.Fatalf("后向误差仍应 ~eps，得到 %.3e", rep.BackwardError)
	}
}
