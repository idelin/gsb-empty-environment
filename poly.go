package netsec

import "math/big"

// Poly 是域上的一元多项式，p[i] 为 x^i 的系数。
// 约定零多项式为 nil，非零多项式最高次系数非零（已规范化）。
type Poly []*big.Int

// polyNorm 去掉高位零系数，返回规范化多项式。
func (f *Field) polyNorm(p Poly) Poly {
	for len(p) > 0 && p[len(p)-1].Sign() == 0 {
		p = p[:len(p)-1]
	}
	return p
}

// polyDeg 返回次数，零多项式为 -1。
func polyDeg(p Poly) int { return len(p) - 1 }

// polyConst 构造常数多项式。
func (f *Field) polyConst(c *big.Int) Poly {
	if c.Sign() == 0 {
		return nil
	}
	return Poly{new(big.Int).Set(c)}
}

// PolyAdd 为特征 2 下的逐项异或。
func (f *Field) PolyAdd(a, b Poly) Poly {
	n := max(len(a), len(b))
	r := make(Poly, n)
	for i := range r {
		switch {
		case i < len(a) && i < len(b):
			r[i] = f.Add(a[i], b[i])
		case i < len(a):
			r[i] = new(big.Int).Set(a[i])
		default:
			r[i] = new(big.Int).Set(b[i])
		}
	}
	return f.polyNorm(r)
}

// PolyMul 为域上多项式乘法。
func (f *Field) PolyMul(a, b Poly) Poly {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	r := make(Poly, len(a)+len(b)-1)
	for i := range r {
		r[i] = f.Zero()
	}
	for i, ai := range a {
		if ai.Sign() == 0 {
			continue
		}
		for j, bj := range b {
			if bj.Sign() == 0 {
				continue
			}
			r[i+j] = f.Add(r[i+j], f.Mul(ai, bj))
		}
	}
	return f.polyNorm(r)
}

// PolySquare 利用特征 2 的 (a+b)^2=a^2+b^2，仅把系数移到偶数位。
func (f *Field) PolySquare(a Poly) Poly {
	if len(a) == 0 {
		return nil
	}
	r := make(Poly, 2*len(a)-1)
	for i := range r {
		r[i] = f.Zero()
	}
	for i, ai := range a {
		r[2*i] = f.Square(ai)
	}
	return f.polyNorm(r)
}

// PolyQuoRem 做带余除法，返回商与余式；b 必须非零。
func (f *Field) PolyQuoRem(a, b Poly) (q, r Poly) {
	if len(b) == 0 {
		panic("netsec: 除以零多项式")
	}
	r = f.polyNorm(append(Poly{}, a...))
	if polyDeg(r) < polyDeg(b) {
		return nil, r
	}
	q = make(Poly, polyDeg(r)-polyDeg(b)+1)
	for i := range q {
		q[i] = f.Zero()
	}
	lead := b[len(b)-1]
	invLead := lead
	if lead.Cmp(big.NewInt(1)) != 0 {
		invLead = f.Inv(lead)
	}
	for d := polyDeg(r); d >= polyDeg(b); d = polyDeg(r) {
		if r[d].Sign() == 0 {
			continue
		}
		shift := d - polyDeg(b)
		c := f.Mul(r[d], invLead)
		q[shift] = c
		for j := 0; j < len(b); j++ {
			r[shift+j] = f.Add(r[shift+j], f.Mul(c, b[j]))
		}
		r = f.polyNorm(r)
	}
	return f.polyNorm(q), f.polyNorm(r)
}

// polyMod 即 a mod b。
func (f *Field) polyMod(a, b Poly) Poly {
	_, r := f.PolyQuoRem(a, b)
	return r
}

// PolyGCD 返回首一的最大公因式；两个零多项式的 gcd 约定为零多项式。
func (f *Field) PolyGCD(a, b Poly) Poly {
	for len(b) > 0 {
		a, b = b, f.polyMod(a, b)
	}
	return f.polyMonic(a)
}

// polyMonic 把多项式化为首一（零多项式原样返回）。
func (f *Field) polyMonic(p Poly) Poly {
	if len(p) == 0 {
		return nil
	}
	inv := f.Inv(p[len(p)-1])
	r := make(Poly, len(p))
	for i, c := range p {
		r[i] = f.Mul(c, inv)
	}
	return r
}

// PolyEval 用 Horner 法在 x 处求值。
func (f *Field) PolyEval(p Poly, x *big.Int) *big.Int {
	acc := f.Zero()
	for i := len(p) - 1; i >= 0; i-- {
		acc = f.Add(f.Mul(acc, x), p[i])
	}
	return acc
}
