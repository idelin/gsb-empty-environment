package main

import (
	"fmt"
	"math/big"
)

// Node 是计算图节点。共享节点通过复用同一个 *Node 指针表达,
// 其误差符号相同, 相关性因此被保留。
type Node struct {
	name string
	op   string // "input" | "const" | "add" | "sub" | "mul" | "div"
	args []*Node
	form *Form
	sym  int // 仅 input: 该输入自己的噪声符号 id
}

// Engine 构建计算图并分配噪声符号。
type Engine struct {
	nextSym int
	symSrc  map[int]string // 符号 id -> 来源名
}

func NewEngine() *Engine {
	return &Engine{nextSym: 0, symSrc: map[int]string{}}
}

func (e *Engine) fresh(src string) int {
	id := e.nextSym
	e.nextSym++
	e.symSrc[id] = src
	return id
}

// Input 创建一个带有理误差区间 [lo, hi] 的输入节点。
func (e *Engine) Input(name string, lo, hi *big.Rat) *Node {
	if lo.Cmp(hi) > 0 {
		panic("lo > hi for input " + name)
	}
	two := rat("2")
	center := new(big.Rat).Quo(new(big.Rat).Add(lo, hi), two)
	coeff := new(big.Rat).Quo(new(big.Rat).Sub(hi, lo), two)
	sym := e.fresh(name)
	f := &Form{center: center, coeffs: map[int]*big.Rat{}}
	if coeff.Sign() != 0 {
		f.coeffs[sym] = coeff
	}
	return &Node{name: name, op: "input", form: f, sym: sym}
}

// Const 创建常数节点。
func (e *Engine) Const(c *big.Rat) *Node {
	return &Node{name: c.RatString(), op: "const", form: constForm(c)}
}

func (e *Engine) Add(a, b *Node) *Node {
	return &Node{name: "(" + a.name + "+" + b.name + ")", op: "add", args: []*Node{a, b}, form: a.form.add(b.form)}
}

func (e *Engine) Sub(a, b *Node) *Node {
	return &Node{name: "(" + a.name + "-" + b.name + ")", op: "sub", args: []*Node{a, b}, form: a.form.sub(b.form)}
}

func (e *Engine) Mul(a, b *Node) *Node {
	sym := e.fresh("mul-roundoff@" + a.name + "*" + b.name)
	return &Node{name: "(" + a.name + "*" + b.name + ")", op: "mul", args: []*Node{a, b}, form: a.form.mul(b.form, sym)}
}

// Div 计算 a/b。b 的保守区间包含 0 时返回错误 (拒绝), 否则用倒数仿射逼近。
func (e *Engine) Div(a, b *Node) (*Node, error) {
	sym := e.fresh("recip-remainder@" + b.name)
	r, err := b.form.recip(sym)
	if err != nil {
		return nil, fmt.Errorf("Div(%s, %s): %v", a.name, b.name, err)
	}
	mulSym := e.fresh("mul-roundoff@" + a.name + "/" + b.name)
	form := a.form.mul(r, mulSym)
	return &Node{name: "(" + a.name + "/" + b.name + ")", op: "div", args: []*Node{a, b}, form: form}, nil
}

// Bound 返回输出的保守误差区间。
func (n *Node) Bound() (lo, hi *big.Rat) { return n.form.interval() }

// TopSources 返回对输出误差影响最大的来源, 按贡献降序。
func (e *Engine) TopSources(n *Node) []Source {
	return n.form.sensitivity(func(id int) string { return e.symSrc[id] })
}

// ---------- 有限小域精确枚举验证 ----------

// collectInputs 收集图中所有输入节点 (去重, 共享节点只出现一次)。
func collectInputs(root *Node) []*Node {
	seen := map[*Node]bool{}
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if seen[n] {
			return
		}
		seen[n] = true
		if n.op == "input" {
			out = append(out, n)
		}
		for _, a := range n.args {
			walk(a)
		}
	}
	walk(root)
	return out
}

// evalExact 在给定输入取值 (精确有理数) 下对 DAG 求值。
// 共享节点只算一次且处处同值 —— 这就是"同源相关性"在真实语义下的体现。
// 除数恰为 0 时返回错误。
func evalExact(n *Node, vals map[*Node]*big.Rat, memo map[*Node]*big.Rat) (*big.Rat, error) {
	if v, ok := memo[n]; ok {
		return v, nil
	}
	var r *big.Rat
	switch n.op {
	case "input":
		v, ok := vals[n]
		if !ok {
			return nil, fmt.Errorf("input %s 未赋值", n.name)
		}
		r = v
	case "const":
		r = new(big.Rat).Set(n.form.center)
	case "add", "sub", "mul", "div":
		a, err := evalExact(n.args[0], vals, memo)
		if err != nil {
			return nil, err
		}
		b, err := evalExact(n.args[1], vals, memo)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case "add":
			r = new(big.Rat).Add(a, b)
		case "sub":
			r = new(big.Rat).Sub(a, b)
		case "mul":
			r = new(big.Rat).Mul(a, b)
		case "div":
			if b.Sign() == 0 {
				return nil, fmt.Errorf("枚举中除数 %s 取到 0", n.args[1].name)
			}
			r = new(big.Rat).Quo(a, b)
		}
	}
	memo[n] = r
	return r, nil
}

// VerifyContainment 在有限小域上精确枚举: 每个输入的噪声符号在 grid
// (例如 {-1, -1/2, 0, 1/2, 1}) 上取值, 输入值 = center + coeff*符号值。
// 计算真实值域 [min, max], 检查其被仿射形式的保守区间包含。
// 返回真实 min/max 与是否包含。
func VerifyContainment(root *Node, grid []*big.Rat) (mn, mx *big.Rat, ok bool, err error) {
	inputs := collectInputs(root)
	vals := map[*Node]*big.Rat{}
	idx := make([]int, len(inputs))
	mn, mx = nil, nil
	total := 1
	for range inputs {
		total *= len(grid)
	}
	for count := 0; count < total; count++ {
		// 混合进制计数器生成所有组合
		t := count
		for i := range inputs {
			idx[i] = t % len(grid)
			t /= len(grid)
		}
		for i, in := range inputs {
			s := grid[idx[i]]
			coeff := in.form.coeffs[in.sym]
			v := new(big.Rat).Set(in.form.center)
			if coeff != nil {
				v.Add(v, new(big.Rat).Mul(coeff, s))
			}
			vals[in] = v
		}
		v, verr := evalExact(root, vals, map[*Node]*big.Rat{})
		if verr != nil {
			return nil, nil, false, verr
		}
		if mn == nil || v.Cmp(mn) < 0 {
			mn = new(big.Rat).Set(v)
		}
		if mx == nil || v.Cmp(mx) > 0 {
			mx = new(big.Rat).Set(v)
		}
	}
	lo, hi := root.Bound()
	ok = mn != nil && lo.Cmp(mn) <= 0 && hi.Cmp(mx) >= 0
	return mn, mx, ok, nil
}
