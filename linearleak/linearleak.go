// Package linearleak 判定统计接口的整数线性计数查询是否会唯一确定
// 尚未公开的秘密位。
//
// 输入是若干条对秘密位的整数线性方程
//
//	sum(Coeff[i] * x_i) = RHS,  x_i ∈ {0,1}
//
// 秘密位总数不超过 20。判定同时使用两种手段：
//   - 有理数高斯-若尔当消元：识别实数域上的固定变量、自由变量与线性矛盾；
//   - 带预算的 0/1 可行性回溯：确认某个变量在布尔域上是否被强制取值。
//
// 实数域唯一并不等价于布尔域唯一：自由实变量可能被 0/1 约束钉死，
// 实数唯一解也可能是分数而不存在布尔解。因此两种判断必须共同参与，
// 任何只基于实数消元的结论都不会被当作最终结论。预算耗尽时 Analyze
// 返回 StatusUnknown，绝不给出缺少证明支撑的结论。
package linearleak

import (
	"fmt"
	"math/big"
	"sort"
)

// maxBits 是允许的秘密位总数上限。
const maxBits = 20

const (
	defaultNodeLimit  int64 = 250000
	defaultCheckLimit int64 = 400
)

// Equation 是一条观测到的整数线性方程：sum(Coeff[i]*x_i) = RHS。
// Coeff 的长度必须等于 Analyze 接收的秘密位总数。Tag 为可选标签，
// 会原样出现在 Finding.Tags 中，便于调用方回溯具体查询。
type Equation struct {
	Coeff []int64
	RHS   int64
	Tag   string
}

// BitValue 表示一个已公开（外部已知）秘密位的取值。
type BitValue struct {
	Index int
	Value int // 只能是 0 或 1
}

// Budget 限制分析器的工作量，零值字段使用默认预算。
//
// FeasibilityNodes 限制全部 0/1 可行性回溯的搜索节点总数；
// ProofChecks 限制提取最小证明过程中发起的可行性判定次数。
// 任一预算耗尽都会让相关结论降级为 StatusUnknown。
type Budget struct {
	FeasibilityNodes int64
	ProofChecks      int64
}

// Status 是分析结论的三值（外加矛盾）判定。
type Status int

const (
	// StatusUnknown 表示预算耗尽，存在无法可靠判定的秘密位。
	StatusUnknown Status = iota
	// StatusSafe 表示所有未公开秘密位都能取 0 和 1，均未被唯一确定。
	StatusSafe
	// StatusLeak 表示至少一个未公开秘密位被方程组唯一确定。
	StatusLeak
	// StatusInconsistent 表示方程组在 0/1 域上无解（观测本身矛盾）。
	StatusInconsistent
)

// String 返回状态的可读名称。
func (s Status) String() string {
	switch s {
	case StatusSafe:
		return "safe"
	case StatusLeak:
		return "leak"
	case StatusInconsistent:
		return "inconsistent"
	default:
		return "unknown"
	}
}

// Finding 描述一个被唯一确定的未公开秘密位及其证明。
type Finding struct {
	// Bit 是被确定的秘密位在输入中的原始下标。
	Bit int
	// Value 是被强制的取值（0 或 1）；StatusInconsistent 时为 -1，
	// 表示方程组无布尔解，该位不存在任何可行取值。
	Value int
	// Proof 是足以推出该结论的最小方程下标集合（指向 Analyze 入参）。
	Proof []int
	// Tags 与 Proof 一一对应，只包含非空的 Equation.Tag。
	Tags []string
	// RealFixed 为 true 表示该位在实数消元中即被唯一固定；
	// 为 false 表示它在实数域自由、仅由 0/1 约束强制确定。
	RealFixed bool
	// ExactMin 为 true 表示 Proof 已证明是基数最小的证明集合；
	// 为 false 时 Proof 仍是合法且包含极小（inclusion-minimal）核的证明，
	// 但预算不足以证明不存在更小的集合。
	ExactMin bool
}

// Report 是 Analyze 的完整判定结果。
type Report struct {
	// Status 是总体结论。
	Status Status
	// Findings 按 Bit 升序排列所有被唯一确定的秘密位。
	Findings []Finding
	// UnknownBits 列出因预算耗尽而无法判定的秘密位原始下标。
	UnknownBits []int
	// NodesUsed 是实际消耗的回溯节点数。
	NodesUsed int64
	// ChecksUsed 是实际消耗的可行性判定次数。
	ChecksUsed int64
}

// Analyze 判定 equations 是否唯一确定任一未公开秘密位。
//
// nBits 为秘密位总数（1 到 20），public 为已公开的位及其取值，
// 公开位会被直接代入方程且不参与泄漏判定。返回的 Report 给出总体
// 状态、每个被确定位的最小证明方程集合，以及预算耗尽时未决的位。
func Analyze(nBits int, equations []Equation, public []BitValue, budget Budget) (Report, error) {
	if nBits < 1 || nBits > maxBits {
		return Report{}, fmt.Errorf("linearleak: nBits must be in [1,%d], got %d", maxBits, nBits)
	}
	for i, eq := range equations {
		if len(eq.Coeff) != nBits {
			return Report{}, fmt.Errorf("linearleak: equation %d has %d coefficients, want %d", i, len(eq.Coeff), nBits)
		}
	}

	published := make([]bool, nBits)
	pubValue := make([]int8, nBits)
	for _, pv := range public {
		if pv.Index < 0 || pv.Index >= nBits {
			return Report{}, fmt.Errorf("linearleak: public bit index %d out of range [0,%d)", pv.Index, nBits)
		}
		if pv.Value != 0 && pv.Value != 1 {
			return Report{}, fmt.Errorf("linearleak: public bit %d has value %d, want 0 or 1", pv.Index, pv.Value)
		}
		if published[pv.Index] {
			return Report{}, fmt.Errorf("linearleak: public bit %d listed more than once", pv.Index)
		}
		published[pv.Index] = true
		pubValue[pv.Index] = int8(pv.Value)
	}

	secretOf := make([]int, 0, nBits)
	for j := 0; j < nBits; j++ {
		if !published[j] {
			secretOf = append(secretOf, j)
		}
	}

	an := &analyzer{
		n:          len(secretOf),
		secretOf:   secretOf,
		equations:  equations,
		nodeLimit:  budget.FeasibilityNodes,
		checkLimit: budget.ProofChecks,
	}
	if an.nodeLimit <= 0 {
		an.nodeLimit = defaultNodeLimit
	}
	if an.checkLimit <= 0 {
		an.checkLimit = defaultCheckLimit
	}

	// 代入公开位：公开列删除，其贡献移到右端项。
	an.coeff = make([][]int64, len(equations))
	an.rhs = make([]int64, len(equations))
	for i, eq := range equations {
		row := make([]int64, 0, an.n)
		adj := new(big.Int).SetInt64(eq.RHS)
		for j, c := range eq.Coeff {
			if published[j] {
				if c != 0 {
					adj.Sub(adj, new(big.Int).Mul(big.NewInt(c), big.NewInt(int64(pubValue[j]))))
				}
				continue
			}
			row = append(row, c)
		}
		if !adj.IsInt64() {
			return Report{}, fmt.Errorf("linearleak: equation %d overflows int64 after substituting public bits", i)
		}
		an.coeff[i] = row
		an.rhs[i] = adj.Int64()
	}

	return an.run(), nil
}

// analyzer 持有代入公开位后的方程组与共享预算计数器。
type analyzer struct {
	n          int
	secretOf   []int // 局部下标 -> 原始下标
	equations  []Equation
	coeff      [][]int64
	rhs        []int64
	nodeLimit  int64
	nodes      int64
	checkLimit int64
	checks     int64
}

// allIndices 返回全部方程下标。
func (an *analyzer) allIndices() []int {
	idx := make([]int, len(an.coeff))
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// infeasible 判定 subset 配合 seed 固定条件是否不可行。
// triTrue 表示确认不可行，triFalse 表示确认存在可行解，
// triUnknown 表示预算耗尽无法判定。每次调用计入一次 ProofChecks。
func (an *analyzer) infeasible(subset []int, seed map[int]int) triState {
	if an.checks >= an.checkLimit {
		return triUnknown
	}
	an.checks++

	coeff := make([][]int64, len(subset))
	rhs := make([]int64, len(subset))
	for k, idx := range subset {
		coeff[k] = an.coeff[idx]
		rhs[k] = an.rhs[idx]
	}
	remaining := an.nodeLimit - an.nodes
	if remaining < 1 {
		remaining = 1
	}
	sol := newSolver(an.n, coeff, rhs, remaining)
	feasible := sol.feasible(seed)
	an.nodes += sol.nodes
	if feasible == triFalse {
		return triTrue // 确认不可行
	}
	if feasible == triTrue {
		return triFalse // 确认可行
	}
	return triUnknown
}

// run 执行完整判定流程。
func (an *analyzer) run() Report {
	report := Report{Status: StatusSafe}
	allEq := an.allIndices()

	// 实数消元：识别实数固定位（整份方程组上的信息，用于标记 RealFixed）。
	realFixIndex := map[int]bool{}
	mat := an.ratMatrix(allEq)
	pivots, frees, inconsistent := rref(mat)
	if !inconsistent {
		fixed, vals := realFixed(mat, pivots, frees)
		for i, j := range fixed {
			if v, ok := ratBit(vals[i]); ok {
				realFixIndex[j] = true
				_ = v
			}
		}
	}

	// 全局 0/1 可行性。
	global := an.infeasible(allEq, nil)
	if global == triTrue {
		// 方程组无布尔解：求一个最小矛盾子集，所有秘密位都被空解集“强制”。
		proof, exact := an.minimizeProof(allEq, func(subset []int) triState {
			return an.infeasible(subset, nil)
		})
		report.Status = StatusInconsistent
		report.Findings = make([]Finding, 0, an.n)
		for local := 0; local < an.n; local++ {
			report.Findings = append(report.Findings, an.makeFinding(an.secretOf[local], -1, proof, exact, false))
		}
		an.fillCounters(&report)
		return report
	}

	for local := 0; local < an.n; local++ {
		original := an.secretOf[local]
		// 强制为 v 当且仅当 x_local = 1-v 不可行、x_local = v 可行。
		badZero := an.infeasible(allEq, map[int]int{local: 0})
		badOne := an.infeasible(allEq, map[int]int{local: 1})

		switch {
		case badZero == triTrue && badOne == triFalse:
			an.addForcedFinding(&report, local, 1, allEq, realFixIndex[local])
		case badOne == triTrue && badZero == triFalse:
			an.addForcedFinding(&report, local, 0, allEq, realFixIndex[local])
		case badZero == triFalse && badOne == triFalse:
			// 0 与 1 都有可行解，该位安全。
		case badZero == triTrue && badOne == triTrue:
			// 理论上已被全局矛盾覆盖，防御性处理为未知。
			report.UnknownBits = append(report.UnknownBits, original)
		default:
			report.UnknownBits = append(report.UnknownBits, original)
		}
	}

	sort.Ints(report.UnknownBits)
	sort.Slice(report.Findings, func(i, j int) bool { return report.Findings[i].Bit < report.Findings[j].Bit })
	switch {
	case len(report.Findings) > 0:
		report.Status = StatusLeak
	case len(report.UnknownBits) > 0:
		report.Status = StatusUnknown
	default:
		report.Status = StatusSafe
	}
	an.fillCounters(&report)
	return report
}

// addForcedFinding 为确认被强制的位提取最小证明并写入报告。
func (an *analyzer) addForcedFinding(report *Report, local, value int, allEq []int, realFixed bool) {
	forbidden := 1 - value
	proof, exact := an.minimizeProof(allEq, func(subset []int) triState {
		return an.infeasible(subset, map[int]int{local: forbidden})
	})
	report.Findings = append(report.Findings, an.makeFinding(an.secretOf[local], value, proof, exact, realFixed))
}

// makeFinding 组装 Finding，并把方程下标映射为标签。
func (an *analyzer) makeFinding(bit, value int, proof []int, exact, realFixed bool) Finding {
	sortedProof := append([]int(nil), proof...)
	sort.Ints(sortedProof)
	tags := make([]string, 0, len(sortedProof))
	for _, idx := range sortedProof {
		if an.equations[idx].Tag != "" {
			tags = append(tags, an.equations[idx].Tag)
		}
	}
	return Finding{
		Bit:       bit,
		Value:     value,
		Proof:     sortedProof,
		Tags:      tags,
		RealFixed: realFixed,
		ExactMin:  exact,
	}
}

func (an *analyzer) fillCounters(report *Report) {
	report.NodesUsed = an.nodes
	report.ChecksUsed = an.checks
}
