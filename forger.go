package netsec

import (
	"errors"
	"math/big"
)

// Status 描述攻击者当前掌握的信息状态。
type Status int

const (
	// StatusInsufficient 信息不足：约束为零多项式（如重复观测），无法限制 H。
	StatusInsufficient Status = iota
	// StatusInconsistent 观测不一致：约束为常数非零多项式或无候选可解释观测。
	StatusInconsistent
	// StatusNeedMore 需更多观测：存在多个候选，伪造标签尚不唯一。
	StatusNeedMore
	// StatusDetermined 确定伪造：候选唯一，或所有候选对新消息产生相同标签。
	StatusDetermined
)

func (s Status) String() string {
	switch s {
	case StatusInsufficient:
		return "信息不足"
	case StatusInconsistent:
		return "观测不一致"
	case StatusNeedMore:
		return "需更多观测"
	case StatusDetermined:
		return "确定伪造"
	}
	return "未知状态"
}

// Candidate 是一组自洽的密钥候选 (H, S)。
type Candidate struct {
	H *big.Int
	S *big.Int
}

// ForgedTag 记录某个候选对新消息算出的标签。
type ForgedTag struct {
	Candidate Candidate
	Tag       *big.Int
}

// Forger 维护同 nonce 观测序列、约束多项式与候选集合。
// 仅用于教学实验，不对应任何真实协议。
type Forger struct {
	f *Field

	baseMsg []*big.Int
	baseTag *big.Int

	constraint    Poly // 各消去 S 后的约束多项式的 gcd
	hasConstraint bool
	inconsistent  bool

	candidates []Candidate
}

// NewForger 在域 f 上创建伪造器。
func (f *Field) NewForger() *Forger { return &Forger{f: f} }

// tagPoly 构造消息的贡献多项式
// Q(M) = M1*X^(l+1) + ... + Ml*X^2 + l*X，使 T = S + Q(H)。
func (f *Field) tagPoly(msg []*big.Int) Poly {
	l := len(msg)
	p := make(Poly, l+2)
	for i := range p {
		p[i] = f.Zero()
	}
	for i, m := range msg {
		p[l+1-i] = f.reduce(new(big.Int).Set(m))
	}
	p[1] = f.FromUint(uint64(l))
	return f.polyNorm(p)
}

// Tag 用给定密钥材料计算消息标签，供实验宿主合成观测与核对伪造。
func (f *Field) Tag(h, s *big.Int, msg []*big.Int) *big.Int {
	return f.Add(s, f.PolyEval(f.tagPoly(msg), h))
}

// AddObservation 追加一条同 nonce 观测（1 至 3 个域元素的消息及其标签），
// 用消去 S 后的约束过滤候选根，返回当前状态。
func (g *Forger) AddObservation(msg []*big.Int, tag *big.Int) (Status, error) {
	if len(msg) < 1 || len(msg) > 3 {
		return g.status(), errors.New("netsec: 消息须含 1 至 3 个域元素")
	}
	if g.inconsistent {
		return StatusInconsistent, nil
	}
	if g.baseMsg == nil {
		g.baseMsg = append([]*big.Int{}, msg...)
		g.baseTag = new(big.Int).Set(tag)
		return StatusInsufficient, nil
	}
	// 消去 S：P(H) = Q(H) + Q_base(H) + T + T_base = 0。
	p := g.f.PolyAdd(g.f.tagPoly(msg), g.f.tagPoly(g.baseMsg))
	constTerm := g.f.Add(tag, g.baseTag)
	if len(p) == 0 {
		p = g.f.polyConst(constTerm)
	} else {
		p[0] = g.f.Add(p[0], constTerm)
		p = g.f.polyNorm(p)
	}
	switch polyDeg(p) {
	case -1:
		// 零多项式：重复观测，不提供新信息。
		return g.status(), nil
	case 0:
		// 常数非零多项式：观测互相矛盾。
		g.inconsistent = true
		g.candidates = nil
		return StatusInconsistent, nil
	}
	if !g.hasConstraint {
		g.constraint, g.hasConstraint = p, true
	} else {
		g.constraint = g.f.PolyGCD(g.constraint, p)
	}
	g.recompute()
	return g.status(), nil
}

// recompute 由当前约束多项式求全部不同根 H，并为每根恢复 S。
func (g *Forger) recompute() {
	roots := g.f.Roots(g.constraint)
	if len(roots) == 0 {
		g.inconsistent = true
		g.candidates = nil
		return
	}
	baseQ := g.f.tagPoly(g.baseMsg)
	g.candidates = make([]Candidate, len(roots))
	for i, h := range roots {
		s := g.f.Add(g.baseTag, g.f.PolyEval(baseQ, h))
		g.candidates[i] = Candidate{H: h, S: s}
	}
}

func (g *Forger) status() Status {
	switch {
	case g.inconsistent:
		return StatusInconsistent
	case !g.hasConstraint:
		return StatusInsufficient
	case len(g.candidates) == 1:
		return StatusDetermined
	default:
		return StatusNeedMore
	}
}

// Status 返回当前信息状态。
func (g *Forger) Status() Status { return g.status() }

// Candidates 返回当前候选集合的副本。
func (g *Forger) Candidates() []Candidate {
	return append([]Candidate{}, g.candidates...)
}

// Forge 对新消息输出各候选产生的标签；
// 只有所有候选标签一致时才判定为确定伪造。
func (g *Forger) Forge(msg []*big.Int) ([]ForgedTag, Status, error) {
	if len(msg) < 1 || len(msg) > 3 {
		return nil, g.status(), errors.New("netsec: 消息须含 1 至 3 个域元素")
	}
	st := g.status()
	if st == StatusInsufficient || st == StatusInconsistent {
		return nil, st, nil
	}
	tags := make([]ForgedTag, len(g.candidates))
	agree := true
	for i, c := range g.candidates {
		t := g.f.Tag(c.H, c.S, msg)
		tags[i] = ForgedTag{Candidate: c, Tag: t}
		if i > 0 && !g.f.Eq(tags[0].Tag, t) {
			agree = false
		}
	}
	if agree {
		st = StatusDetermined
	} else {
		st = StatusNeedMore
	}
	return tags, st, nil
}
