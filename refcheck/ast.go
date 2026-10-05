package main

import (
	"fmt"
	"strings"
)

// ---------- 表达式 ----------
// 语言特性：可变变量、函数调用（读写集合与是否抛错由函数表给定）、
// 短路 && / ||、异常（调用抛错即程序以该错误终止）。
type Expr interface{ exprNode() }

type Const struct{ V int }
type Var struct{ Name string }
type Add struct{ L, R Expr } // 严格求值，左先右后
type And struct{ L, R Expr } // 短路：左为 0 则不求值右
type Or struct{ L, R Expr }  // 短路：左非 0 则不求值右
type Not struct{ E Expr }
type Call struct {
	F    string
	Args []Expr
}

func (*Const) exprNode() {}
func (*Var) exprNode()   {}
func (*Add) exprNode()   {}
func (*And) exprNode()   {}
func (*Or) exprNode()    {}
func (*Not) exprNode()   {}
func (*Call) exprNode()  {}

// ---------- 语句 ----------
type Stmt interface{ stmtNode() }

type Assign struct {
	X string
	E Expr
}
type Print struct{ E Expr }
type If struct {
	C    Expr
	Then []Stmt
	Else []Stmt
}

func (*Assign) stmtNode() {}
func (*Print) stmtNode()  {}
func (*If) stmtNode()     {}

// ---------- 函数表（输入给定） ----------
type FuncSig struct {
	Reads  []string // 调用读取的变量
	Writes []string // 调用写入的变量
	Throws bool     // 调用被求值时是否抛错
}
type FuncTable map[string]FuncSig

// ---------- 位置：从程序根到某节点的下标路径 ----------
// 根为 []Stmt；Assign/Print 子节点 0=表达式；If 0=cond 1=then 2=else；
// Add/And/Or 0=左 1=右；Not 0=子表达式；Call i=第 i 个参数。

func childAt(node interface{}, i int) interface{} {
	switch n := node.(type) {
	case []Stmt:
		return n[i]
	case *Assign:
		return n.E
	case *Print:
		return n.E
	case *If:
		switch i {
		case 0:
			return n.C
		case 1:
			return n.Then
		default:
			return n.Else
		}
	case *Add:
		if i == 0 {
			return n.L
		}
		return n.R
	case *And:
		if i == 0 {
			return n.L
		}
		return n.R
	case *Or:
		if i == 0 {
			return n.L
		}
		return n.R
	case *Not:
		return n.E
	case *Call:
		return n.Args[i]
	}
	panic("childAt: 非法节点")
}

func nodeAt(prog []Stmt, p []int) interface{} {
	var cur interface{} = prog
	for _, i := range p {
		cur = childAt(cur, i)
	}
	return cur
}

func eqPath(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isPrefix(a, b []int) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ext(p []int, i int) []int {
	np := make([]int, len(p)+1)
	copy(np, p)
	np[len(p)] = i
	return np
}

func pathKey(p []int) string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ".")
}

// ---------- 表达式属性（含函数调用的读写集合） ----------

func varsIn(e Expr) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(x Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case *Var:
			if !seen[n.Name] {
				seen[n.Name] = true
				out = append(out, n.Name)
			}
		case *Add:
			walk(n.L)
			walk(n.R)
		case *And:
			walk(n.L)
			walk(n.R)
		case *Or:
			walk(n.L)
			walk(n.R)
		case *Not:
			walk(n.E)
		case *Call:
			for _, a := range n.Args {
				walk(a)
			}
		}
	}
	walk(e)
	return out
}

func callsIn(e Expr) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(x Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case *Add:
			walk(n.L)
			walk(n.R)
		case *And:
			walk(n.L)
			walk(n.R)
		case *Or:
			walk(n.L)
			walk(n.R)
		case *Not:
			walk(n.E)
		case *Call:
			if !seen[n.F] {
				seen[n.F] = true
				out = append(out, n.F)
			}
			for _, a := range n.Args {
				walk(a)
			}
		}
	}
	walk(e)
	return out
}

// readsOf：表达式读取的变量 = 自由变量 ∪ 各调用的 Reads
func readsOf(e Expr, ft FuncTable) map[string]bool {
	out := map[string]bool{}
	for _, v := range varsIn(e) {
		out[v] = true
	}
	for _, f := range callsIn(e) {
		for _, r := range ft[f].Reads {
			out[r] = true
		}
	}
	return out
}

// throwCalls：表达式中可能抛错的调用名
func throwCalls(e Expr, ft FuncTable) []string {
	var out []string
	for _, f := range callsIn(e) {
		if ft[f].Throws {
			out = append(out, f)
		}
	}
	return out
}

// ---------- 打印 ----------

func exprStr(e Expr) string {
	switch n := e.(type) {
	case *Const:
		return fmt.Sprint(n.V)
	case *Var:
		return n.Name
	case *Add:
		return "(" + exprStr(n.L) + " + " + exprStr(n.R) + ")"
	case *And:
		return "(" + exprStr(n.L) + " && " + exprStr(n.R) + ")"
	case *Or:
		return "(" + exprStr(n.L) + " || " + exprStr(n.R) + ")"
	case *Not:
		return "!" + exprStr(n.E)
	case *Call:
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = exprStr(a)
		}
		return n.F + "(" + strings.Join(args, ", ") + ")"
	}
	return "?"
}

func stmtStr(s Stmt, ind string) string {
	switch n := s.(type) {
	case *Assign:
		return ind + n.X + " = " + exprStr(n.E)
	case *Print:
		return ind + "print " + exprStr(n.E)
	case *If:
		var b strings.Builder
		b.WriteString(ind + "if " + exprStr(n.C) + " {\n")
		for _, t := range n.Then {
			b.WriteString(stmtStr(t, ind+"  ") + "\n")
		}
		b.WriteString(ind + "}")
		if len(n.Else) > 0 {
			b.WriteString(" else {\n")
			for _, t := range n.Else {
				b.WriteString(stmtStr(t, ind+"  ") + "\n")
			}
			b.WriteString(ind + "}")
		}
		return b.String()
	}
	return ind + "?"
}

func progStr(prog []Stmt) string {
	lines := make([]string, len(prog))
	for i, s := range prog {
		lines[i] = stmtStr(s, "  ")
	}
	return "\n" + strings.Join(lines, "\n")
}

func locStr(prog []Stmt, p []int) string {
	var b strings.Builder
	b.WriteString("prog")
	var cur interface{} = prog
	for _, i := range p {
		switch n := cur.(type) {
		case []Stmt:
			fmt.Fprintf(&b, ".s%d", i)
			cur = n[i]
		case *Assign:
			b.WriteString(".rhs")
			cur = n.E
		case *Print:
			b.WriteString(".arg")
			cur = n.E
		case *If:
			switch i {
			case 0:
				b.WriteString(".cond")
				cur = n.C
			case 1:
				b.WriteString(".then")
				cur = n.Then
			default:
				b.WriteString(".else")
				cur = n.Else
			}
		case *Add:
			if i == 0 {
				b.WriteString(".l")
				cur = n.L
			} else {
				b.WriteString(".r")
				cur = n.R
			}
		case *And:
			if i == 0 {
				b.WriteString(".l")
				cur = n.L
			} else {
				b.WriteString(".r")
				cur = n.R
			}
		case *Or:
			if i == 0 {
				b.WriteString(".l")
				cur = n.L
			} else {
				b.WriteString(".r")
				cur = n.R
			}
		case *Not:
			b.WriteString(".e")
			cur = n.E
		case *Call:
			fmt.Fprintf(&b, ".a%d", i)
			cur = n.Args[i]
		}
	}
	return b.String()
}
