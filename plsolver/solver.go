// Package plsolver 求解定义在有限区间 [L, R] 上的一维分段线性函数全局最小化问题：
//
//	f(x) = lin*x + sum_i w_i * |x - a_i|,   x in [L, R]
//
// 权重 w_i 允许为负，因此 f 一般非凸，不能套用加权中位数结论。
// 求解器在全部断点（含重复断点合并后）上逐段精确计算斜率与函数值
// （使用 math/big.Rat 有理数运算，无浮点误差），返回全局最小值、
// 全部最优区间（平坦段、端点最优、孤立最优点）以及每段斜率证据。
package plsolver

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Term 表示一项 w*|x - a|。
type Term struct {
	W *big.Rat
	A *big.Rat
}

// Interval 表示一个闭区间；Lo == Hi 时退化为孤立点。
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

func (iv Interval) String() string {
	if iv.Lo.Cmp(iv.Hi) == 0 {
		return "{" + iv.Lo.RatString() + "}"
	}
	return "[" + iv.Lo.RatString() + ", " + iv.Hi.RatString() + "]"
}

// Segment 是相邻断点之间的一段，记录该段斜率与两端函数值，作为斜率证据。
type Segment struct {
	Lo    *big.Rat
	Hi    *big.Rat
	Slope *big.Rat
	FLo   *big.Rat
	FHi   *big.Rat
}

func (sg Segment) String() string {
	return fmt.Sprintf("[%s, %s]: slope=%s f(lo)=%s f(hi)=%s",
		sg.Lo.RatString(), sg.Hi.RatString(), sg.Slope.RatString(),
		sg.FLo.RatString(), sg.FHi.RatString())
}

// Solution 保存求解结果：全局最小值、全部最优区间（极大化合并后）以及逐段斜率证据。
type Solution struct {
	Min       *big.Rat
	Intervals []Interval
	Segments  []Segment
}

func (sol *Solution) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "min = %s\n", sol.Min.RatString())
	b.WriteString("optimal set:")
	if len(sol.Intervals) == 0 {
		b.WriteString(" <empty>")
	}
	for _, iv := range sol.Intervals {
		fmt.Fprintf(&b, " %s", iv)
	}
	b.WriteString("\nslope evidence:\n")
	for _, sg := range sol.Segments {
		fmt.Fprintf(&b, "  %s\n", sg)
	}
	return b.String()
}

// Solver 维护目标函数的项集合，支持增删项后重复求解。
type Solver struct {
	L     *big.Rat
	R     *big.Rat
	lin   *big.Rat
	terms map[int]Term
	next  int
}

// NewSolver 创建定义在 [L, R] 上的求解器，要求 L <= R。
func NewSolver(L, R *big.Rat) *Solver {
	if L.Cmp(R) > 0 {
		panic("plsolver: empty interval")
	}
	return &Solver{
		L:     new(big.Rat).Set(L),
		R:     new(big.Rat).Set(R),
		lin:   new(big.Rat),
		terms: make(map[int]Term),
	}
}

// AddTerm 加入一项 w*|x - a|，返回该项的 id（用于删除）。
func (s *Solver) AddTerm(w, a *big.Rat) int {
	id := s.next
	s.next++
	s.terms[id] = Term{new(big.Rat).Set(w), new(big.Rat).Set(a)}
	return id
}

// RemoveTerm 按 id 删除一项，返回是否存在。
func (s *Solver) RemoveTerm(id int) bool {
	if _, ok := s.terms[id]; !ok {
		return false
	}
	delete(s.terms, id)
	return true
}

// AddLinear 给线性项系数加上 c（目标含 lin*x）。
func (s *Solver) AddLinear(c *big.Rat) {
	s.lin.Add(s.lin, c)
}

// Linear 返回当前线性项系数。
func (s *Solver) Linear() *big.Rat {
	return new(big.Rat).Set(s.lin)
}

// Eval 直接按定义计算 f(x)，供核对使用。
func (s *Solver) Eval(x *big.Rat) *big.Rat {
	v := new(big.Rat).Mul(s.lin, x)
	for _, t := range s.terms {
		d := new(big.Rat).Sub(x, t.A)
		v.Add(v, new(big.Rat).Mul(t.W, d.Abs(d)))
	}
	return v
}

// Solve 枚举全部断点，逐段推进斜率与函数值，求出全局最小值与全部最优区间。
//
// 斜率更新规则：在开段 (bp_k, bp_{k+1}) 内，
//
//	slope = lin + sum_{a_i <= bp_k} w_i - sum_{a_i >= bp_{k+1}} w_i
//
// 每越过一个断点 bp，斜率增加 2 * sum_{a_i == bp} w_i（重复断点按权重和合并）。
func (s *Solver) Solve() *Solution {
	// 退化情形：区间缩为一点。
	if s.L.Cmp(s.R) == 0 {
		v := s.Eval(s.L)
		return &Solution{
			Min:       v,
			Intervals: []Interval{{new(big.Rat).Set(s.L), new(big.Rat).Set(s.R)}},
		}
	}

	// 收集断点：端点 L、R 以及所有落在 (L, R) 内的 a_i，按有理数值去重排序。
	bpSet := make(map[string]*big.Rat)
	addBP := func(r *big.Rat) { bpSet[r.RatString()] = new(big.Rat).Set(r) }
	addBP(s.L)
	addBP(s.R)
	for _, t := range s.terms {
		if t.A.Cmp(s.L) > 0 && t.A.Cmp(s.R) < 0 {
			addBP(t.A)
		}
	}
	bps := make([]*big.Rat, 0, len(bpSet))
	for _, r := range bpSet {
		bps = append(bps, r)
	}
	sort.Slice(bps, func(i, j int) bool { return bps[i].Cmp(bps[j]) < 0 })

	// 初始斜率（最左段）：a_i <= L 的项贡献 +w_i，a_i >= R 的项贡献 -w_i，
	// 内部断点的权重先按 -w_i 计入，越过断点时再加 2*w_i。
	slope := new(big.Rat).Set(s.lin)
	atBP := make(map[string]*big.Rat) // 内部断点处的权重和（重复断点合并）
	for _, t := range s.terms {
		switch {
		case t.A.Cmp(s.L) <= 0:
			slope.Add(slope, t.W)
		case t.A.Cmp(s.R) >= 0:
			slope.Sub(slope, t.W)
		default:
			key := t.A.RatString()
			acc, ok := atBP[key]
			if !ok {
				acc = new(big.Rat)
				atBP[key] = acc
			}
			acc.Add(acc, t.W)
			slope.Sub(slope, t.W)
		}
	}

	// 逐段推进：记录每段斜率与端点函数值。
	var segs []Segment
	v := s.Eval(s.L)
	for k := 0; k+1 < len(bps); k++ {
		lo, hi := bps[k], bps[k+1]
		dx := new(big.Rat).Sub(hi, lo)
		vHi := new(big.Rat).Add(v, new(big.Rat).Mul(slope, dx))
		segs = append(segs, Segment{
			Lo:    new(big.Rat).Set(lo),
			Hi:    new(big.Rat).Set(hi),
			Slope: new(big.Rat).Set(slope),
			FLo:   new(big.Rat).Set(v),
			FHi:   new(big.Rat).Set(vHi),
		})
		v = vHi
		// 越过断点 hi（最后一个断点是 R，其后无段，无需更新）。
		if k+1 < len(bps)-1 {
			if acc, ok := atBP[hi.RatString()]; ok {
				slope.Add(slope, new(big.Rat).Mul(acc, big.NewRat(2, 1)))
			}
		}
	}

	// 全局最小值：线性段的最小值必在断点处取得。
	min := new(big.Rat).Set(segs[0].FLo)
	for _, sg := range segs {
		if sg.FHi.Cmp(min) < 0 {
			min.Set(sg.FHi)
		}
		if sg.FLo.Cmp(min) < 0 {
			min.Set(sg.FLo)
		}
	}

	// 最优集合：
	//  - 斜率为 0 且函数值等于 min 的段：整段最优（平坦段）；
	//  - 斜率 > 0 的段：仅左端点可能最优；
	//  - 斜率 < 0 的段：仅右端点可能最优。
	var opts []Interval
	point := func(p *big.Rat) Interval { return Interval{new(big.Rat).Set(p), new(big.Rat).Set(p)} }
	for _, sg := range segs {
		switch sg.Slope.Sign() {
		case 0:
			if sg.FLo.Cmp(min) == 0 {
				opts = append(opts, Interval{new(big.Rat).Set(sg.Lo), new(big.Rat).Set(sg.Hi)})
			}
		case 1:
			if sg.FLo.Cmp(min) == 0 {
				opts = append(opts, point(sg.Lo))
			}
		case -1:
			if sg.FHi.Cmp(min) == 0 {
				opts = append(opts, point(sg.Hi))
			}
		}
	}

	// 合并相邻/相接的最优区间（段已按从左到右顺序生成）。
	merged := opts[:0]
	for _, iv := range opts {
		if n := len(merged); n > 0 && merged[n-1].Hi.Cmp(iv.Lo) >= 0 {
			if iv.Hi.Cmp(merged[n-1].Hi) > 0 {
				merged[n-1].Hi = iv.Hi
			}
			continue
		}
		merged = append(merged, iv)
	}

	return &Solution{Min: min, Intervals: merged, Segments: segs}
}
