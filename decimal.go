package netsec

import (
	"fmt"
	"math/big"
	"strings"
)

// RoundingMode 定义服务端按币种对配置的舍入规则。
type RoundingMode int

const (
	RoundHalfUp   RoundingMode = iota // 四舍五入(远离零)
	RoundHalfEven                     // 银行家舍入
	RoundDown                         // 截断(向零)
	RoundUp                           // 进位(远离零)
)

func (m RoundingMode) String() string {
	switch m {
	case RoundHalfUp:
		return "HALF_UP"
	case RoundHalfEven:
		return "HALF_EVEN"
	case RoundDown:
		return "DOWN"
	case RoundUp:
		return "UP"
	default:
		return "UNKNOWN"
	}
}

// Decimal 是十进制不动点数:value = coef * 10^exp。
// 所有运算精确,不存在二进制浮点误差。
type Decimal struct {
	coef *big.Int
	exp  int32
}

// NewDecimal 从字符串解析十进制数,如 "1234.5678"、"-0.05"、"7e3"。
func NewDecimal(s string) (Decimal, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Decimal{}, fmt.Errorf("decimal: empty string")
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	expPart := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := parseInt64(s[i+1:])
		if err != nil {
			return Decimal{}, fmt.Errorf("decimal: bad exponent in %q", orig)
		}
		expPart = e
		s = s[:i]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" && fracPart == "" {
		return Decimal{}, fmt.Errorf("decimal: no digits in %q", orig)
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return Decimal{}, fmt.Errorf("decimal: invalid digit in %q", orig)
		}
	}
	coef, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("decimal: cannot parse %q", orig)
	}
	if neg {
		coef.Neg(coef)
	}
	exp := expPart - int64(len(fracPart))
	if exp > 1<<30 || exp < -(1<<30) {
		return Decimal{}, fmt.Errorf("decimal: exponent out of range in %q", orig)
	}
	return Decimal{coef: coef, exp: int32(exp)}.norm(), nil
}

func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || !v.IsInt64() {
		return 0, fmt.Errorf("bad int %q", s)
	}
	return v.Int64(), nil
}

// MustDecimal 解析失败即 panic,仅供常量与测试使用。
func MustDecimal(s string) Decimal {
	d, err := NewDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalFromInt 由整数构造。
func DecimalFromInt(v int64) Decimal {
	return Decimal{coef: big.NewInt(v), exp: 0}
}

func (d Decimal) norm() Decimal {
	if d.coef == nil {
		d.coef = new(big.Int)
	}
	if d.coef.Sign() == 0 {
		return Decimal{coef: new(big.Int), exp: 0}
	}
	// 裁剪尾零,规范化表示(7.10 -> 7.1)。
	coef := new(big.Int).Set(d.coef)
	exp := d.exp
	ten := big.NewInt(10)
	q, r := new(big.Int), new(big.Int)
	for exp < 0 {
		q.QuoRem(coef, ten, r)
		if r.Sign() != 0 {
			break
		}
		coef.Set(q)
		exp++
	}
	return Decimal{coef: coef, exp: exp}
}

// IsZero 报告是否为零。
func (d Decimal) IsZero() bool { return d.coef.Sign() == 0 }

// Sign 返回 -1、0、1。
func (d Decimal) Sign() int { return d.coef.Sign() }

// align 把两个数对齐到相同指数,返回对齐后的系数与该指数。
func align(a, b Decimal) (x, y *big.Int, exp int32) {
	exp = a.exp
	if b.exp < a.exp {
		exp = b.exp
	}
	x = new(big.Int).Set(a.coef)
	y = new(big.Int).Set(b.coef)
	if da := int64(a.exp) - int64(exp); da > 0 {
		x.Mul(x, pow10(da))
	}
	if db := int64(b.exp) - int64(exp); db > 0 {
		y.Mul(y, pow10(db))
	}
	return x, y, exp
}

func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// Add 精确加法。
func (d Decimal) Add(o Decimal) Decimal {
	x, y, exp := align(d, o)
	return Decimal{coef: x.Add(x, y), exp: exp}.norm()
}

// Sub 精确减法。
func (d Decimal) Sub(o Decimal) Decimal {
	x, y, exp := align(d, o)
	return Decimal{coef: x.Sub(x, y), exp: exp}.norm()
}

// Mul 精确乘法。
func (d Decimal) Mul(o Decimal) Decimal {
	return Decimal{
		coef: new(big.Int).Mul(d.coef, o.coef),
		exp:  d.exp + o.exp,
	}.norm()
}

// Neg 取相反数。
func (d Decimal) Neg() Decimal {
	return Decimal{coef: new(big.Int).Neg(d.coef), exp: d.exp}
}

// Cmp 比较大小:-1 小于,0 相等,1 大于。
func (d Decimal) Cmp(o Decimal) int {
	x, y, _ := align(d, o)
	return x.Cmp(y)
}

// Equal 数值相等(忽略表示形式)。
func (d Decimal) Equal(o Decimal) bool { return d.Cmp(o) == 0 }

// Quantize 按服务端配置的最小单位 scale(小数位)与舍入规则归约。
func (d Decimal) Quantize(scale int32, mode RoundingMode) Decimal {
	target := -scale
	if d.exp >= target {
		// 当前精度不高于目标,直接左移补齐。
		mult := pow10(int64(d.exp) - int64(target))
		return Decimal{coef: new(big.Int).Mul(d.coef, mult), exp: target}.norm()
	}
	div := pow10(int64(target) - int64(d.exp))
	q, r := new(big.Int).QuoRem(d.coef, div, new(big.Int)) // 向零截断
	if r.Sign() != 0 {
		switch mode {
		case RoundUp:
			if d.coef.Sign() > 0 {
				q.Add(q, big.NewInt(1))
			} else {
				q.Sub(q, big.NewInt(1))
			}
		case RoundHalfUp, RoundHalfEven:
			// 比较 2*|r| 与 div
			twice := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
			cmp := twice.Cmp(div)
			round := cmp > 0
			if cmp == 0 {
				if mode == RoundHalfUp {
					round = true
				} else {
					round = new(big.Int).Abs(q).Bit(0) == 1 // 奇数则进
				}
			}
			if round {
				if d.coef.Sign() > 0 {
					q.Add(q, big.NewInt(1))
				} else {
					q.Sub(q, big.NewInt(1))
				}
			}
		case RoundDown:
			// QuoRem 已向零截断
		}
	}
	return Decimal{coef: q, exp: target}.norm()
}

// String 输出普通十进制表示。
func (d Decimal) String() string {
	if d.coef.Sign() == 0 {
		return "0"
	}
	neg := d.coef.Sign() < 0
	digits := new(big.Int).Abs(d.coef).String()
	var out string
	switch {
	case d.exp >= 0:
		out = digits + strings.Repeat("0", int(d.exp))
	case -int(d.exp) < len(digits):
		p := len(digits) + int(d.exp)
		out = digits[:p] + "." + digits[p:]
	default:
		out = "0." + strings.Repeat("0", -int(d.exp)-len(digits)) + digits
	}
	if neg {
		return "-" + out
	}
	return out
}

// MarshalJSON 以字符串形式编码,避免经由浮点丢失精度。
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON 接受字符串或裸数字形式。
func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := NewDecimal(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
