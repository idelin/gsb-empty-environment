package main

import "fmt"

func sec(s string) { fmt.Printf("\n=== %s ===\n", s) }

func analyze(e *Engine, root *Node, memo map[*Node]*Affine, inputs []*Node, label string) {
	af, err := root.Eval(e, memo)
	if err != nil {
		fmt.Printf("%s: 拒绝 — %v\n", label, err)
		return
	}
	bound := af.Range()
	fmt.Printf("%s\n", label)
	fmt.Println("  仿射形:", e.Show(af))
	fmt.Println("  保守误差界:", bound)
	top := e.TopSource(af)
	fmt.Printf("  最大误差来源: %s（贡献 %s）\n", top.Name, top.Abs.RatString())
	for _, c := range e.Contributions(af) {
		fmt.Printf("    - %-12s 贡献 %s\n", c.Name, c.Abs.RatString())
	}
	fmt.Println("  " + Verify(root, inputs, bound).String())
}

func main() {
	e := NewEngine()
	memo := map[*Node]*Affine{}

	// 输入：标称值 + 有理误差区间
	x := InputNode("x", "2", "-1/8", "1/8")   // x = 2 ± 1/8
	y := InputNode("y", "3", "-1/4", "1/4")   // y = 3 ± 1/4
	z := InputNode("z", "1", "-1/16", "1/16") // z = 1 ± 1/16
	x2 := InputNode("x2", "2", "-1/8", "1/8") // 与 x 同分布但独立的输入

	sec("1. 同源相关性: x - x vs x - x2（两个独立变量相减）")
	dSame := Sub(x, x)
	aSame, _ := dSame.Eval(e, memo)
	fmt.Println("x - x  仿射形:", e.Show(aSame), " 界:", aSame.Range(), "（同源精确抵消）")
	fmt.Println("  " + Verify(dSame, []*Node{x}, aSame.Range()).String())
	dIndep := Sub(x, x2)
	aIndep, _ := dIndep.Eval(e, memo)
	fmt.Println("x - x2 仿射形:", e.Show(aIndep), " 界:", aIndep.Range(), "（独立误差叠加，宽度翻倍）")
	fmt.Println("  " + Verify(dIndep, []*Node{x, x2}, aIndep.Range()).String())

	sec("2. 除法的相关性: x / x vs x / x2")
	qSame := Div(x, x)
	aqSame, err := qSame.Eval(e, memo)
	if err != nil {
		fmt.Println("x / x 拒绝:", err)
	} else {
		fmt.Println("x / x  界:", aqSame.Range(), "（一阶项抵消，残差仅 O(r²)）")
		fmt.Println("  " + Verify(qSame, []*Node{x}, aqSame.Range()).String())
	}
	qIndep := Div(x, x2)
	aqIndep, _ := qIndep.Eval(e, memo)
	fmt.Println("x / x2 界:", aqIndep.Range(), "（独立误差，明显更宽）")
	fmt.Println("  " + Verify(qIndep, []*Node{x, x2}, aqIndep.Range()).String())

	sec("3. 共享节点主表达式: s = x*y + z; f = (s*s) / (s+2)")
	s := Add(Mul(x, y), z) // 共享子表达式，被使用两次
	f := Div(Mul(s, s), Add(s, ConstNode("2")))
	analyze(e, f, memo, []*Node{x, y, z}, "f = (s·s)/(s+2), s = x·y+z")

	sec("4. 除数过零: g = x / (y - 3)")
	g := Div(x, Sub(y, ConstNode("3")))
	analyze(e, g, memo, []*Node{x, y}, "g = x/(y-3)")
}
