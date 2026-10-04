package main

import (
	"fmt"
	"math/big"
	"strings"
)

// ClosedClass 是一个闭通信类（无出边的强连通分量）。
type ClosedClass struct {
	Members   []int
	Absorbing bool // 单点吸收态
}

// Analysis 保存完整分析结果。
type Analysis struct {
	Chain     *Chain
	Classes   []ClosedClass
	classOf   []int // 状态 -> 闭类下标，-1 表示非常返（瞬态）
	transient []int
	tIdx      []int // 状态 -> transient 下标，-1 表示非瞬态

	// Hit[i][c]：从状态 i 出发最终进入闭类 c 的概率（对所有状态定义）。
	Hit [][]*big.Rat
	// ExpSteps[i]：被吸收所需的期望步数；nil 表示 +∞（可能或必然不被吸收）。
	ExpSteps []*big.Rat
}

// Analyze 执行完整分析。关键原则：
//   - 只在真正的瞬态子集上求解 (I-Q)X=R 与 (I-Q)t=1，该子矩阵必非奇异；
//   - 能以正概率进入非吸收闭类的状态，期望吸收步数为 +∞，绝不硬解奇异方程
//     伪造有限期望。
func Analyze(c *Chain) (*Analysis, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	a := &Analysis{Chain: c, classOf: make([]int, c.N), tIdx: make([]int, c.N)}
	for i := range a.classOf {
		a.classOf[i] = -1
		a.tIdx[i] = -1
	}

	sccs := tarjanSCC(c.N, c.succ)
	for _, scc := range sccs {
		in := make(map[int]bool, len(scc))
		for _, v := range scc {
			in[v] = true
		}
		closed := true
		for _, v := range scc {
			for _, w := range c.succ(v) {
				if !in[w] {
					closed = false
					break
				}
			}
			if !closed {
				break
			}
		}
		if !closed {
			continue
		}
		idx := len(a.Classes)
		a.Classes = append(a.Classes, ClosedClass{
			Members:   scc,
			Absorbing: len(scc) == 1 && c.Absorbing[scc[0]],
		})
		for _, v := range scc {
			a.classOf[v] = idx
		}
	}
	for i := 0; i < c.N; i++ {
		if a.classOf[i] == -1 {
			a.tIdx[i] = len(a.transient)
			a.transient = append(a.transient, i)
		}
	}

	t := len(a.transient)
	k := len(a.Classes)
	a.Hit = make([][]*big.Rat, c.N)
	a.ExpSteps = make([]*big.Rat, c.N)

	// 闭类内状态：命中自身类概率为 1；非吸收闭类期望为 +∞，吸收态期望为 0。
	for i := 0; i < c.N; i++ {
		a.Hit[i] = make([]*big.Rat, k)
		for cc := 0; cc < k; cc++ {
			a.Hit[i][cc] = new(big.Rat)
		}
		if ci := a.classOf[i]; ci >= 0 {
			a.Hit[i][ci].Set(ratOne)
			if a.Classes[ci].Absorbing {
				a.ExpSteps[i] = new(big.Rat) // 已吸收：0 步
			}
			// 非吸收闭类：ExpSteps 保持 nil（+∞）
		}
	}

	if t > 0 && k > 0 {
		// 组装 I-Q 与 R（每列对应一个闭类的一步进入概率）。
		iq := make([][]*big.Rat, t)
		r := make([][]*big.Rat, t)
		for ti, i := range a.transient {
			iq[ti] = make([]*big.Rat, t)
			for tj, j := range a.transient {
				v := new(big.Rat).Neg(c.P[i][j])
				if ti == tj {
					v.Add(v, ratOne)
				}
				iq[ti][tj] = v
			}
			r[ti] = make([]*big.Rat, k)
			for cc, cls := range a.Classes {
				s := new(big.Rat)
				for _, j := range cls.Members {
					s.Add(s, c.P[i][j])
				}
				r[ti][cc] = s
			}
		}
		// 瞬态集合不含闭类，I-Q 必非奇异；若奇异说明分类有 bug，必须报错而非硬解。
		x, ok := solveLinear(iq, r)
		if !ok {
			return nil, fmt.Errorf("internal error: I-Q singular over transient states")
		}
		for ti, i := range a.transient {
			for cc := 0; cc < k; cc++ {
				a.Hit[i][cc].Set(x[ti][cc])
			}
		}

		// 期望步数：仅对几乎必然被吸收（命中吸收类概率和为 1）的瞬态给出有限值。
		// 对其余瞬态（能以正概率陷入非吸收闭类）保持 nil = +∞。
		ones := make([][]*big.Rat, t)
		for ti := range ones {
			ones[ti] = []*big.Rat{new(big.Rat).Set(ratOne)}
		}
		tsol, ok := solveLinear(iq, ones)
		if !ok {
			return nil, fmt.Errorf("internal error: I-Q singular for expectation system")
		}
		for ti, i := range a.transient {
			absorbed := new(big.Rat)
			for cc, cls := range a.Classes {
				if cls.Absorbing {
					absorbed.Add(absorbed, a.Hit[i][cc])
				}
			}
			if absorbed.Cmp(ratOne) == 0 {
				a.ExpSteps[i] = new(big.Rat).Set(tsol[ti][0])
			}
		}
	}
	return a, nil
}

// Verification 是独立代回核验的结果。
type Verification struct {
	HitResidual  *big.Rat // max |x_ic - (r_ic + Σ_j Q_ij x_jc)|
	TimeResidual *big.Rat // max |t_i - (1 + Σ_j Q_ij t_j)|（仅有限期望状态）
	HitSumOK     bool     // 每个瞬态：所有闭类命中概率之和 == 1
	RangeOK      bool     // 概率 ∈ [0,1]，有限期望 >= 1
	InfOK        bool     // 期望为 ∞ 当且仅当吸收概率 < 1
}

// Verify 用原始矩阵 P 独立代回核验解的正确性。
func (a *Analysis) Verify() *Verification {
	c := a.Chain
	v := &Verification{
		HitResidual:  new(big.Rat),
		TimeResidual: new(big.Rat),
		HitSumOK:     true,
		RangeOK:      true,
		InfOK:        true,
	}
	maxAbs := func(dst, x *big.Rat) {
		ax := new(big.Rat).Abs(x)
		if ax.Cmp(dst) > 0 {
			dst.Set(ax)
		}
	}
	for i := 0; i < c.N; i++ {
		// 命中概率方程：x_ic = Σ_{j∈c} P_ij + Σ_{j 瞬态} P_ij x_jc
		for cc := range a.Classes {
			rhs := new(big.Rat)
			for j := 0; j < c.N; j++ {
				if c.P[i][j].Sign() == 0 {
					continue
				}
				if a.classOf[j] == cc {
					rhs.Add(rhs, c.P[i][j])
				} else if a.classOf[j] == -1 {
					t := new(big.Rat).Mul(c.P[i][j], a.Hit[j][cc])
					rhs.Add(rhs, t)
				}
			}
			diff := new(big.Rat).Sub(a.Hit[i][cc], rhs)
			maxAbs(v.HitResidual, diff)
			if a.Hit[i][cc].Sign() < 0 || a.Hit[i][cc].Cmp(ratOne) > 0 {
				v.RangeOK = false
			}
		}
		// 期望方程：t_i = 1 + Σ_{j 瞬态} P_ij t_j（仅对有限期望状态）
		if a.ExpSteps[i] != nil && a.classOf[i] == -1 {
			rhs := new(big.Rat).Set(ratOne)
			for _, j := range a.transient {
				if c.P[i][j].Sign() == 0 {
					continue
				}
				if a.ExpSteps[j] == nil {
					v.InfOK = false // 有限期望状态不应以正概率到达无限期望状态
					continue
				}
				t := new(big.Rat).Mul(c.P[i][j], a.ExpSteps[j])
				rhs.Add(rhs, t)
			}
			diff := new(big.Rat).Sub(a.ExpSteps[i], rhs)
			maxAbs(v.TimeResidual, diff)
			if a.ExpSteps[i].Cmp(ratOne) < 0 {
				v.RangeOK = false
			}
		}
		// 瞬态：命中概率总和必须为 1
		if a.classOf[i] == -1 {
			sum := new(big.Rat)
			for cc := range a.Classes {
				sum.Add(sum, a.Hit[i][cc])
			}
			if sum.Cmp(ratOne) != 0 {
				v.HitSumOK = false
			}
		}
		// ∞ 当且仅当吸收概率 < 1
		absProb := new(big.Rat)
		for cc, cls := range a.Classes {
			if cls.Absorbing {
				absProb.Add(absProb, a.Hit[i][cc])
			}
		}
		isInf := a.ExpSteps[i] == nil
		shouldInf := absProb.Cmp(ratOne) != 0
		if isInf != shouldInf {
			v.InfOK = false
		}
	}
	return v
}

// fmtRat 以紧凑形式输出有理数（整数不显示分母）。
func fmtRat(r *big.Rat) string {
	return r.RatString()
}

func formatClass(cls ClosedClass) string {
	parts := make([]string, len(cls.Members))
	for i, m := range cls.Members {
		parts[i] = fmt.Sprintf("%d", m)
	}
	kind := "non-absorbing"
	if cls.Absorbing {
		kind = "absorbing"
	}
	return fmt.Sprintf("{%s} (%s)", strings.Join(parts, ", "), kind)
}

func formatPath(path []int) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = fmt.Sprintf("%d", s)
	}
	return strings.Join(parts, " -> ")
}

// Report 生成人类可读报告：闭类清单、每个查询状态的命中概率、
// 期望步数、可达闭类证据（见证路径）以及代回核验结果。
func (a *Analysis) Report(queries []int, v *Verification) string {
	var b strings.Builder
	c := a.Chain
	var absorbingStates []int
	for i := 0; i < c.N; i++ {
		if c.Absorbing[i] {
			absorbingStates = append(absorbingStates, i)
		}
	}
	fmt.Fprintf(&b, "Markov chain: %d states, absorbing: %v\n", c.N, absorbingStates)
	fmt.Fprintf(&b, "Closed classes (%d):\n", len(a.Classes))
	for i, cls := range a.Classes {
		fmt.Fprintf(&b, "  class %d: %s\n", i, formatClass(cls))
	}
	if len(a.transient) > 0 {
		fmt.Fprintf(&b, "Transient states: %v\n", a.transient)
	}
	for _, q := range queries {
		fmt.Fprintf(&b, "\nQuery initial state %d:\n", q)
		for cc, cls := range a.Classes {
			fmt.Fprintf(&b, "  P(hit %s) = %s\n", formatClass(cls), fmtRat(a.Hit[q][cc]))
		}
		if a.ExpSteps[q] != nil {
			fmt.Fprintf(&b, "  expected absorption steps = %s\n", fmtRat(a.ExpSteps[q]))
		} else {
			trap := new(big.Rat)
			for cc, cls := range a.Classes {
				if !cls.Absorbing {
					trap.Add(trap, a.Hit[q][cc])
				}
			}
			fmt.Fprintf(&b, "  expected absorption steps = +inf "+
				"(probability %s of never being absorbed)\n", fmtRat(trap))
		}
		// 可达闭类证据：BFS 见证路径
		reach := c.reachable(q)
		fmt.Fprintf(&b, "  reachable closed-class evidence:\n")
		any := false
		for cc, cls := range a.Classes {
			inClass := make(map[int]bool, len(cls.Members))
			hit := false
			for _, m := range cls.Members {
				inClass[m] = true
				if reach[m] {
					hit = true
				}
			}
			if !hit {
				continue
			}
			any = true
			path := c.witnessPath(q, func(s int) bool { return inClass[s] })
			fmt.Fprintf(&b, "    class %d %s via path %s\n", cc, formatClass(cls), formatPath(path))
		}
		if !any {
			fmt.Fprintf(&b, "    (none)\n")
		}
	}
	fmt.Fprintf(&b, "\nIndependent substitution-back verification:\n")
	fmt.Fprintf(&b, "  hitting-probability equation residual: %s\n", fmtRat(v.HitResidual))
	fmt.Fprintf(&b, "  expectation equation residual: %s\n", fmtRat(v.TimeResidual))
	fmt.Fprintf(&b, "  hitting probabilities sum to 1: %v\n", v.HitSumOK)
	fmt.Fprintf(&b, "  probabilities/expectations in range: %v\n", v.RangeOK)
	fmt.Fprintf(&b, "  infinity iff absorption prob < 1: %v\n", v.InfOK)
	return b.String()
}
