package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Form 是一个仿射形式: center + sum(coeffs[i] * e_i), 每个噪声符号 e_i ∈ [-1,1]。
// 关键性质: 同一个符号 id 在所有表达式中代表同一个误差源,
// 因此共享节点的相关性被保留, 重复输入不会被当作独立变量放大。
type Form struct {
	center *big.Rat
	coeffs map[int]*big.Rat // 噪声符号 id -> 系数 (有理数, 精确)
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func zero() *big.Rat { return new(big.Rat) }

func constForm(c *big.Rat) *Form {
	return &Form{center: new(big.Rat).Set(c), coeffs: map[int]*big.Rat{}}
}

// radius 返回所有噪声系数绝对值之和 (保守半径)。
func (f *Form) radius() *big.Rat {
	r := zero()
	for _, c := range f.coeffs {
		r.Add(r, new(big.Rat).Abs(c))
	}
	return r
}

// interval 返回保守区间 [center - r, center + r]。
func (f *Form) interval() (lo, hi *big.Rat) {
	r := f.radius()
	return new(big.Rat).Sub(f.center, r), new(big.Rat).Add(f.center, r)
}

func (f *Form) add(g *Form) *Form {
	out := &Form{center: new(big.Rat).Add(f.center, g.center), coeffs: map[int]*big.Rat{}}
	for id, c := range f.coeffs {
		out.coeffs[id] = new(big.Rat).Set(c)
	}
	for id, c := range g.coeffs {
		if old, ok := out.coeffs[id]; ok {
			old.Add(old, c)
		} else {
			out.coeffs[id] = new(big.Rat).Set(c)
		}
	}
	out.prune()
	return out
}

func (f *Form) neg() *Form {
	out := &Form{center: new(big.Rat).Neg(f.center), coeffs: map[int]*big.Rat{}}
	for id, c := range f.coeffs {
		out.coeffs[id] = new(big.Rat).Neg(c)
	}
	return out
}

func (f *Form) sub(g *Form) *Form { return f.add(g.neg()) }

func (f *Form) scale(k *big.Rat) *Form {
	out := &Form{center: new(big.Rat).Mul(f.center, k), coeffs: map[int]*big.Rat{}}
	for id, c := range f.coeffs {
		out.coeffs[id] = new(big.Rat).Mul(c, k)
	}
	out.prune()
	return out
}

func (f *Form) shift(k *big.Rat) *Form {
	out := &Form{center: new(big.Rat).Add(f.center, k), coeffs: map[int]*big.Rat{}}
	for id, c := range f.coeffs {
		out.coeffs[id] = new(big.Rat).Set(c)
	}
	return out
}

// mul 仿射乘法: 线性部分精确, 二次项 (Σ|ai|)(Σ|bi|) 归入一个新噪声符号。
func (f *Form) mul(g *Form, freshSym int) *Form {
	out := &Form{center: new(big.Rat).Mul(f.center, g.center), coeffs: map[int]*big.Rat{}}
	acc := func(id int, c *big.Rat) {
		if old, ok := out.coeffs[id]; ok {
			old.Add(old, c)
		} else {
			out.coeffs[id] = c
		}
	}
	for id, c := range f.coeffs {
		acc(id, new(big.Rat).Mul(c, g.center))
	}
	for id, c := range g.coeffs {
		acc(id, new(big.Rat).Mul(c, f.center))
	}
	rad := new(big.Rat).Mul(f.radius(), g.radius())
	if rad.Sign() != 0 {
		acc(freshSym, rad)
	}
	out.prune()
	return out
}

func (f *Form) prune() {
	for id, c := range f.coeffs {
		if c.Sign() == 0 {
			delete(f.coeffs, id)
		}
	}
}

// recip 计算 1/f 的保守仿射形式。f 的区间跨越 0 时返回错误 (拒绝)。
// 方法: 弦斜率 α = -1/(lo*hi), 余项界 δ = L*(hi-lo)/2,
// 其中 L = max(|α + 1/lo²|, |α + 1/hi²|) 是 chord-f 导数的界,
// 全部用有理数精确计算, 保证包含真实值域。
func (f *Form) recip(freshSym int) (*Form, error) {
	lo, hi := f.interval()
	if lo.Sign() <= 0 && hi.Sign() >= 0 {
		return nil, fmt.Errorf("除数区间 [%s, %s] 包含 0, 拒绝计算", lo.RatString(), hi.RatString())
	}
	one := rat("1")
	fLo := new(big.Rat).Quo(one, lo) // 1/lo
	fHi := new(big.Rat).Quo(one, hi) // 1/hi
	span := new(big.Rat).Sub(hi, lo)
	alpha := new(big.Rat).Quo(new(big.Rat).Sub(fHi, fLo), span) // 弦斜率
	mid := new(big.Rat).Quo(new(big.Rat).Add(lo, hi), rat("2"))
	zeta := new(big.Rat).Sub(new(big.Rat).Quo(new(big.Rat).Add(fLo, fHi), rat("2")), new(big.Rat).Mul(alpha, mid))
	// L = max(|alpha + 1/lo²|, |alpha + 1/hi²|)
	dLo := new(big.Rat).Abs(new(big.Rat).Add(alpha, new(big.Rat).Quo(one, new(big.Rat).Mul(lo, lo))))
	dHi := new(big.Rat).Abs(new(big.Rat).Add(alpha, new(big.Rat).Quo(one, new(big.Rat).Mul(hi, hi))))
	L := dLo
	if dHi.Cmp(dLo) > 0 {
		L = dHi
	}
	delta := new(big.Rat).Mul(L, new(big.Rat).Quo(span, rat("2")))
	out := f.scale(alpha).shift(zeta)
	if delta.Sign() != 0 {
		out.coeffs[freshSym] = new(big.Rat).Set(delta)
	}
	return out, nil
}

// sensitivity 返回每个误差源的贡献 |系数| (符号 ∈ [-1,1], 贡献即 |系数|), 按降序。
func (f *Form) sensitivity(symName func(int) string) []Source {
	var list []Source
	for id, c := range f.coeffs {
		list = append(list, Source{Name: symName(id), Impact: new(big.Rat).Abs(c)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Impact.Cmp(list[j].Impact) > 0 })
	return list
}

// Source 是一个误差来源及其对输出的影响量。
type Source struct {
	Name   string
	Impact *big.Rat
}
