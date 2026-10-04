package main

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly 是有理系数多项式，系数按升幂排列（c[0] 为常数项）。
// 零多项式用空切片表示；非零多项式最高次系数非零（规范化不变量）。
type Poly struct {
	c []*big.Rat
}

// NewPoly 由系数构造并规范化多项式。
func NewPoly(coeffs ...*big.Rat) Poly {
	p := Poly{c: coeffs}
	return p.normalize()
}

// PolyFromStrings 解析形如 "3/2"、"1.5"、"-2" 的系数列表。
func PolyFromStrings(ss []string) (Poly, error) {
	coeffs := make([]*big.Rat, len(ss))
	for i, s := range ss {
		r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
		if !ok {
			return Poly{}, fmt.Errorf("无法解析有理数 %q", s)
		}
		coeffs[i] = r
	}
	return NewPoly(coeffs...), nil
}

func ratConst(v string) *big.Rat {
	r, _ := new(big.Rat).SetString(v)
	return r
}

// Zero 返回零多项式。
func Zero() Poly { return Poly{} }

// Const 返回常数多项式。
func Const(r *big.Rat) Poly {
	if r.Sign() == 0 {
		return Zero()
	}
	return Poly{c: []*big.Rat{new(big.Rat).Set(r)}}
}

func (p Poly) normalize() Poly {
	n := len(p.c)
	for n > 0 && (p.c[n-1] == nil || p.c[n-1].Sign() == 0) {
		n--
	}
	return Poly{c: p.c[:n]}
}

// Deg 返回次数，零多项式为 -1。
func (p Poly) Deg() int { return len(p.c) - 1 }

func (p Poly) IsZero() bool { return len(p.c) == 0 }

// Lead 返回首项系数（零多项式返回 0）。
func (p Poly) Lead() *big.Rat {
	if p.IsZero() {
		return new(big.Rat)
	}
	return p.c[len(p.c)-1]
}

func (p Poly) coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return p.c[i]
}

func (p Poly) Copy() Poly {
	q := Poly{c: make([]*big.Rat, len(p.c))}
	for i, c := range p.c {
		q.c[i] = new(big.Rat).Set(c)
	}
	return q
}

func (p Poly) Equal(q Poly) bool {
	if len(p.c) != len(q.c) {
		return false
	}
	for i := range p.c {
		if p.c[i].Cmp(q.c[i]) != 0 {
			return false
		}
	}
	return true
}

func (p Poly) Add(q Poly) Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	r := make([]*big.Rat, n)
	for i := range r {
		r[i] = new(big.Rat).Add(p.coeff(i), q.coeff(i))
	}
	return Poly{c: r}.normalize()
}

func (p Poly) Neg() Poly {
	r := make([]*big.Rat, len(p.c))
	for i, c := range p.c {
		r[i] = new(big.Rat).Neg(c)
	}
	return Poly{c: r}
}

func (p Poly) Sub(q Poly) Poly { return p.Add(q.Neg()) }

func (p Poly) Mul(q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Zero()
	}
	r := make([]*big.Rat, len(p.c)+len(q.c)-1)
	for i := range r {
		r[i] = new(big.Rat)
	}
	for i, a := range p.c {
		for j, b := range q.c {
			r[i+j].Add(r[i+j], new(big.Rat).Mul(a, b))
		}
	}
	return Poly{c: r}.normalize()
}

// Scale 乘以标量。
func (p Poly) Scale(s *big.Rat) Poly {
	if s.Sign() == 0 {
		return Zero()
	}
	r := make([]*big.Rat, len(p.c))
	for i, c := range p.c {
		r[i] = new(big.Rat).Mul(c, s)
	}
	return Poly{c: r}.normalize()
}

// Monic 化为首一多项式。
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.Lead()))
}

// DivMod 多项式带余除法：p = q*quo + rem。
func (p Poly) DivMod(q Poly) (quo, rem Poly) {
	if q.IsZero() {
		panic("多项式除以零")
	}
	rem = p.Copy()
	quot := Zero()
	for !rem.IsZero() && rem.Deg() >= q.Deg() {
		d := rem.Deg() - q.Deg()
		c := new(big.Rat).Quo(rem.Lead(), q.Lead())
		tc := make([]*big.Rat, d+1)
		for i := range tc {
			tc[i] = new(big.Rat)
		}
		tc[d] = c
		t := Poly{c: tc}
		quot = quot.Add(t)
		rem = rem.Sub(q.Mul(t))
	}
	return quot, rem
}

// Div 整除（要求余数为零，否则 panic）。
func (p Poly) Div(q Poly) Poly {
	quo, rem := p.DivMod(q)
	if !rem.IsZero() {
		panic("多项式不能整除")
	}
	return quo
}

// GCD 返回首一最大公因式；GCD(0,0) 为零多项式。
func (p Poly) GCD(q Poly) Poly {
	a, b := p, q
	for !b.IsZero() {
		_, r := a.DivMod(b)
		a, b = b, r
	}
	if a.IsZero() {
		return Zero()
	}
	return a.Monic()
}

// Deriv 形式导数。
func (p Poly) Deriv() Poly {
	if len(p.c) <= 1 {
		return Zero()
	}
	r := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		r[i-1] = new(big.Rat).Mul(p.c[i], big.NewRat(int64(i), 1))
	}
	return Poly{c: r}.normalize()
}

// SquareFree 返回无平方部分 p / gcd(p, p')。
func (p Poly) SquareFree() Poly {
	if p.IsZero() || p.Deg() == 0 {
		return p
	}
	g := p.GCD(p.Deriv())
	if g.IsZero() {
		return p
	}
	return p.Div(g)
}

// Eval 在有理点处精确求值（Horner）。
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.c[i])
	}
	return acc
}

// EvalFloat 浮点求值。
func (p Poly) EvalFloat(x float64) float64 {
	acc := 0.0
	for i := len(p.c) - 1; i >= 0; i-- {
		f, _ := p.c[i].Float64()
		acc = acc*x + f
	}
	return acc
}

// Deflate 除以 (x-r)^k，返回商与重数 k。
func (p Poly) Deflate(r *big.Rat) (Poly, int) {
	factor := NewPoly(new(big.Rat).Neg(r), big.NewRat(1, 1))
	cur := p
	k := 0
	for cur.Deg() >= 1 {
		quo, rem := cur.DivMod(factor)
		if !rem.IsZero() {
			break
		}
		cur = quo
		k++
	}
	return cur, k
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := len(p.c) - 1; i >= 0; i-- {
		c := p.c[i]
		if c.Sign() == 0 {
			continue
		}
		cs := c.RatString()
		switch i {
		case 0:
			terms = append(terms, fmt.Sprintf("(%s)", cs))
		case 1:
			terms = append(terms, fmt.Sprintf("(%s)*x", cs))
		default:
			terms = append(terms, fmt.Sprintf("(%s)*x^%d", cs, i))
		}
	}
	return strings.Join(terms, " + ")
}

// CoeffStrings 以字符串形式返回升幂系数。
func (p Poly) CoeffStrings() []string {
	out := make([]string, len(p.c))
	for i, c := range p.c {
		out[i] = c.RatString()
	}
	if len(out) == 0 {
		out = []string{"0"}
	}
	return out
}

// ---------------- Sturm 序列与根隔离 ----------------

// Sturm 构造 Sturm 序列。p 为零或常数多项式时返回单元素序列。
func (p Poly) Sturm() []Poly {
	if p.IsZero() || p.Deg() == 0 {
		return []Poly{p}
	}
	seq := []Poly{p, p.Deriv()}
	if seq[1].IsZero() {
		return seq[:1]
	}
	for {
		n := len(seq)
		_, rem := seq[n-2].DivMod(seq[n-1])
		rem = rem.Neg()
		if rem.IsZero() {
			break
		}
		seq = append(seq, rem)
	}
	return seq
}

// signVariations 计算序列在 x 处的变号数（忽略零值）。
func signVariations(seq []Poly, x *big.Rat) int {
	signs := 0
	prev := 0
	for _, s := range seq {
		v := s.Eval(x).Sign()
		if v == 0 {
			continue
		}
		if prev != 0 && v != prev {
			signs++
		}
		prev = v
	}
	return signs
}

// CountRoots 返回 (a, b] 内不同实根个数（不计重数）。
// 采用“忽略零值”的变号约定，对 a、b 恰好为根的情形依然成立。
func (p Poly) CountRoots(a, b *big.Rat) int {
	if p.IsZero() || p.Deg() <= 0 {
		return 0
	}
	seq := p.Sturm()
	return signVariations(seq, a) - signVariations(seq, b)
}

// Root 表示一个被隔离的实根：要么精确已知，要么落在 (Lo, Hi] 内。
type Root struct {
	Exact *big.Rat // 非空表示精确根
	Lo    *big.Rat
	Hi    *big.Rat
	Count int // 区间内不同根个数（正常为 1）
}

// CauchyBound 返回全部实根的严格上界 M（|root| < M）。
func (p Poly) CauchyBound() *big.Rat {
	if p.IsZero() || p.Deg() == 0 {
		return big.NewRat(1, 1)
	}
	lead := new(big.Rat).Abs(p.Lead())
	maxc := new(big.Rat)
	for _, c := range p.c[:len(p.c)-1] {
		a := new(big.Rat).Abs(c)
		if a.Cmp(maxc) > 0 {
			maxc = a
		}
	}
	m := new(big.Rat).Quo(maxc, lead)
	m.Add(m, big.NewRat(1, 1))
	return m
}

// IsolateRoots 隔离 p 在 (a, b] 内的全部不同实根。
// 遇到有理点处精确为零时做因式降阶，保证终止且不丢根。
func IsolateRoots(p Poly, a, b, eps *big.Rat) []Root {
	if p.IsZero() || p.Deg() <= 0 {
		return nil
	}
	var rec func(f Poly, lo, hi *big.Rat) []Root
	rec = func(f Poly, lo, hi *big.Rat) []Root {
		n := f.CountRoots(lo, hi)
		if n == 0 {
			return nil
		}
		if f.Eval(hi).Sign() == 0 {
			q, _ := f.Deflate(hi)
			roots := rec(q, lo, hi)
			return append(roots, Root{Exact: new(big.Rat).Set(hi)})
		}
		width := new(big.Rat).Sub(hi, lo)
		if width.Cmp(eps) <= 0 {
			return []Root{{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi), Count: n}}
		}
		mid := new(big.Rat).Quo(new(big.Rat).Add(lo, hi), big.NewRat(2, 1))
		if f.Eval(mid).Sign() == 0 {
			q, _ := f.Deflate(mid)
			left := rec(q, lo, mid)
			right := rec(q, mid, hi)
			res := append(left, Root{Exact: new(big.Rat).Set(mid)})
			return append(res, right...)
		}
		left := rec(f, lo, mid)
		right := rec(f, mid, hi)
		return append(left, right...)
	}
	return rec(p, a, b)
}

// MultiplicityIn 返回 p 在 (lo, hi] 内唯一根的重数（用于切触/穿越判定）。
// 前提：该区间内恰有一个不同根。
func (p Poly) MultiplicityIn(lo, hi *big.Rat) int {
	m := 1
	d := p
	for k := 1; ; k++ {
		d = d.Deriv()
		if d.IsZero() {
			return m
		}
		g := p.GCD(d)
		if g.Deg() <= 0 || g.CountRoots(lo, hi) == 0 {
			return m
		}
		m = k + 1
	}
}
