package main

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// ---------- 有理数工具 ----------

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func absRat(x *big.Rat) *big.Rat { return new(big.Rat).Abs(x) }

// ---------- 有理区间 ----------

type Interval struct{ Lo, Hi *big.Rat }

func (v Interval) String() string { return "[" + v.Lo.RatString() + ", " + v.Hi.RatString() + "]" }

func (v Interval) contains(x *big.Rat) bool {
	return x.Cmp(v.Lo) >= 0 && x.Cmp(v.Hi) <= 0
}

// ---------- 仿射形式 ----------
//
// 仿射形式:  a = c + Σ k_i · ε_i,  ε_i ∈ [-1, 1]
// 每个噪声符号 ε_i 有全局唯一 id。同源子表达式共享同一符号 id，
// 因此 x - x 会精确抵消为 0，而不会被当作两个独立误差放大。

type Engine struct {
	names []string // 噪声符号 id -> 名字（输入名或生成的非线性余项名）
}

func NewEngine() *Engine { return &Engine{} }

func (e *Engine) newSym(name string) int {
	e.names = append(e.names, name)
	return len(e.names) - 1
}

type Affine struct {
	c     *big.Rat         // 中心值
	terms map[int]*big.Rat // 噪声符号 id -> 系数
}

func Const(c *big.Rat) *Affine {
	return &Affine{c: new(big.Rat).Set(c), terms: map[int]*big.Rat{}}
}

// NewInput 创建输入：标称值 nominal，有理误差区间 [errLo, errHi]（可不对称）。
func (e *Engine) NewInput(nominal, errLo, errHi *big.Rat, name string) *Affine {
	two := big.NewRat(2, 1)
	mid := new(big.Rat).Quo(new(big.Rat).Add(errLo, errHi), two)
	rad := new(big.Rat).Quo(new(big.Rat).Sub(errHi, errLo), two)
	c := new(big.Rat).Add(nominal, mid)
	a := &Affine{c: c, terms: map[int]*big.Rat{}}
	if rad.Sign() != 0 {
		a.terms[e.newSym(name)] = rad
	}
	return a
}

func addTerm(m map[int]*big.Rat, id int, coeff *big.Rat) {
	if coeff.Sign() == 0 {
		return
	}
	if t, ok := m[id]; ok {
		t.Add(t, coeff)
		if t.Sign() == 0 {
			delete(m, id)
		}
	} else {
		m[id] = new(big.Rat).Set(coeff)
	}
}

func (a *Affine) Add(b *Affine) *Affine {
	res := &Affine{c: new(big.Rat).Add(a.c, b.c), terms: map[int]*big.Rat{}}
	for id, k := range a.terms {
		addTerm(res.terms, id, k)
	}
	for id, k := range b.terms {
		addTerm(res.terms, id, k)
	}
	return res
}

func (a *Affine) Neg() *Affine {
	res := &Affine{c: new(big.Rat).Neg(a.c), terms: map[int]*big.Rat{}}
	for id, k := range a.terms {
		res.terms[id] = new(big.Rat).Neg(k)
	}
	return res
}

func (a *Affine) Sub(b *Affine) *Affine { return a.Add(b.Neg()) }

// devRadius 返回总偏差半径 Σ|k_i|。
func (a *Affine) devRadius() *big.Rat {
	r := new(big.Rat)
	for _, k := range a.terms {
		r.Add(r, absRat(k))
	}
	return r
}

// Range 返回保守值域区间 [c - r, c + r]。
func (a *Affine) Range() Interval {
	r := a.devRadius()
	return Interval{new(big.Rat).Sub(a.c, r), new(big.Rat).Add(a.c, r)}
}

// Mul 仿射乘法。线性部分精确保留共享符号；二次项
// (Σa_i ε_i)(Σb_j ε_j) 的幅值上界为 (Σ|a_i|)(Σ|b_j|)，
// 归入一个新的噪声符号，保证结果保守（sound）。
func (e *Engine) Mul(a, b *Affine) *Affine {
	res := &Affine{c: new(big.Rat).Mul(a.c, b.c), terms: map[int]*big.Rat{}}
	for id, ka := range a.terms {
		addTerm(res.terms, id, new(big.Rat).Mul(ka, b.c))
	}
	for id, kb := range b.terms {
		addTerm(res.terms, id, new(big.Rat).Mul(kb, a.c))
	}
	q := new(big.Rat).Mul(a.devRadius(), b.devRadius())
	if q.Sign() > 0 {
		res.terms[e.newSym(fmt.Sprintf("nl_mul#%d", len(e.names)))] = q
	}
	return res
}

// Recip 仿射倒数。若除数区间包含 0 则拒绝（返回 error）。
// 推导：1/(c+u) = 1/c - u/c² + u²/(c²(c+u))，其中 |u| ≤ r < |c|。
// 余项 |u²/(c²(c+u))| ≤ ρ²/((1-ρ)·|c|)，ρ = r/|c|，全部为有理数。
// 线性项 -u/c² 保留原噪声符号，因此 x/x 的一阶相关性被保留。
func (e *Engine) Recip(a *Affine) (*Affine, error) {
	r := a.devRadius()
	absC := absRat(a.c)
	if absC.Cmp(r) <= 0 {
		return nil, fmt.Errorf("除数区间 %v 包含 0，拒绝除法", a.Range())
	}
	one := big.NewRat(1, 1)
	rho := new(big.Rat).Quo(new(big.Rat).Set(r), absC)
	delta := new(big.Rat).Quo(
		new(big.Rat).Mul(rho, rho),
		new(big.Rat).Mul(new(big.Rat).Sub(one, rho), absC),
	)
	res := &Affine{
		c:     new(big.Rat).Quo(one, a.c),
		terms: map[int]*big.Rat{},
	}
	negInvC2 := new(big.Rat).Neg(new(big.Rat).Quo(one, new(big.Rat).Mul(a.c, a.c)))
	for id, k := range a.terms {
		addTerm(res.terms, id, new(big.Rat).Mul(k, negInvC2))
	}
	if delta.Sign() > 0 {
		res.terms[e.newSym(fmt.Sprintf("nl_recip#%d", len(e.names)))] = delta
	}
	return res, nil
}

// Div 仿射除法：a / b = a * (1/b)。除数可能过零时拒绝。
func (e *Engine) Div(a, b *Affine) (*Affine, error) {
	inv, err := e.Recip(b)
	if err != nil {
		return nil, err
	}
	return e.Mul(a, inv), nil
}

// ---------- 误差来源分析 ----------

type Contrib struct {
	Name string
	Abs  *big.Rat // 该噪声符号对输出总误差的贡献 |k_i|
}

// Contributions 按贡献降序返回所有误差来源。
func (e *Engine) Contributions(a *Affine) []Contrib {
	out := make([]Contrib, 0, len(a.terms))
	for id, k := range a.terms {
		out = append(out, Contrib{e.names[id], absRat(k)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Abs.Cmp(out[j].Abs) > 0 })
	return out
}

// TopSource 返回对输出误差影响最大的来源。
func (e *Engine) TopSource(a *Affine) Contrib {
	cs := e.Contributions(a)
	if len(cs) == 0 {
		return Contrib{"(无误差项)", new(big.Rat)}
	}
	return cs[0]
}

// Show 以符号形式打印仿射表达式。
func (e *Engine) Show(a *Affine) string {
	var sb strings.Builder
	sb.WriteString(a.c.RatString())
	ids := make([]int, 0, len(a.terms))
	for id := range a.terms {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		k := a.terms[id]
		if k.Sign() >= 0 {
			fmt.Fprintf(&sb, " + %s·ε(%s)", k.RatString(), e.names[id])
		} else {
			fmt.Fprintf(&sb, " - %s·ε(%s)", absRat(k).RatString(), e.names[id])
		}
	}
	return sb.String()
}
