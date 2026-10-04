package main

import (
	"fmt"
	"math/big"
	"sort"
)

// ---------- 输出结构 ----------

// RootOut 是一个相等点（交点）的隔离证据。
type RootOut struct {
	Value        string   `json:"value,omitempty"`
	Interval     []string `json:"isolating_interval,omitempty"`
	Approx       float64  `json:"approx"`
	Multiplicity int      `json:"multiplicity"`
	Contact      string   `json:"contact"` // crossing(奇重) / tangency(偶重切触)
	Segment      []string `json:"segment"`
}

// SegEvidence 是单个原子区间上“差多项式恒零”的公共因子证据。
type SegEvidence struct {
	Segment     []string `json:"segment"`
	ReducedF    string   `json:"reduced_f"`
	ReducedG    string   `json:"reduced_g"`
	CrossDiff   string   `json:"cross_difference"`
	CommonCross string   `json:"common_cross_product"`
	ScalarRatio string   `json:"scalar_ratio"`
}

// SampleCheck 是错位采样核对记录。
type SampleCheck struct {
	At    string `json:"at"`
	F     string `json:"f"`
	G     string `json:"g"`
	Equal bool   `json:"equal"`
}

// CoincEvidence 是重合区间的完整证据。
type CoincEvidence struct {
	Reason           string        `json:"reason"`
	Segments         []SegEvidence `json:"segments"`
	StaggeredSamples []SampleCheck `json:"staggered_samples"`
	BreakpointChecks []SampleCheck `json:"breakpoint_checks,omitempty"`
}

// CoincOut 是一个极大重合区间。
type CoincOut struct {
	Lo         string        `json:"lo"`
	Hi         string        `json:"hi"`
	LoIncluded bool          `json:"lo_included"`
	HiIncluded bool          `json:"hi_included"`
	Evidence   CoincEvidence `json:"evidence"`
}

// PointOut 是被剔除点或极点的描述。
type PointOut struct {
	Value    string   `json:"value,omitempty"`
	Interval []string `json:"interval,omitempty"`
	Approx   float64  `json:"approx"`
	Reason   string   `json:"reason"`
}

// GapOut 是某函数无定义的区间。
type GapOut struct {
	Segment []string `json:"segment"`
	Missing string   `json:"missing"`
}

// VerifyOut 是整体核对结论。
type VerifyOut struct {
	AllPassed bool     `json:"all_passed"`
	Checks    []string `json:"checks"`
}

// Output 是完整求解结果。
type Output struct {
	Roots        []RootOut  `json:"roots"`
	Coincident   []CoincOut `json:"coincident_intervals"`
	Excluded     []PointOut `json:"excluded_points,omitempty"`
	Poles        []PointOut `json:"pole_points,omitempty"`
	Gaps         []GapOut   `json:"undefined_segments,omitempty"`
	Verification VerifyOut  `json:"verification"`
}

// ---------- 求解器 ----------

type Solver struct {
	F, G   *Piecewise
	Lo, Hi *big.Rat
	Eps    *big.Rat

	bps    []*big.Rat
	roots  []rootRec
	coincs []coincRaw
	poles  []Root
	out    *Output
}

type rootRec struct {
	root  Root
	mult  int
	segLo *big.Rat
	segHi *big.Rat
}

type coincRaw struct {
	lo, hi       *big.Rat
	loInc, hiInc bool
	segEv        []SegEvidence
}

func NewSolver(f, g *Piecewise, lo, hi *big.Rat, eps *big.Rat) (*Solver, error) {
	if lo.Cmp(hi) >= 0 {
		return nil, fmt.Errorf("区间非法: lo 必须小于 hi")
	}
	if eps == nil || eps.Sign() <= 0 {
		eps = big.NewRat(1, 1000000)
	}
	return &Solver{F: f, G: g, Lo: lo, Hi: hi, Eps: eps}, nil
}

func ratStr(r *big.Rat) string { return r.RatString() }

func rootApprox(r Root) float64 {
	if r.Exact != nil {
		f, _ := r.Exact.Float64()
		return f
	}
	m := new(big.Rat).Add(r.Lo, r.Hi)
	m.Quo(m, big.NewRat(2, 1))
	f, _ := m.Float64()
	return f
}

// Solve 执行完整求解。
func (s *Solver) Solve() *Output {
	s.out = &Output{}
	s.buildBreakpoints()
	for i := 0; i+1 < len(s.bps); i++ {
		s.solveSegment(s.bps[i], s.bps[i+1], i+2 == len(s.bps))
	}
	s.mergeCoincident()
	s.fillRoots()
	s.fillPoles()
	s.verify()
	return s.out
}

func (s *Solver) buildBreakpoints() {
	set := map[string]*big.Rat{
		s.Lo.RatString(): s.Lo,
		s.Hi.RatString(): s.Hi,
	}
	for _, t := range s.F.Breakpoints(s.Lo, s.Hi) {
		set[t.RatString()] = t
	}
	for _, t := range s.G.Breakpoints(s.Lo, s.Hi) {
		set[t.RatString()] = t
	}
	s.bps = s.bps[:0]
	for _, t := range set {
		s.bps = append(s.bps, t)
	}
	sort.Slice(s.bps, func(i, j int) bool { return s.bps[i].Cmp(s.bps[j]) < 0 })
}

// segmentPoles 计算一段内两分母在成员区间内的零点（极点）。
// 成员约定：非末段为 [segLo, segHi)，末段含 segHi。
func (s *Solver) segmentPoles(den Poly, segLo, segHi *big.Rat, final bool) []Root {
	if den.Deg() <= 0 {
		return nil
	}
	var poles []Root
	if den.Eval(segLo).Sign() == 0 {
		poles = append(poles, Root{Exact: new(big.Rat).Set(segLo)})
	}
	for _, r := range IsolateRoots(den, segLo, segHi, s.Eps) {
		if !final && r.Exact != nil && r.Exact.Cmp(segHi) == 0 {
			continue // 右连续：segHi 处取值属于下一段
		}
		poles = append(poles, r)
	}
	return poles
}

func (s *Solver) solveSegment(segLo, segHi *big.Rat, final bool) {
	fi := s.F.pieceRightOf(segLo)
	gi := s.G.pieceRightOf(segLo)
	seg := []string{ratStr(segLo), ratStr(segHi)}
	if fi < 0 || gi < 0 {
		missing := ""
		switch {
		case fi < 0 && gi < 0:
			missing = "f,g"
		case fi < 0:
			missing = "f"
		default:
			missing = "g"
		}
		s.out.Gaps = append(s.out.Gaps, GapOut{Segment: seg, Missing: missing})
		return
	}
	pf, pg := s.F.Pieces[fi], s.G.Pieces[gi]

	// 叉乘差多项式 D = n1*d2 - n2*d1
	D := pf.Num.Mul(pg.Den).Sub(pg.Num.Mul(pf.Den))

	// 极点（两函数在该段的未定义点）
	segPoles := append(s.segmentPoles(pf.Den, segLo, segHi, final),
		s.segmentPoles(pg.Den, segLo, segHi, final)...)
	s.poles = append(s.poles, segPoles...)

	if D.IsZero() {
		// 恒零差多项式：两有理函数在该段完全重合。
		// 直接记录重合区间，绝不进入根隔离（避免把恒零当无限循环）。
		ev := SegEvidence{
			Segment:     seg,
			ReducedF:    pf.RatString(),
			ReducedG:    pg.RatString(),
			CrossDiff:   "0 (恒零多项式)",
			CommonCross: pf.Num.Mul(pg.Den).Monic().String(),
			ScalarRatio: "1",
		}
		raw := coincRaw{
			lo: segLo, hi: segHi,
			loInc: true, hiInc: final,
			segEv: []SegEvidence{ev},
		}
		s.coincs = append(s.coincs, s.subtractPoles(raw, segPoles)...)
		return
	}

	// 剔除“差为零且分母为零”的伪根：把 D 中与分母乘积共享的因子全部除去。
	denProd := pf.Den.Mul(pg.Den)
	common := D.GCD(denProd)
	Dred := D
	for {
		g := Dred.GCD(denProd)
		if g.Deg() <= 0 {
			break
		}
		Dred = Dred.Div(g)
	}

	// 被剔除的伪根（差为零但函数无定义的点）
	if common.Deg() > 0 {
		csf := common.SquareFree()
		var excl []Root
		if csf.Eval(segLo).Sign() == 0 {
			excl = append(excl, Root{Exact: new(big.Rat).Set(segLo)})
		}
		excl = append(excl, IsolateRoots(csf, segLo, segHi, s.Eps)...)
		for _, r := range excl {
			if !final && r.Exact != nil && r.Exact.Cmp(segHi) == 0 {
				continue
			}
			s.out.Excluded = append(s.out.Excluded, PointOut{
				Value:    rootValueStr(r),
				Interval: rootIntervalStr(r),
				Approx:   rootApprox(r),
				Reason:   "差多项式与分母同时为零，函数在此无定义（伪根，已剔除）",
			})
		}
	}

	// 根隔离：左端点精确判定 + (segLo, segHi] 内 Sturm 隔离。
	var cands []Root
	if Dred.Eval(segLo).Sign() == 0 {
		cands = append(cands, Root{Exact: new(big.Rat).Set(segLo)})
	}
	cands = append(cands, IsolateRoots(Dred, segLo, segHi, s.Eps)...)
	for _, r := range cands {
		if !final && r.Exact != nil && r.Exact.Cmp(segHi) == 0 {
			continue // 右连续：归于下一段
		}
		mult := 0
		if r.Exact != nil {
			_, mult = D.Deflate(r.Exact)
		} else {
			mult = D.MultiplicityIn(r.Lo, r.Hi)
		}
		s.roots = append(s.roots, rootRec{root: r, mult: mult, segLo: segLo, segHi: segHi})
	}
}

// subtractPoles 从重合区间中挖去极点（函数无定义的点）。
func (s *Solver) subtractPoles(raw coincRaw, poles []Root) []coincRaw {
	raws := []coincRaw{raw}
	for _, p := range poles {
		var next []coincRaw
		for _, r := range raws {
			next = append(next, subtractOne(r, p)...)
		}
		raws = next
	}
	return raws
}

func subtractOne(r coincRaw, p Root) []coincRaw {
	var pLo, pHi *big.Rat // 极点占据的开洞 (pLo, pHi) 或点
	if p.Exact != nil {
		pLo, pHi = p.Exact, p.Exact
	} else {
		pLo, pHi = p.Lo, p.Hi
	}
	// 无交
	if pHi.Cmp(r.lo) <= 0 || pLo.Cmp(r.hi) >= 0 {
		if p.Exact != nil && p.Exact.Cmp(r.lo) == 0 {
			r.loInc = false // 左端点是极点
		}
		return []coincRaw{r}
	}
	var out []coincRaw
	if pLo.Cmp(r.lo) > 0 {
		left := r
		left.hi = pLo
		left.hiInc = false
		out = append(out, left)
	} else {
		// 极点覆盖左端点
		if p.Exact == nil || pLo.Cmp(r.lo) == 0 {
			r.loInc = false
		}
	}
	if pHi.Cmp(r.hi) < 0 {
		right := r
		right.lo = pHi
		right.loInc = false
		out = append(out, right)
	}
	if len(out) == 0 {
		// 整个区间被极点覆盖（理论上不会发生，因为重合要求两边有定义）
		return nil
	}
	return out
}

// mergeCoincident 合并相邻重合段为极大重合区间。
func (s *Solver) mergeCoincident() {
	if len(s.coincs) == 0 {
		return
	}
	sort.Slice(s.coincs, func(i, j int) bool { return s.coincs[i].lo.Cmp(s.coincs[j].lo) < 0 })
	merged := []coincRaw{s.coincs[0]}
	for _, c := range s.coincs[1:] {
		last := &merged[len(merged)-1]
		if last.hi.Cmp(c.lo) == 0 && (last.hiInc || c.loInc) {
			// 共享断点处函数有定义且相等（属于某个重合段），可合并
			last.hi = c.hi
			last.hiInc = c.hiInc
			last.segEv = append(last.segEv, c.segEv...)
		} else {
			merged = append(merged, c)
		}
	}
	s.coincs = merged
}

func rootValueStr(r Root) string {
	if r.Exact != nil {
		return r.Exact.RatString()
	}
	return ""
}

func rootIntervalStr(r Root) []string {
	if r.Exact != nil {
		return nil
	}
	return []string{r.Lo.RatString(), r.Hi.RatString()}
}

func (s *Solver) fillRoots() {
	for _, rr := range s.roots {
		contact := "crossing"
		if rr.mult%2 == 0 {
			contact = "tangency" // 偶重重根：切触也算相等
		}
		s.out.Roots = append(s.out.Roots, RootOut{
			Value:        rootValueStr(rr.root),
			Interval:     rootIntervalStr(rr.root),
			Approx:       rootApprox(rr.root),
			Multiplicity: rr.mult,
			Contact:      contact,
			Segment:      []string{ratStr(rr.segLo), ratStr(rr.segHi)},
		})
	}
}

func (s *Solver) fillPoles() {
	seen := map[string]bool{}
	for _, p := range s.poles {
		key := rootValueStr(p)
		if key == "" {
			key = fmt.Sprintf("%v", rootIntervalStr(p))
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		s.out.Poles = append(s.out.Poles, PointOut{
			Value:    rootValueStr(p),
			Interval: rootIntervalStr(p),
			Approx:   rootApprox(p),
			Reason:   "分母为零，函数在此无定义（极点）",
		})
	}
}
