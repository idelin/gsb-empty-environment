// Package interval 提供有理数区间运算（误差分析）。
// 区间端点用 big.Rat 精确表示，四则运算结果严格包含真值；
// 仅在输出十进制时对两端分别向外舍入（下界向下、上界向上）。
package interval

import (
	"fmt"
	"math/big"
	"strings"
)

// Interval 表示闭区间 [Lo, Hi]。Unbounded 为真时表示无界区间
// （例如除数区间包含零），此时 Lo/Hi 无意义。
type Interval struct {
	Lo        *big.Rat
	Hi        *big.Rat
	Unbounded bool
}

// New 构造区间 [lo, hi]，自动排序两端。
func New(lo, hi *big.Rat) Interval {
	if lo.Cmp(hi) > 0 {
		lo, hi = hi, lo
	}
	return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
}

// Point 构造退化区间 [x, x]。
func Point(x *big.Rat) Interval { return New(x, x) }

// UnboundedInterval 返回无界区间标记。
func UnboundedInterval() Interval { return Interval{Unbounded: true} }

// ContainsZero 判断区间是否包含 0（含内部与端点）。
func (a Interval) ContainsZero() bool {
	if a.Unbounded {
		return true
	}
	return a.Lo.Sign() <= 0 && a.Hi.Sign() >= 0
}

// Contains 判断有理数 x 是否落在区间内。
func (a Interval) Contains(x *big.Rat) bool {
	if a.Unbounded {
		return true
	}
	return a.Lo.Cmp(x) <= 0 && a.Hi.Cmp(x) >= 0
}

func (a Interval) mapBin(b Interval, f func(x, y Interval) Interval) Interval {
	if a.Unbounded || b.Unbounded {
		return UnboundedInterval()
	}
	return f(a, b)
}

// Add 区间加法 [lo1+lo2, hi1+hi2]。
func Add(a, b Interval) Interval {
	return a.mapBin(b, func(x, y Interval) Interval {
		return Interval{
			Lo: new(big.Rat).Add(x.Lo, y.Lo),
			Hi: new(big.Rat).Add(x.Hi, y.Hi),
		}
	})
}

// Sub 区间减法 [lo1-hi2, hi1-lo2]。
func Sub(a, b Interval) Interval {
	return a.mapBin(b, func(x, y Interval) Interval {
		return Interval{
			Lo: new(big.Rat).Sub(x.Lo, y.Hi),
			Hi: new(big.Rat).Sub(x.Hi, y.Lo),
		}
	})
}

// Mul 区间乘法：四端点乘积取最小/最大（精确有理模型）。
func Mul(a, b Interval) Interval {
	return a.mapBin(b, func(x, y Interval) Interval {
		prods := [4]*big.Rat{
			new(big.Rat).Mul(x.Lo, y.Lo),
			new(big.Rat).Mul(x.Lo, y.Hi),
			new(big.Rat).Mul(x.Hi, y.Lo),
			new(big.Rat).Mul(x.Hi, y.Hi),
		}
		lo, hi := prods[0], prods[0]
		for _, p := range prods[1:] {
			if p.Cmp(lo) < 0 {
				lo = p
			}
			if p.Cmp(hi) > 0 {
				hi = p
			}
		}
		return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
	})
}

// Div 区间除法。除数区间包含零（内部或端点）时返回无界标记。
func Div(a, b Interval) Interval {
	if a.Unbounded || b.Unbounded || b.ContainsZero() {
		return UnboundedInterval()
	}
	recip := Interval{
		Lo: new(big.Rat).Inv(b.Hi),
		Hi: new(big.Rat).Inv(b.Lo),
	}
	return Mul(a, recip)
}

// pow10 返回 10^n 的整数。
func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// roundDown 返回 floor(x * 10^prec) / 10^prec（向 -∞ 舍入）。
func roundDown(x *big.Rat, prec int) *big.Rat {
	s := pow10(prec)
	num := new(big.Int).Mul(x.Num(), s)
	q := new(big.Int).Quo(num, x.Denom()) // Quo 向零截断
	// 向零截断后，若余数非零且 x 为负，需要再减 1 才是 floor
	if x.Sign() < 0 && new(big.Int).Rem(num, x.Denom()).Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(q, s)
}

// roundUp 返回 ceil(x * 10^prec) / 10^prec（向 +∞ 舍入）。
func roundUp(x *big.Rat, prec int) *big.Rat {
	return new(big.Rat).Neg(roundDown(new(big.Rat).Neg(x), prec))
}

// Format 以 prec 位小数输出区间，两端分别向外舍入，
// 保证输出的十进制区间仍包含真值区间。
func (a Interval) Format(prec int) string {
	if a.Unbounded {
		return "(-inf, +inf)"
	}
	lo := roundDown(a.Lo, prec)
	hi := roundUp(a.Hi, prec)
	return "[" + ratDecimal(lo, prec) + ", " + ratDecimal(hi, prec) + "]"
}

// ratDecimal 将分母为 10^prec 的有理数格式化为固定 prec 位小数。
func ratDecimal(x *big.Rat, prec int) string {
	s := pow10(prec)
	scaled := new(big.Rat).Mul(x, new(big.Rat).SetInt(s))
	if scaled.Denom().Cmp(big.NewInt(1)) != 0 {
		panic("ratDecimal: 内部错误，分母不是 1")
	}
	n := scaled.Num()
	neg := n.Sign() < 0
	abs := new(big.Int).Abs(n)
	digits := abs.String()
	if prec > 0 {
		for len(digits) <= prec {
			digits = "0" + digits
		}
		intPart := digits[:len(digits)-prec]
		fracPart := digits[len(digits)-prec:]
		digits = intPart + "." + fracPart
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

// String 输出精确有理形式。
func (a Interval) String() string {
	if a.Unbounded {
		return "(-inf, +inf)"
	}
	return fmt.Sprintf("[%s, %s]", a.Lo.RatString(), a.Hi.RatString())
}

// Parse 解析 "num/den" 或整数字符串为退化区间，便于测试与交互。
func Parse(s string) Interval {
	s = strings.TrimSpace(s)
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("interval.Parse: 无法解析 " + s)
	}
	return Point(r)
}
