package netsec

import "math/big"

// Field 表示 GF(2^n)：元素为 n 位无符号数，最低位是 x^0 项，
// 乘法在 GF(2) 上按给定的 n 次约简多项式取模。
// 本类型仅供教学实验，不对应任何真实协议实现。
type Field struct {
	n   int
	mod *big.Int // n 次约简多项式，第 n 位必须为 1
}

// NewField 以 n 次约简多项式 mod（第 n 位为最高项）构造 GF(2^n)。
func NewField(n int, mod *big.Int) *Field {
	if n <= 0 || mod.BitLen() != n+1 {
		panic("netsec: 非法的域参数")
	}
	return &Field{n: n, mod: new(big.Int).Set(mod)}
}

// Field128 返回本题规定的 GF(2^128)，约简多项式 x^128+x^7+x^2+x+1。
func Field128() *Field {
	m, _ := new(big.Int).SetString("100000000000000000000000000000087", 16)
	return NewField(128, m)
}

// Field8 返回教学用小域 GF(2^8)，约简多项式 x^8+x^4+x^3+x+1。
func Field8() *Field {
	return NewField(8, big.NewInt(0x11b))
}

// N 返回域的位数 n。
func (f *Field) N() int { return f.n }

// Zero 返回加法单位元。
func (f *Field) Zero() *big.Int { return new(big.Int) }

// One 返回乘法单位元。
func (f *Field) One() *big.Int { return big.NewInt(1) }

// FromUint 把无符号整数按位嵌入域（bit i 对应 x^i 项）。
func (f *Field) FromUint(v uint64) *big.Int {
	return f.reduce(new(big.Int).SetUint64(v))
}

// Add 即特征 2 下的按位异或。
func (f *Field) Add(a, b *big.Int) *big.Int {
	return new(big.Int).Xor(a, b)
}

// Mul 为无进位乘法后按约简多项式取模。
func (f *Field) Mul(a, b *big.Int) *big.Int {
	p := new(big.Int)
	for i := 0; i < f.n; i++ {
		if b.Bit(i) == 1 {
			p.Xor(p, new(big.Int).Lsh(a, uint(i)))
		}
	}
	return f.reduce(p)
}

// Square 即 Mul(a, a)。
func (f *Field) Square(a *big.Int) *big.Int { return f.Mul(a, a) }

// Pow 计算 a^e（e 为非负整数）。
func (f *Field) Pow(a *big.Int, e *big.Int) *big.Int {
	r := f.One()
	base := f.reduce(new(big.Int).Set(a))
	for i := 0; i < e.BitLen(); i++ {
		if e.Bit(i) == 1 {
			r = f.Mul(r, base)
		}
		base = f.Mul(base, base)
	}
	return r
}

// Inv 用二元扩展欧几里得算法求乘法逆元；0 无逆，调用方需避免。
func (f *Field) Inv(a *big.Int) *big.Int {
	if a.Sign() == 0 {
		panic("netsec: 对 0 求逆")
	}
	one := big.NewInt(1)
	u := new(big.Int).Set(a)
	v := new(big.Int).Set(f.mod)
	g1 := new(big.Int).Set(one)
	g2 := new(big.Int)
	for u.Cmp(one) != 0 {
		j := u.BitLen() - v.BitLen()
		if j < 0 {
			u, v = v, u
			g1, g2 = g2, g1
			j = -j
		}
		u.Xor(u, new(big.Int).Lsh(v, uint(j)))
		g1.Xor(g1, new(big.Int).Lsh(g2, uint(j)))
	}
	return f.reduce(g1)
}

// Eq 判断两元素是否相等。
func (f *Field) Eq(a, b *big.Int) bool { return a.Cmp(b) == 0 }

// reduce 把任意非负整数按约简多项式折回 n 位。
func (f *Field) reduce(p *big.Int) *big.Int {
	r := new(big.Int).Set(p)
	for d := r.BitLen() - 1; d >= f.n; d = r.BitLen() - 1 {
		r.Xor(r, new(big.Int).Lsh(f.mod, uint(d-f.n)))
	}
	return r
}
