package main

import (
	"math/big"
	"testing"
)

var testGrid = []*big.Rat{rat("-1"), rat("-1/2"), rat("0"), rat("1/2"), rat("1")}

func width(n *Node) *big.Rat {
	lo, hi := n.Bound()
	return new(big.Rat).Sub(hi, lo)
}

// x - x 必须精确为 0 (同源相关性保留)。
func TestCancellationShared(t *testing.T) {
	e := NewEngine()
	x := e.Input("x", rat("9/10"), rat("11/10"))
	d := e.Sub(x, x)
	if w := width(d); w.Sign() != 0 {
		t.Fatalf("x - x 宽度应为 0, 得到 %s", w.RatString())
	}
}

// 两个独立变量相减必须放大: 宽度为两者宽度之和。
func TestIndependentSubtraction(t *testing.T) {
	e := NewEngine()
	x := e.Input("x", rat("9/10"), rat("11/10"))
	y := e.Input("y", rat("9/10"), rat("11/10"))
	d := e.Sub(x, y)
	if w := width(d); w.Cmp(rat("2/5")) != 0 {
		t.Fatalf("x - y 宽度应为 2/5, 得到 %s", w.RatString())
	}
}

// 共享中间节点: (x+1)-x == 1 精确。
func TestSharedIntermediate(t *testing.T) {
	e := NewEngine()
	x := e.Input("x", rat("1"), rat("2"))
	one := e.Const(rat("1"))
	d := e.Sub(e.Add(x, one), x)
	lo, hi := d.Bound()
	if lo.Cmp(rat("1")) != 0 || hi.Cmp(rat("1")) != 0 {
		t.Fatalf("(x+1)-x 应为 [1,1], 得到 [%s,%s]", lo.RatString(), hi.RatString())
	}
}

// 除数区间跨 0 必须拒绝。
func TestDivByZeroCrossingRejected(t *testing.T) {
	e := NewEngine()
	x := e.Input("x", rat("1"), rat("2"))
	y := e.Input("y", rat("-1"), rat("2"))
	if _, err := e.Div(x, y); err == nil {
		t.Fatal("除数跨 0 应被拒绝")
	}
}

// 除数含 0 端点也拒绝。
func TestDivByZeroEndpointRejected(t *testing.T) {
	e := NewEngine()
	y := e.Input("y", rat("0"), rat("2"))
	if _, err := e.Div(e.Const(rat("1")), y); err == nil {
		t.Fatal("除数含 0 端点应被拒绝")
	}
}

// 多种表达式在有限小域上的精确枚举包含验证。
func TestContainment(t *testing.T) {
	cases := []struct {
		name  string
		build func(e *Engine) (*Node, error)
	}{
		{"mul", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("1"), rat("2"))
			y := e.Input("y", rat("-1"), rat("1"))
			return e.Mul(x, y), nil
		}},
		{"mul-add", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("1"), rat("2"))
			y := e.Input("y", rat("-1"), rat("1"))
			z := e.Input("z", rat("0"), rat("1"))
			return e.Add(e.Mul(x, y), z), nil
		}},
		{"shared-square", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("1"), rat("2"))
			y := e.Input("y", rat("-1"), rat("1"))
			s := e.Add(x, y)
			return e.Mul(s, s), nil
		}},
		{"div", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("1"), rat("2"))
			return e.Div(e.Const(rat("1")), e.Add(x, e.Const(rat("1"))))
		}},
		{"div-compound", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("1"), rat("2"))
			y := e.Input("y", rat("3"), rat("4"))
			return e.Div(e.Add(e.Mul(x, y), x), e.Add(x, e.Const(rat("2"))))
		}},
		{"div-negative-den", func(e *Engine) (*Node, error) {
			x := e.Input("x", rat("-3"), rat("-2"))
			return e.Div(e.Const(rat("1")), x)
		}},
	}
	for _, c := range cases {
		e := NewEngine()
		n, err := c.build(e)
		if err != nil {
			t.Fatalf("%s: 构建失败: %v", c.name, err)
		}
		mn, mx, ok, err := VerifyContainment(n, testGrid)
		if err != nil {
			t.Fatalf("%s: 枚举失败: %v", c.name, err)
		}
		if !ok {
			lo, hi := n.Bound()
			t.Fatalf("%s: 真实值域 [%s,%s] 未被保守界 [%s,%s] 包含",
				c.name, mn.RatString(), mx.RatString(), lo.RatString(), hi.RatString())
		}
	}
}

// 灵敏度排名: 最大贡献来源应排在最前, 且贡献之和等于半径。
func TestSensitivity(t *testing.T) {
	e := NewEngine()
	x := e.Input("x", rat("0"), rat("2"))  // 半径 1
	y := e.Input("y", rat("0"), rat("10")) // 半径 5
	n := e.Add(x, y)
	srcs := e.TopSources(n)
	if len(srcs) != 2 || srcs[0].Name != "y" {
		t.Fatalf("最大来源应为 y, 得到 %+v", srcs)
	}
	total := new(big.Rat)
	for _, s := range srcs {
		total.Add(total, s.Impact)
	}
	lo, hi := n.Bound()
	r := new(big.Rat).Quo(new(big.Rat).Sub(hi, lo), rat("2"))
	if total.Cmp(r) != 0 {
		t.Fatalf("贡献之和 %s 应等于半径 %s", total.RatString(), r.RatString())
	}
}
