// matdiag 演示：行交换、尺度悬殊、小残差大误差、真奇异 vs 近奇异。
package main

import (
	"fmt"
	"math/big"

	"matdiag"
)

func printReport(rep *matdiag.Report, exact []*big.Rat) {
	if rep.Singular {
		fmt.Printf("  判定：真奇异（第 %d 步消元遇到精确零主元列，秩亏）\n", rep.SingularAt)
	} else {
		fmt.Printf("  解 x = %.6g\n", rep.X)
		if exact != nil {
			fmt.Printf("  相对前向误差（对照有理精确解）= %.3e\n", matdiag.RelForwardError(rep.X, exact))
		}
		fmt.Printf("  残差 ||b-Ax||_inf = %.3e，范数后向误差 η = %.3e\n", rep.ResidualInf, rep.BackwardError)
		fmt.Printf("  cond1(A) = %.3e，均衡后 cond1 = %.3e，估计可靠位 ≈ %.1f 位十进制\n",
			rep.Cond, rep.ScaledCond, rep.ReliableDigits)
		fmt.Printf("  最小主元比（相对矩阵最大元）= %.3e\n", rep.MinPivotRatio)
	}
	for _, note := range rep.Notes {
		fmt.Println("  * " + note)
	}
}

func main() {
	fmt.Println("=== 案例 1：需要行交换（首主元为 0）===")
	ar := matdiag.RatMatrix(
		[]string{"0", "1", "2"},
		[]string{"1", "0", "1"},
		[]string{"2", "1", "0"},
	)
	br := matdiag.RatVector("8", "4", "4") // 精确解 [1, 2, 3]
	exact, _ := matdiag.ExactSolve(ar, br)
	a, b := matdiag.FloatMatrix(ar), matdiag.FloatVector(br)
	f := matdiag.Factor(a)
	fmt.Printf("  行置换 Perm = %v（第一步即换行）\n", f.Perm)
	printReport(matdiag.Diagnose(a, b), exact)

	fmt.Println("\n=== 案例 2a：整体尺度极小 1e-12·I（固定绝对阈值会误判）===")
	ar = matdiag.RatMatrix(
		[]string{"1e-12", "0", "0"},
		[]string{"0", "1e-12", "0"},
		[]string{"0", "0", "1e-12"},
	)
	br = matdiag.RatVector("1e-12", "2e-12", "3e-12") // 精确解 [1, 2, 3]
	exact, _ = matdiag.ExactSolve(ar, br)
	a, b = matdiag.FloatMatrix(ar), matdiag.FloatVector(br)
	fmt.Println("  主元绝对值仅 1e-12，但相对判据下完全健康：")
	printReport(matdiag.Diagnose(a, b), exact)

	fmt.Println("\n=== 案例 2b：尺度悬殊 diag(1e-12, 1)，对角均衡与映射回 ===")
	ar = matdiag.RatMatrix([]string{"1e-12", "0"}, []string{"0", "1"})
	br = matdiag.RatVector("1e-12", "1") // 精确解 [1, 1]
	exact, _ = matdiag.ExactSolve(ar, br)
	a, b = matdiag.FloatMatrix(ar), matdiag.FloatVector(br)
	printReport(matdiag.Diagnose(a, b), exact)
	sc := matdiag.Equilibrate(a)
	as, bs := sc.Apply(a, b)
	y := matdiag.Factor(as).Solve(bs)
	x := sc.MapBack(y)
	fmt.Printf("  均衡系统 (Dr·A·Dc)y = Dr·b 的解 y = %v\n", y)
	fmt.Printf("  映射回原变量 x = Dc·y = %v（行缩放不影响未知量，列缩放须乘回）\n", x)

	fmt.Println("\n=== 案例 3：残差小但解误差大（Forsythe–Moler 经典例）===")
	ar = matdiag.RatMatrix([]string{"0.780", "0.563"}, []string{"0.913", "0.659"})
	br = matdiag.RatVector("0.217", "0.254") // 精确解 [1, -1]
	exact, _ = matdiag.ExactSolve(ar, br)
	a, b = matdiag.FloatMatrix(ar), matdiag.FloatVector(br)
	rep := matdiag.Diagnose(a, b)
	printReport(rep, exact)
	bad := []float64{0.341, -0.087}
	resid, eta := matdiag.Evaluate(a, b, bad)
	fwd := matdiag.RelForwardError(bad, exact)
	fmt.Printf("  候选解 x̂ = %v：残差 %.3e、后向误差 %.3e 都很小，\n", bad, resid, eta)
	fmt.Printf("  但相对前向误差 = %.3f！界 cond·η = %.3e 能覆盖它（%v）。\n",
		fwd, rep.Cond*eta, rep.Cond*eta >= fwd)
	fmt.Println("  结论：小残差只保证 x̂ 是某个邻近问题的精确解；前向误差还取决于条件数。")

	fmt.Println("\n=== 案例 4：真奇异 vs 近奇异，分开报告 ===")
	fmt.Println("-- [[1 2] [2 4]]（精确秩亏）:")
	printReport(matdiag.Diagnose([][]float64{{1, 2}, {2, 4}}, []float64{3, 6}), nil)
	fmt.Println("-- [[1 1] [1 1+1e-9]]（近奇异）:")
	printReport(matdiag.Diagnose([][]float64{{1, 1}, {1, 1 + 1e-9}}, []float64{2, 2 + 1e-9}), nil)
}
