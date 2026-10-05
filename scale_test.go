package matdiag

import (
	"math"
	"testing"
)

// 对角均衡后必须能用 x = Dc·y 映射回原变量，且与精确解一致。
func TestEquilibrateMapBack(t *testing.T) {
	ar := RatMatrix([]string{"1e-12", "2e-12"}, []string{"1", "1"})
	br := RatVector("3e-12", "2") // 精确解 x = [1, 1]
	exact, err := ExactSolve(ar, br)
	if err != nil {
		t.Fatal(err)
	}
	a, b := FloatMatrix(ar), FloatVector(br)

	sc := Equilibrate(a)
	as, bs := sc.Apply(a, b)
	y := Factor(as).Solve(bs)
	x := sc.MapBack(y) // x = Dc·y

	if fwd := RelForwardError(x, exact); fwd > 1e-12 {
		t.Fatalf("映射回原变量后前向误差过大: %.3e", fwd)
	}
	// 映射回的解在原系统上的后向误差也应很小。
	if _, eta := Evaluate(a, b, x); eta > 1e-12 {
		t.Fatalf("原系统后向误差过大: %.3e", eta)
	}
}

// 尺度悬殊：diag(1e-12, 1) 原始 cond = 1e12（触发“尺度不良”），
// 均衡后变为单位矩阵 cond = 1，不应报告为近奇异。
func TestPoorlyScaledNotIllConditioned(t *testing.T) {
	a := [][]float64{{1e-12, 0}, {0, 1}}
	rep := Diagnose(a, []float64{1e-12, 1})
	if rep.Singular || rep.NearSingular {
		t.Fatalf("尺度悬殊不等于病态: %+v", rep)
	}
	if !rep.PoorlyScaled {
		t.Fatalf("cond=%.3e 应报告为尺度不良", rep.Cond)
	}
	if rep.ScaledCond >= 2 {
		// 2 的幂缩放使均衡后条件数落在 [1, 2)，远小于原始的 1e12。
		t.Fatalf("均衡后条件数应小于 2，得到 %v", rep.ScaledCond)
	}
	// 缩放因子必须是 2 的幂（精确缩放）。
	sc := Equilibrate(a)
	for _, v := range append(append([]float64{}, sc.Row...), sc.Col...) {
		frac, exp := math.Frexp(v)
		if frac != 0.5 || exp < -1000 {
			t.Fatalf("缩放因子 %v 不是 2 的幂", v)
		}
	}
}
