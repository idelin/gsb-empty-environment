package fxsettle

import (
	"fmt"
	"math/big"
)

// RoundMode 舍入模式。
type RoundMode int

const (
	// RoundHalfUp 四舍五入：绝对值过半（含恰好一半）时远离零进位。
	RoundHalfUp RoundMode = iota
	// RoundHalfEven 银行家舍入：恰好一半时向偶数靠拢。
	RoundHalfEven
	// RoundDown 截断：向零舍入。
	RoundDown
	// RoundUp 远离零进位。
	RoundUp
)

func (m RoundMode) String() string {
	switch m {
	case RoundHalfUp:
		return "half-up"
	case RoundHalfEven:
		return "half-even"
	case RoundDown:
		return "down"
	case RoundUp:
		return "up"
	default:
		return "unknown"
	}
}

// Rat 以整数分子分母构造精确有理数。
func Rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// ParseRat 解析有理数，支持 "3/4"、"1.2345"、"-2" 等形式。
func ParseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("fxsettle: 无法解析有理数 %q", s)
	}
	return r, nil
}

// RoundTo 将 r 精确舍入到 scale 位小数。舍入只发生在调用处，
// 库内其余计算一律保持精确有理数，绝不提前舍入。
func RoundTo(r *big.Rat, scale int, mode RoundMode) *big.Rat {
	if scale < 0 {
		panic("fxsettle: scale 不能为负")
	}
	mul := pow10(scale)
	scaled := new(big.Rat).Mul(r, mul)
	q := roundInt(scaled, mode)
	return new(big.Rat).Quo(new(big.Rat).SetInt(q), mul)
}

func pow10(n int) *big.Rat {
	i := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	return new(big.Rat).SetInt(i)
}

// roundInt 按模式把有理数舍入到整数（T 除法 + 余数修正）。
func roundInt(r *big.Rat, mode RoundMode) *big.Int {
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(num, den, rem) // 向零截断，rem 与 num 同号
	if rem.Sign() == 0 {
		return q
	}
	sign := big.NewInt(int64(r.Sign()))
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1) // 2*|rem|
	cmp := twice.Cmp(den)
	switch mode {
	case RoundHalfUp:
		if cmp >= 0 {
			q.Add(q, sign)
		}
	case RoundHalfEven:
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			q.Add(q, sign)
		}
	case RoundUp:
		q.Add(q, sign)
	case RoundDown:
		// 截断即结果
	}
	return q
}

// Frac 返回精确分数形式，如 "71347/100"；整数则直接返回整数。
func Frac(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return r.String()
}

// Dec 返回十进制近似（仅用于展示，不参与计算）。
func Dec(r *big.Rat, digits int) string {
	return r.FloatString(digits)
}

// Must 包裹 ParseRat 等返回 (值, error) 的调用，出错即 panic，
// 便于在初始化常量时使用。
func Must(r *big.Rat, err error) *big.Rat {
	if err != nil {
		panic(err)
	}
	return r
}
