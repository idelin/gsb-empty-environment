package matdiag

import (
	"math"
	"testing"
)

// 经典样例（Forsythe–Moler）：A 的行列式为 1e-6，cond ≈ 2.7e6。
// 精确解 x = [1, -1]；候选解 x̂ = [0.341, -0.087] 的残差约 1e-6，
// 但前向误差约 0.9——“残差小但解误差大”的标准反例。

func TestSmallResidualLargeError(t *testing.T) {
	ar := RatMatrix([]string{"0.780", "0.563"}, []string{"0.913", "0.659"})
	br := RatVector("0.217", "0.254")
	exact, err := ExactSolve(ar, br)
	if err != nil {
		t.Fatal(err)
	}
	a, b := FloatMatrix(ar), FloatVector(br)

	// 浮点求解器得到的解应当很好。
	rep := Diagnose(a, b)
	if rep.Singular || rep.NearSingular {
		t.Fatalf("不应报告奇异: %+v", rep)
	}
	if fwd := RelForwardError(rep.X, exact); fwd > 1e-9 {
		t.Fatalf("求解器前向误差应很小，得到 %.3e", fwd)
	}

	// 但一个残差极小的候选解可以有巨大的前向误差。
	bad := []float64{0.341, -0.087}
	resid, eta := Evaluate(a, b, bad)
	if resid > 1e-5 {
		t.Fatalf("候选解残差应约 1e-6，得到 %.3e", resid)
	}
	fwd := RelForwardError(bad, exact)
	if fwd < 0.5 {
		t.Fatalf("候选解前向误差应约 0.9，得到 %.3e", fwd)
	}
	// 误差界 cond·η 必须覆盖实际前向误差。
	if bound := rep.Cond * eta; bound < fwd {
		t.Fatalf("误差界失效：cond·η = %.3e < 前向误差 %.3e", bound, fwd)
	}
}

// 计算解的后向误差应在机器精度量级。
func TestComputedSolutionBackwardError(t *testing.T) {
	ar := RatMatrix(
		[]string{"2", "1", "1"},
		[]string{"1", "3", "1"},
		[]string{"1", "1", "4"},
	)
	br := RatVector("4", "5", "6")
	exact, err := ExactSolve(ar, br)
	if err != nil {
		t.Fatal(err)
	}
	a, b := FloatMatrix(ar), FloatVector(br)
	rep := Diagnose(a, b)
	if rep.BackwardError > 1e-14 {
		t.Fatalf("后向误差应 ~eps，得到 %.3e", rep.BackwardError)
	}
	if fwd := RelForwardError(rep.X, exact); fwd > 1e-14 {
		t.Fatalf("前向误差应 ~eps，得到 %.3e", fwd)
	}
}

// 真奇异与近奇异必须分开报告。
func TestSingularReportedSeparately(t *testing.T) {
	rep := Diagnose([][]float64{{1, 2}, {2, 4}}, []float64{3, 6})
	if !rep.Singular {
		t.Fatal("应报告真奇异")
	}
	if rep.NearSingular {
		t.Fatal("真奇异不应同时标记为近奇异")
	}
	if rep.X != nil {
		t.Fatal("真奇异时不应给出解")
	}
	if !math.IsInf(rep.Cond, 1) {
		t.Fatal("真奇异时条件数应为 +Inf")
	}
}
