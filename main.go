package main

import (
	"fmt"
	"math/big"
)

var grid = []*big.Rat{rat("-1"), rat("-1/2"), rat("0"), rat("1/2"), rat("1")}

func showBound(label string, n *Node) {
	lo, hi := n.Bound()
	fmt.Printf("%-28s 保守界: [%s, %s]  (宽度 %s)\n", label, lo.RatString(), hi.RatString(),
		new(big.Rat).Sub(hi, lo).RatString())
}

func verify(label string, n *Node) {
	mn, mx, ok, err := VerifyContainment(n, grid)
	if err != nil {
		fmt.Printf("%-28s 枚举验证失败: %v\n", label, err)
		return
	}
	lo, hi := n.Bound()
	fmt.Printf("%-28s 枚举值域: [%s, %s] ⊆ 保守界 [%s, %s] ? %v\n",
		label, mn.RatString(), mx.RatString(), lo.RatString(), hi.RatString(), ok)
}

func main() {
	fmt.Println("=== 1. 同源相关性: x - x vs 两个独立变量相减 ===")
	{
		e := NewEngine()
		x := e.Input("x", rat("9/10"), rat("11/10"))
		showBound("x - x (共享节点)", e.Sub(x, x))
	}
	{
		e := NewEngine()
		x := e.Input("x", rat("9/10"), rat("11/10"))
		y := e.Input("y", rat("9/10"), rat("11/10")) // 与 x 同区间但独立
		showBound("x - y (独立输入)", e.Sub(x, y))
	}

	fmt.Println()
	fmt.Println("=== 2. 共享中间节点: (x+1) - x 与 z=x*x; z-z ===")
	{
		e := NewEngine()
		x := e.Input("x", rat("1"), rat("2"))
		showBound("(x+1) - x", e.Sub(e.Add(x, e.Const(rat("1"))), x))
		z := e.Mul(x, x)
		showBound("z - z (z=x*x 共享)", e.Sub(z, z))
	}

	fmt.Println()
	fmt.Println("=== 3. 复合表达式 f = (x*y + x) / (x + 2), x∈[1,2], y∈[3,4] ===")
	{
		e := NewEngine()
		x := e.Input("x", rat("1"), rat("2"))
		y := e.Input("y", rat("3"), rat("4"))
		num := e.Add(e.Mul(x, y), x)
		den := e.Add(x, e.Const(rat("2")))
		f, err := e.Div(num, den)
		if err != nil {
			fmt.Println("拒绝:", err)
			return
		}
		showBound("f = (x*y+x)/(x+2)", f)
		verify("f = (x*y+x)/(x+2)", f)
		fmt.Println("误差来源排名 (贡献降序):")
		for i, s := range e.TopSources(f) {
			fmt.Printf("  %d. %-40s 影响 %s\n", i+1, s.Name, s.Impact.RatString())
		}
	}

	fmt.Println()
	fmt.Println("=== 4. 除数过零: 拒绝 ===")
	{
		e := NewEngine()
		x := e.Input("x", rat("1"), rat("2"))
		y := e.Input("y", rat("-1"), rat("2")) // 区间跨 0
		if _, err := e.Div(x, y); err != nil {
			fmt.Println("Div(x, y), y∈[-1,2]:", err)
		}
	}

	fmt.Println()
	fmt.Println("=== 5. 枚举验证: 多种表达式 ===")
	{
		e := NewEngine()
		x := e.Input("x", rat("1"), rat("2"))
		y := e.Input("y", rat("-1"), rat("1"))
		z := e.Input("z", rat("0"), rat("1"))
		verify("x*y + z", e.Add(e.Mul(x, y), z))
		verify("(x+y)*(x+y) 共享", func() *Node {
			s := e.Add(x, y)
			return e.Mul(s, s)
		}())
		verify("x*x - x*x 共享节点", func() *Node {
			m := e.Mul(x, x)
			return e.Sub(m, m)
		}())
		d, err := e.Div(e.Const(rat("1")), e.Add(x, e.Const(rat("1"))))
		if err != nil {
			fmt.Println("拒绝:", err)
		} else {
			verify("1/(x+1)", d)
		}
	}
}
