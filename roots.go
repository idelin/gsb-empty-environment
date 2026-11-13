package netsec

import (
	"math/big"
	"sort"
)

// splitmix64 是确定性的伪随机源，用于把拆分系数散布到整个域，
// 避免小整数试探落在迹为零的子空间上（例如 x^128+x^7+x^2+x+1
// 的所有低次单项式迹都为 0）。
type splitmix64 struct{ state uint64 }

func (s *splitmix64) next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// spreadElem 用伪随机序列生成一个非零域元素（确定性，可复现）。
func (f *Field) spreadElem(s *splitmix64) *big.Int {
	for {
		v := new(big.Int)
		for i := 0; i < f.n; i += 64 {
			w := s.next()
			for b := 0; b < 64 && i+b < f.n; b++ {
				if (w>>uint(b))&1 == 1 {
					v.SetBit(v, i+b, 1)
				}
			}
		}
		if v.Sign() != 0 {
			return f.reduce(v)
		}
	}
}

// Roots 返回 p 在域中的全部不同根（按数值升序）。
// 零多项式与常数多项式没有可报告的根，返回 nil；
// 算法先做互异根分离再递归拆分，不做全域枚举，
// 因此重复根只报告一次，H=0 也能正常命中。
func (f *Field) Roots(p Poly) []*big.Int {
	p = f.polyNorm(p)
	if polyDeg(p) < 1 {
		return nil
	}
	// g = gcd(p, X^(2^n) - X)：p 在域内全部互异根的乘积。
	xq := f.frobeniusPowMod(p)
	diff := f.PolyAdd(xq, Poly{f.Zero(), f.One()})
	g := f.PolyGCD(p, diff)
	if polyDeg(g) < 1 {
		return nil
	}
	var roots []*big.Int
	f.splitRoots(g, &roots)
	sort.Slice(roots, func(i, j int) bool { return roots[i].Cmp(roots[j]) < 0 })
	return roots
}

// frobeniusPowMod 计算 X^(2^n) mod m：连续 n 次平方取模。
func (f *Field) frobeniusPowMod(m Poly) Poly {
	r := Poly{f.Zero(), f.One()} // X
	for i := 0; i < f.n; i++ {
		r = f.polyMod(f.PolySquare(r), m)
	}
	return r
}

// splitRoots 把完全分裂于域内的首一多项式 g 拆成一次因子，
// 用迹映射 Tr(a*X) 做等次数拆分，a 按确定顺序试探。
func (f *Field) splitRoots(g Poly, roots *[]*big.Int) {
	if polyDeg(g) == 1 {
		// 首一一次因子 X + c 的根即 c。
		*roots = append(*roots, f.Mul(g[0], f.Inv(g[1])))
		return
	}
	rng := &splitmix64{state: 0x5bd1e995}
	for tries := 0; tries < 4096; tries++ {
		a := f.spreadElem(rng)
		t := f.traceMod(a, g)
		h := f.PolyGCD(g, t)
		if polyDeg(h) > 0 && polyDeg(h) < polyDeg(g) {
			f.splitRoots(f.polyMonic(h), roots)
			q, _ := f.PolyQuoRem(g, h)
			f.splitRoots(f.polyMonic(q), roots)
			return
		}
	}
	panic("netsec: 多项式拆分失败（g 未在域内完全分裂）")
}

// traceMod 计算 Tr(a*X) mod g，其中 Tr(Y) = sum_{i=0}^{n-1} Y^(2^i)。
func (f *Field) traceMod(a *big.Int, g Poly) Poly {
	y := f.polyMod(Poly{f.Zero(), a}, g)
	t := Poly{}
	for i := 0; i < f.n; i++ {
		t = f.PolyAdd(t, y)
		y = f.polyMod(f.PolySquare(y), g)
	}
	return t
}
