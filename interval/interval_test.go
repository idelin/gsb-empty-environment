package interval

import (
	"math/big"
	"strings"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

func iv(lo, hi string) Interval { return New(rat(lo), rat(hi)) }

// 样本区间：正、负、跨零、退化（正/负/零）、含零端点。
var samples = []Interval{
	iv("1/3", "2"),
	iv("-2", "-1/3"),
	iv("-3/2", "5/4"),
	iv("7/8", "7/8"),
	iv("-4/3", "-4/3"),
	iv("0", "0"),
	iv("0", "3/2"),
	iv("-5/6", "0"),
	iv("1/1000", "1/7"),
	iv("-999/100", "1001/1000"),
}

// 每个区间内的采样点（两端 + 中点）。
func pointsOf(a Interval) []*big.Rat {
	mid := new(big.Rat).Add(a.Lo, a.Hi)
	mid.Quo(mid, big.NewRat(2, 1))
	return []*big.Rat{a.Lo, mid, a.Hi}
}

// 精确有理核对：对任意 x∈a, y∈b，res 必须包含 x⊕y。
func checkContains(t *testing.T, op string, res Interval, a, b Interval, f func(x, y *big.Rat) *big.Rat) {
	t.Helper()
	if res.Unbounded {
		return
	}
	for _, x := range pointsOf(a) {
		for _, y := range pointsOf(b) {
			if !res.Contains(f(x, y)) {
				t.Errorf("%s: %v 不包含真值点: x=%v y=%v", op, res, x, y)
			}
		}
	}
}

func TestArithmeticContainment(t *testing.T) {
	for _, a := range samples {
		for _, b := range samples {
			checkContains(t, "add", Add(a, b), a, b, func(x, y *big.Rat) *big.Rat {
				return new(big.Rat).Add(x, y)
			})
			checkContains(t, "sub", Sub(a, b), a, b, func(x, y *big.Rat) *big.Rat {
				return new(big.Rat).Sub(x, y)
			})
			checkContains(t, "mul", Mul(a, b), a, b, func(x, y *big.Rat) *big.Rat {
				return new(big.Rat).Mul(x, y)
			})
			if !b.ContainsZero() {
				checkContains(t, "div", Div(a, b), a, b, func(x, y *big.Rat) *big.Rat {
					return new(big.Rat).Quo(x, y)
				})
			}
		}
	}
}

// 四端点模型精确值核对（含负数与退化区间）。
func TestExactEndpoints(t *testing.T) {
	cases := []struct {
		op      string
		a, b    Interval
		lo, hi  string
		unbound bool
	}{
		{"add", iv("-3/2", "1/2"), iv("-1", "2"), "-5/2", "5/2", false},
		{"sub", iv("-3/2", "1/2"), iv("-1", "2"), "-7/2", "3/2", false},
		{"mul", iv("-3", "-1"), iv("2", "4"), "-12", "-2", false},
		{"mul", iv("-2", "3"), iv("-4", "5"), "-12", "15", false},
		{"mul", iv("-2", "-1"), iv("-4", "-3"), "3", "8", false},
		{"mul", iv("2", "2"), iv("-3", "-3"), "-6", "-6", false}, // 退化×退化
		{"mul", iv("0", "0"), iv("-5", "7"), "0", "0", false},
		{"div", iv("1", "2"), iv("1/2", "2"), "1/2", "4", false},
		{"div", iv("-1", "2"), iv("-2", "-1/2"), "-4", "2", false},
		{"div", iv("1", "2"), iv("-1", "1"), "", "", true}, // 内部含零
		{"div", iv("1", "2"), iv("0", "1"), "", "", true},  // 端点为零
		{"div", iv("1", "2"), iv("-1", "0"), "", "", true}, // 端点为零
		{"div", iv("1", "2"), iv("0", "0"), "", "", true},  // 退化为零
		{"div", iv("1", "2"), iv("1/3", "1/2"), "2", "6", false},
	}
	for _, c := range cases {
		var got Interval
		switch c.op {
		case "add":
			got = Add(c.a, c.b)
		case "sub":
			got = Sub(c.a, c.b)
		case "mul":
			got = Mul(c.a, c.b)
		case "div":
			got = Div(c.a, c.b)
		}
		if got.Unbounded != c.unbound {
			t.Errorf("%s %v %v: unbounded=%v, want %v", c.op, c.a, c.b, got.Unbounded, c.unbound)
			continue
		}
		if c.unbound {
			continue
		}
		if got.Lo.Cmp(rat(c.lo)) != 0 || got.Hi.Cmp(rat(c.hi)) != 0 {
			t.Errorf("%s %v %v = %v, want [%s, %s]", c.op, c.a, c.b, got, c.lo, c.hi)
		}
	}
}

// 连续运算：((a+b)*c - d)/e，对采样点验证包含真值。
func TestChainedOps(t *testing.T) {
	e := iv("1/2", "3/2") // 除数不含零
	sub := samples[:6]    // 覆盖正/负/跨零/退化，控制组合规模
	for _, a := range sub {
		for _, b := range sub {
			for _, c := range sub {
				for _, d := range sub {
					res := Div(Sub(Mul(Add(a, b), c), d), e)
					if res.Unbounded {
						t.Fatalf("除数不含零，不应无界: %v", res)
					}
					for _, xa := range pointsOf(a) {
						for _, xb := range pointsOf(b) {
							for _, xc := range pointsOf(c) {
								for _, xd := range pointsOf(d) {
									for _, xe := range pointsOf(e) {
										v := new(big.Rat).Add(xa, xb)
										v.Mul(v, xc)
										v.Sub(v, xd)
										v.Quo(v, xe)
										if !res.Contains(v) {
											t.Fatalf("连续运算结果 %v 不包含真值 %v", res, v)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// 输出核对：Format 的十进制区间解析回有理数后必须包含精确区间（向外舍入）。
func TestFormatOutwardRounding(t *testing.T) {
	for _, a := range samples {
		for _, b := range samples {
			for _, res := range []Interval{Add(a, b), Sub(a, b), Mul(a, b)} {
				for _, prec := range []int{0, 2, 5, 10} {
					s := res.Format(prec)
					if res.Unbounded {
						if s != "(-inf, +inf)" {
							t.Fatalf("无界区间格式错误: %s", s)
						}
						continue
					}
					body := strings.Trim(s, "[]")
					parts := strings.Split(body, ", ")
					if len(parts) != 2 {
						t.Fatalf("格式错误: %q", s)
					}
					lo, ok1 := new(big.Rat).SetString(parts[0])
					hi, ok2 := new(big.Rat).SetString(parts[1])
					if !ok1 || !ok2 {
						t.Fatalf("输出无法解析为有理数: %q", s)
					}
					if lo.Cmp(res.Lo) > 0 || hi.Cmp(res.Hi) < 0 {
						t.Errorf("prec=%d 输出 %s 未包含精确区间 %v", prec, s, res)
					}
					// 舍入误差不得超过一个最小刻度
					unit := new(big.Rat).SetInt(pow10(prec))
					unit.Inv(unit)
					if new(big.Rat).Sub(res.Lo, lo).Cmp(unit) > 0 {
						t.Errorf("prec=%d 下界舍入超出一个刻度: %s vs %v", prec, s, res)
					}
					if new(big.Rat).Sub(hi, res.Hi).Cmp(unit) > 0 {
						t.Errorf("prec=%d 上界舍入超出一个刻度: %s vs %v", prec, s, res)
					}
				}
			}
		}
	}
}

// 负数的向外舍入方向：下界必须向 -∞，上界向 +∞。
func TestNegativeRoundingDirection(t *testing.T) {
	a := iv("-1/3", "-1/7") // 两端的十进制展开都是无限循环
	got := a.Format(2)
	if got != "[-0.34, -0.14]" {
		t.Fatalf("负数向外舍入错误: got %s, want [-0.34, -0.14]", got)
	}
	b := iv("1/3", "1/3") // 退化区间，两端各自向外
	got = b.Format(2)
	if got != "[0.33, 0.34]" {
		t.Fatalf("退化区间向外舍入错误: got %s, want [0.33, 0.34]", got)
	}
	c := iv("-5", "7")
	if got := c.Format(3); got != "[-5.000, 7.000]" {
		t.Fatalf("整数端点格式错误: got %s", got)
	}
}
