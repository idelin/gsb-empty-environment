package main

import "fmt"

// ---------- 具体解释器（用于对照验证） ----------

// Outcome 是一次具体执行的 observable 行为。
type Outcome struct {
	Env    map[string]int // 终止时的变量环境（出错时为出错点环境）
	Err    string         // 首个错误的函数名，无错为 ""
	ECount int            // 目标表达式 e 的求值次数
}

type interp struct {
	ft     FuncTable
	target Expr
	env    map[string]int
	ecount int
	err    string
}

func (in *interp) eval(e Expr) int {
	if in.err != "" {
		return 0
	}
	if eqExpr(e, in.target) {
		in.ecount++
	}
	switch x := e.(type) {
	case Num:
		return x.V
	case Var:
		return in.env[x.Name]
	case Add:
		l := in.eval(x.L)
		r := in.eval(x.R)
		return l + r
	case And:
		if in.eval(x.L) == 0 {
			return 0
		}
		return in.eval(x.R)
	case Or:
		if v := in.eval(x.L); v != 0 {
			return v
		}
		return in.eval(x.R)
	case Call:
		for _, a := range x.Args {
			in.eval(a)
		}
		if in.err != "" {
			return 0
		}
		spec := in.ft[x.Fn]
		res := 0
		for _, r := range spec.Reads {
			res += in.env[r]
		}
		if spec.Throws {
			in.err = x.Fn
			return 0
		}
		for _, w := range spec.Writes {
			in.env[w]++
		}
		return res
	}
	return 0
}

func (in *interp) exec(s Stmt) {
	if in.err != "" {
		return
	}
	switch st := s.(type) {
	case Assign:
		v := in.eval(st.RHS)
		if in.err == "" {
			in.env[st.Name] = v
		}
	case Let:
		v := in.eval(st.RHS)
		if in.err == "" {
			in.env[st.Name] = v
		}
	case ExprStmt:
		in.eval(st.E)
	case If:
		c := in.eval(st.Cond)
		if in.err != "" {
			return
		}
		if c != 0 {
			for _, s2 := range st.Then {
				in.exec(s2)
			}
		} else {
			for _, s2 := range st.Else {
				in.exec(s2)
			}
		}
	}
}

// Run 以给定初始环境具体执行程序，统计 target 的求值次数。
func Run(prog []Stmt, ft FuncTable, env map[string]int, target Expr) Outcome {
	cp := make(map[string]int, len(env))
	for k, v := range env {
		cp[k] = v
	}
	in := &interp{ft: ft, target: target, env: cp}
	for _, s := range prog {
		in.exec(s)
	}
	return Outcome{Env: cp, Err: in.err, ECount: in.ecount}
}

// equivalent 枚举初始环境（原程序变量取 0/1），对照两个程序的 observable 行为。
func equivalent(p1, p2 []Stmt, ft FuncTable, target Expr) (bool, string) {
	used := map[string]bool{}
	collectVarsProg(p1, used)
	for _, spec := range ft {
		for _, v := range spec.Reads {
			used[v] = true
		}
		for _, v := range spec.Writes {
			used[v] = true
		}
	}
	vars := make([]string, 0, len(used))
	for v := range used {
		vars = append(vars, v)
	}
	// 排序保证确定性
	for i := 0; i < len(vars); i++ {
		for j := i + 1; j < len(vars); j++ {
			if vars[j] < vars[i] {
				vars[i], vars[j] = vars[j], vars[i]
			}
		}
	}
	n := len(vars)
	for mask := 0; mask < 1<<uint(n); mask++ {
		env := map[string]int{}
		for i, v := range vars {
			env[v] = (mask >> uint(i)) & 1
		}
		o1 := Run(p1, ft, env, target)
		o2 := Run(p2, ft, env, target)
		if o1.Err != o2.Err || o1.ECount != o2.ECount || !envEqual(o1.Env, o2.Env, vars) {
			return false, fmt.Sprintf(
				"初始环境 %v：原程序(首错=%q, e求值=%d次, env=%v)，抽取后(首错=%q, e求值=%d次, env=%v)",
				env, o1.Err, o1.ECount, o1.Env, o2.Err, o2.ECount, o2.Env)
		}
	}
	return true, ""
}

// envEqual 只比较原程序涉及的变量（忽略抽取引入的临时变量）。
func envEqual(a, b map[string]int, vars []string) bool {
	for _, v := range vars {
		if a[v] != b[v] {
			return false
		}
	}
	return true
}
