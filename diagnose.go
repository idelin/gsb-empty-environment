package matdiag

import (
	"fmt"
	"math"
)

// Report 汇总一次求解的全部数值诊断。
type Report struct {
	N int
	// Singular：精确秩亏（消元中遇到零主元列）。与“近奇异”分开报告。
	Singular   bool
	SingularAt int
	// NearSingular：均衡缩放后条件数仍 >= 1/sqrt(eps)，属于真正的病态。
	NearSingular bool
	// PoorlyScaled：原始条件数越界但均衡后条件数良好，
	// 说明病态是缩放伪影，均衡后即可正常求解。
	PoorlyScaled bool

	MinPivotRatio  float64 // 最小主元比（相对矩阵最大元，尺度无关）
	Cond           float64 // 原始矩阵 1-范数条件数估计
	ScaledCond     float64 // 对角均衡后的 1-范数条件数估计
	ReliableDigits float64 // 估计的可靠十进制位数 ≈ 16 - log10(Cond)

	X             []float64 // 求得的解（奇异时为 nil）
	ResidualInf   float64   // ||b - A x||_inf
	BackwardError float64   // 范数后向误差 η

	Notes []string
}

// nearSingularThreshold 是“近奇异”判据：cond >= 1/sqrt(eps) ≈ 6.7e7，
// 即最坏情况下可用有效数字不足 float64 精度的一半。
// 该判据基于条件数，对矩阵整体缩放不变，不用任何固定绝对阈值。
func nearSingularThreshold() float64 {
	return 1 / math.Sqrt(machineEps)
}

// Evaluate 对任意候选解 x 计算残差与范数后向误差
//
//	η = ||b - A x||_inf / (||A||_inf·||x||_inf + ||b||_inf)
//
// η 是使得 x 成为 (A+ΔA)y = b+Δb（|ΔA|<=η|A|，|Δb|<=η|b|）精确解的最小扰动量级。
func Evaluate(a [][]float64, b, x []float64) (residualInf, backwardError float64) {
	r := matVec(a, x)
	for i := range r {
		r[i] = b[i] - r[i]
	}
	residualInf = vecInfNorm(r)
	denom := InfNorm(a)*vecInfNorm(x) + vecInfNorm(b)
	if denom == 0 {
		return residualInf, 0
	}
	return residualInf, residualInf / denom
}

// Diagnose 求解 A x = b 并给出完整诊断。
func Diagnose(a [][]float64, b []float64) *Report {
	n := len(a)
	rep := &Report{N: n, SingularAt: -1}

	f := Factor(a)
	rep.MinPivotRatio = f.MinPivotRatio()

	if f.Singular {
		rep.Singular = true
		rep.SingularAt = f.SingularAt
		rep.Cond = math.Inf(1)
		rep.ScaledCond = math.Inf(1)
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"真奇异：第 %d 步消元时主元列全为精确的 0（相对矩阵尺度），矩阵秩亏，解不存在或不唯一；这与“小于某阈值”无关",
			f.SingularAt))
		return rep
	}

	x := f.Solve(b)
	rep.X = x
	rep.Cond = Cond1(a, f)

	sc := Equilibrate(a)
	as, _ := sc.Apply(a, b)
	rep.ScaledCond = Cond1(as, Factor(as))

	rep.ResidualInf, rep.BackwardError = Evaluate(a, b, x)
	rep.ReliableDigits = math.Max(0, 16-math.Log10(rep.Cond))

	threshold := nearSingularThreshold()
	switch {
	case rep.ScaledCond >= threshold:
		rep.NearSingular = true
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"近奇异：均衡后条件数 %.3e >= 1/sqrt(eps) ≈ %.1e，属真正病态；即使后向误差仅 eps 量级，解的相对误差也可达 cond·eps ≈ %.1e",
			rep.ScaledCond, threshold, rep.ScaledCond*machineEps))
	case rep.Cond >= threshold:
		rep.PoorlyScaled = true
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"尺度不良而非病态：原始条件数 %.3e 越界，但对角均衡后仅 %.3e；建议解 (Dr·A·Dc)y = Dr·b 并用 x = Dc·y 映射回原变量",
			rep.Cond, rep.ScaledCond))
	}
	if !rep.Singular && rep.MinPivotRatio <= math.Sqrt(machineEps) && !rep.NearSingular && !rep.PoorlyScaled {
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"注意：最小主元比 %.3e 偏小（相对矩阵最大元），建议关注舍入误差累积", rep.MinPivotRatio))
	}
	return rep
}
