package main

import "fmt"

// ---------- 表达式 ----------

type Expr interface{ isExpr() }

type Num struct{ V int }
type Var struct{ Name string }
type Call struct {
	Fn   string
	Args []Expr
}
type And struct{ L, R Expr } // 短路：L 为假则跳过 R
type Or struct{ L, R Expr }  // 短路：L 为真则跳过 R
type Add struct{ L, R Expr } // 严格：左右都求值

func (Num) isExpr()  {}
func (Var) isExpr()  {}
func (Call) isExpr() {}
func (And) isExpr()  {}
func (Or) isExpr()   {}
func (Add) isExpr()  {}

// ---------- 语句 ----------

type Stmt interface{ isStmt() }

type Assign struct { // 可变变量赋值
	Name string
	RHS  Expr
}
type ExprStmt struct{ E Expr } // 为副作用求值
type Let struct {              // 抽取引入的绑定
	Name string
	RHS  Expr
}
type If struct {
	Cond       Expr
	Then, Else []Stmt
}

func (Assign) isStmt()   {}
func (ExprStmt) isStmt() {}
func (Let) isStmt()      {}
func (If) isStmt()       {}

// ---------- 函数规约（由输入给定） ----------

type FuncSpec struct {
	Reads  []string // 调用读取的变量
	Writes []string // 调用写入的变量（各 +1）
	Throws bool     // 调用是否抛错（确定性）
}

type FuncTable map[string]FuncSpec

// ---------- 结构相等 ----------

func eqExpr(a, b Expr) bool {
	switch x := a.(type) {
	case Num:
		y, ok := b.(Num)
		return ok && x.V == y.V
	case Var:
		y, ok := b.(Var)
		return ok && x.Name == y.Name
	case Call:
		y, ok := b.(Call)
		if !ok || x.Fn != y.Fn || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !eqExpr(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case And:
		y, ok := b.(And)
		return ok && eqExpr(x.L, y.L) && eqExpr(x.R, y.R)
	case Or:
		y, ok := b.(Or)
		return ok && eqExpr(x.L, y.L) && eqExpr(x.R, y.R)
	case Add:
		y, ok := b.(Add)
		return ok && eqExpr(x.L, y.L) && eqExpr(x.R, y.R)
	}
	return false
}

// ---------- 表达式的读/写/抛错集合 ----------

func readsOf(e Expr, ft FuncTable) map[string]bool {
	out := map[string]bool{}
	var walk func(Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case Var:
			out[n.Name] = true
		case Call:
			for _, a := range n.Args {
				walk(a)
			}
			for _, r := range ft[n.Fn].Reads {
				out[r] = true
			}
		case And:
			walk(n.L)
			walk(n.R)
		case Or:
			walk(n.L)
			walk(n.R)
		case Add:
			walk(n.L)
			walk(n.R)
		}
	}
	walk(e)
	return out
}

func writesOf(e Expr, ft FuncTable) map[string]bool {
	out := map[string]bool{}
	var walk func(Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case Call:
			for _, a := range n.Args {
				walk(a)
			}
			for _, w := range ft[n.Fn].Writes {
				out[w] = true
			}
		case And:
			walk(n.L)
			walk(n.R)
		case Or:
			walk(n.L)
			walk(n.R)
		case Add:
			walk(n.L)
			walk(n.R)
		}
	}
	walk(e)
	return out
}

// ---------- 出现次数与替换 ----------

func countOccurExpr(e, target Expr) int {
	if eqExpr(e, target) {
		return 1
	}
	n := 0
	switch x := e.(type) {
	case Call:
		for _, a := range x.Args {
			n += countOccurExpr(a, target)
		}
	case And:
		n += countOccurExpr(x.L, target) + countOccurExpr(x.R, target)
	case Or:
		n += countOccurExpr(x.L, target) + countOccurExpr(x.R, target)
	case Add:
		n += countOccurExpr(x.L, target) + countOccurExpr(x.R, target)
	}
	return n
}

func countOccurStmt(s Stmt, target Expr) int {
	n := 0
	switch st := s.(type) {
	case Assign:
		n += countOccurExpr(st.RHS, target)
	case ExprStmt:
		n += countOccurExpr(st.E, target)
	case Let:
		n += countOccurExpr(st.RHS, target)
	case If:
		n += countOccurExpr(st.Cond, target)
		for _, s2 := range st.Then {
			n += countOccurStmt(s2, target)
		}
		for _, s2 := range st.Else {
			n += countOccurStmt(s2, target)
		}
	}
	return n
}

func countOccurProg(prog []Stmt, target Expr) int {
	n := 0
	for _, s := range prog {
		n += countOccurStmt(s, target)
	}
	return n
}

func replaceExpr(e, target, repl Expr) Expr {
	if eqExpr(e, target) {
		return repl
	}
	switch x := e.(type) {
	case Call:
		args := make([]Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = replaceExpr(a, target, repl)
		}
		return Call{Fn: x.Fn, Args: args}
	case And:
		return And{replaceExpr(x.L, target, repl), replaceExpr(x.R, target, repl)}
	case Or:
		return Or{replaceExpr(x.L, target, repl), replaceExpr(x.R, target, repl)}
	case Add:
		return Add{replaceExpr(x.L, target, repl), replaceExpr(x.R, target, repl)}
	}
	return e
}

func replaceStmt(s Stmt, target, repl Expr) Stmt {
	switch st := s.(type) {
	case Assign:
		return Assign{st.Name, replaceExpr(st.RHS, target, repl)}
	case ExprStmt:
		return ExprStmt{replaceExpr(st.E, target, repl)}
	case Let:
		return Let{st.Name, replaceExpr(st.RHS, target, repl)}
	case If:
		th := make([]Stmt, len(st.Then))
		for i, s2 := range st.Then {
			th[i] = replaceStmt(s2, target, repl)
		}
		el := make([]Stmt, len(st.Else))
		for i, s2 := range st.Else {
			el[i] = replaceStmt(s2, target, repl)
		}
		return If{replaceExpr(st.Cond, target, repl), th, el}
	}
	return s
}

// firstOccurStmt 返回首个包含 target 的顶层语句下标，没有则返回 -1。
func firstOccurStmt(prog []Stmt, target Expr) int {
	for i, s := range prog {
		if countOccurStmt(s, target) > 0 {
			return i
		}
	}
	return -1
}

// ---------- 变量收集与新鲜名 ----------

func collectVarsExpr(e Expr, out map[string]bool) {
	switch x := e.(type) {
	case Var:
		out[x.Name] = true
	case Call:
		for _, a := range x.Args {
			collectVarsExpr(a, out)
		}
	case And:
		collectVarsExpr(x.L, out)
		collectVarsExpr(x.R, out)
	case Or:
		collectVarsExpr(x.L, out)
		collectVarsExpr(x.R, out)
	case Add:
		collectVarsExpr(x.L, out)
		collectVarsExpr(x.R, out)
	}
}

func collectVarsStmt(s Stmt, out map[string]bool) {
	switch st := s.(type) {
	case Assign:
		out[st.Name] = true
		collectVarsExpr(st.RHS, out)
	case ExprStmt:
		collectVarsExpr(st.E, out)
	case Let:
		out[st.Name] = true
		collectVarsExpr(st.RHS, out)
	case If:
		collectVarsExpr(st.Cond, out)
		for _, s2 := range st.Then {
			collectVarsStmt(s2, out)
		}
		for _, s2 := range st.Else {
			collectVarsStmt(s2, out)
		}
	}
}

func collectVarsProg(prog []Stmt, out map[string]bool) {
	for _, s := range prog {
		collectVarsStmt(s, out)
	}
}

// freshName 选一个程序中未使用的绑定名。
func freshName(prog []Stmt, e Expr, ft FuncTable) string {
	used := map[string]bool{}
	collectVarsProg(prog, used)
	collectVarsExpr(e, used)
	for _, spec := range ft {
		for _, v := range spec.Reads {
			used[v] = true
		}
		for _, v := range spec.Writes {
			used[v] = true
		}
	}
	name := "tmp"
	for i := 1; used[name]; i++ {
		name = fmt.Sprintf("tmp%d", i)
	}
	return name
}

// ---------- 打印 ----------

func ExprString(e Expr) string {
	switch x := e.(type) {
	case Num:
		return fmt.Sprintf("%d", x.V)
	case Var:
		return x.Name
	case Call:
		s := x.Fn + "("
		for i, a := range x.Args {
			if i > 0 {
				s += ", "
			}
			s += ExprString(a)
		}
		return s + ")"
	case And:
		return "(" + ExprString(x.L) + " && " + ExprString(x.R) + ")"
	case Or:
		return "(" + ExprString(x.L) + " || " + ExprString(x.R) + ")"
	case Add:
		return "(" + ExprString(x.L) + " + " + ExprString(x.R) + ")"
	}
	return "?"
}

func StmtString(s Stmt) string {
	switch st := s.(type) {
	case Assign:
		return st.Name + " = " + ExprString(st.RHS)
	case ExprStmt:
		return ExprString(st.E)
	case Let:
		return "let " + st.Name + " = " + ExprString(st.RHS)
	case If:
		return "if " + ExprString(st.Cond) + " { ... } else { ... }"
	}
	return "?"
}

func ProgString(prog []Stmt) string {
	s := ""
	for i, st := range prog {
		if i > 0 {
			s += "; "
		}
		s += StmtString(st)
	}
	return s
}
