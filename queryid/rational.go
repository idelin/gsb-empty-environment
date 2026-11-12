package queryid

import (
	"fmt"
	"math/big"
	"strings"
)

// Rat 是既约有理数：分母恒正、分子分母互素。
// 聚合参数用它做精确身份比较，1/2、2/4、0.5 得到同一个规范键。
type Rat struct {
	num *big.Int
	den *big.Int
}

// NewRat 构造 num/den 并规范化；den 为 0 时 panic。
func NewRat(num, den int64) Rat {
	if den == 0 {
		panic("queryid: zero denominator")
	}
	return normRat(big.NewInt(num), big.NewInt(den))
}

// RatFromInt 构造整数值的有理数。
func RatFromInt(v int64) Rat { return NewRat(v, 1) }

// ParseRat 解析 "p/q"、整数或有限小数（含科学计数法，按精确值转换）。
func ParseRat(s string) (Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rat{}, fmt.Errorf("queryid: empty rational")
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		num, okN := new(big.Int).SetString(strings.TrimSpace(s[:i]), 10)
		den, okD := new(big.Int).SetString(strings.TrimSpace(s[i+1:]), 10)
		if !okN || !okD || den.Sign() == 0 {
			return Rat{}, fmt.Errorf("queryid: invalid rational %q", s)
		}
		return normRat(num, den), nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Rat{}, fmt.Errorf("queryid: invalid rational %q", s)
	}
	return normRat(r.Num(), r.Denom()), nil
}

// MustRat 是 ParseRat 的 panic 版本，便于测试与常量。
func MustRat(s string) Rat {
	r, err := ParseRat(s)
	if err != nil {
		panic(err)
	}
	return r
}

func normRat(num, den *big.Int) Rat {
	if den.Sign() < 0 {
		num.Neg(num)
		den.Neg(den)
	}
	g := new(big.Int).GCD(nil, nil, num, den)
	num.Quo(num, g)
	den.Quo(den, g)
	return Rat{num: num, den: den}
}

func (r Rat) parts() (*big.Int, *big.Int) {
	if r.num == nil || r.den == nil {
		return big.NewInt(0), big.NewInt(1)
	}
	return r.num, r.den
}

// Key 返回规范字符串，如 "3"、"-1/2"。相等的有理数键必相同。
func (r Rat) Key() string {
	num, den := r.parts()
	if den.Cmp(big.NewInt(1)) == 0 {
		return num.String()
	}
	return num.String() + "/" + den.String()
}

// String 同 Key。
func (r Rat) String() string { return r.Key() }

// Equal 报告两个有理数是否相等。
func (r Rat) Equal(o Rat) bool {
	an, ad := r.parts()
	bn, bd := o.parts()
	return an.Cmp(bn) == 0 && ad.Cmp(bd) == 0
}

// Sign 返回有理数的符号：-1、0 或 +1。
func (r Rat) Sign() int {
	num, _ := r.parts()
	return num.Sign()
}
