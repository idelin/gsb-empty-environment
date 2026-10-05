package main

import (
	"math/rand"
)

// ---------- 小程序枚举 ----------
// 随机但确定性地生成（程序, 函数表, 表达式 e, 两次出现位置 O1/O2），
// 覆盖：短路分支、条件分支、两次求值之间的写/调用写/抛错、纯调用等。

var genVars = []string{"x", "y", "z"}

func genFuncTable(r *rand.Rand) (FuncTable, []string) {
	ft := FuncTable{}
	names := []string{"f", "g", "h"}
	var fnames []string
	for _, n := range names[:1+r.Intn(3)] {
		sig := FuncSig{}
		for _, v := range genVars {
			if r.Intn(3) == 0 {
				sig.Reads = append(sig.Reads, v)
			}
			if r.Intn(3) == 0 {
				sig.Writes = append(sig.Writes, v)
			}
		}
		sig.Throws = r.Intn(8) == 0
		ft[n] = sig
		fnames = append(fnames, n)
	}
	return ft, fnames
}

func genExpr(r *rand.Rand, depth int, fnames []string) Expr {
	if depth <= 0 {
		if r.Intn(2) == 0 {
			return &Const{r.Intn(3)}
		}
		return &Var{genVars[r.Intn(len(genVars))]}
	}
	switch r.Intn(7) {
	case 0:
		return &Const{r.Intn(3)}
	case 1:
		return &Var{genVars[r.Intn(len(genVars))]}
	case 2:
		return &Add{genExpr(r, depth-1, fnames), genExpr(r, depth-1, fnames)}
	case 3:
		return &And{genExpr(r, depth-1, fnames), genExpr(r, depth-1, fnames)}
	case 4:
		return &Or{genExpr(r, depth-1, fnames), genExpr(r, depth-1, fnames)}
	case 5:
		return &Not{genExpr(r, depth-1, fnames)}
	default:
		if len(fnames) == 0 {
			return &Var{genVars[r.Intn(len(genVars))]}
		}
		n := r.Intn(3)
		args := make([]Expr, n)
		for i := range args {
			args[i] = genExpr(r, depth-1, fnames)
		}
		return &Call{fnames[r.Intn(len(fnames))], args}
	}
}

func genStmts(r *rand.Rand, n int, fnames []string, depth int) []Stmt {
	out := make([]Stmt, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			out = append(out, &Assign{genVars[r.Intn(len(genVars))], genExpr(r, 2, fnames)})
		case 4, 5, 6, 7:
			out = append(out, &Print{genExpr(r, 2, fnames)})
		default:
			if depth > 0 {
				out = append(out, &Print{genExpr(r, 2, fnames)})
				continue
			}
			then := genStmts(r, 1+r.Intn(2), fnames, depth+1)
			var els []Stmt
			if r.Intn(2) == 0 {
				els = genStmts(r, 1+r.Intn(2), fnames, depth+1)
			}
			out = append(out, &If{genExpr(r, 1, fnames), then, els})
		}
	}
	return out
}

// exprSlots：程序中所有表达式位置（赋值右值、print 参数、if 条件）
func exprSlots(prog []Stmt) [][]int {
	var slots [][]int
	var walk func(n interface{}, p []int)
	walk = func(n interface{}, p []int) {
		switch nn := n.(type) {
		case []Stmt:
			for i, s := range nn {
				walk(s, ext(p, i))
			}
		case *Assign:
			slots = append(slots, ext(p, 0))
		case *Print:
			slots = append(slots, ext(p, 0))
		case *If:
			slots = append(slots, ext(p, 0))
			walk(nn.Then, ext(p, 1))
			walk(nn.Else, ext(p, 2))
		}
	}
	walk(prog, nil)
	return slots
}

// evalOrderMap：每个表达式节点的求值顺序编号（与解释器求值序一致）
func evalOrderMap(prog []Stmt) map[string]int {
	order := map[string]int{}
	cnt := 0
	var walkE func(e Expr, p []int)
	var walkS func(ss []Stmt, p []int)
	walkE = func(e Expr, p []int) {
		order[pathKey(p)] = cnt
		cnt++
		switch n := e.(type) {
		case *Add:
			walkE(n.L, ext(p, 0))
			walkE(n.R, ext(p, 1))
		case *And:
			walkE(n.L, ext(p, 0))
			walkE(n.R, ext(p, 1))
		case *Or:
			walkE(n.L, ext(p, 0))
			walkE(n.R, ext(p, 1))
		case *Not:
			walkE(n.E, ext(p, 0))
		case *Call:
			for i, a := range n.Args {
				walkE(a, ext(p, i))
			}
		}
	}
	walkS = func(ss []Stmt, p []int) {
		for i, s := range ss {
			sp := ext(p, i)
			switch n := s.(type) {
			case *Assign:
				walkE(n.E, ext(sp, 0))
			case *Print:
				walkE(n.E, ext(sp, 0))
			case *If:
				walkE(n.C, ext(sp, 0))
				walkS(n.Then, ext(sp, 1))
				walkS(n.Else, ext(sp, 2))
			}
		}
	}
	walkS(prog, nil)
	return order
}

func genCase(r *rand.Rand) ([]Stmt, FuncTable, Expr, []int, []int) {
	ft, fnames := genFuncTable(r)
	prog := genStmts(r, 2+r.Intn(3), fnames, 0)
	// 待抽取表达式：一半概率不含调用（更容易被接受），一半任意
	var e Expr
	if r.Intn(2) == 0 {
		e = genExpr(r, 1+r.Intn(2), nil)
	} else {
		e = genExpr(r, 1+r.Intn(2), fnames)
	}
	slots := exprSlots(prog)
	if len(slots) < 2 {
		return genCase(r)
	}
	for tries := 0; tries < 60; tries++ {
		a := slots[r.Intn(len(slots))]
		b := slots[r.Intn(len(slots))]
		if eqPath(a, b) || isPrefix(a, b) || isPrefix(b, a) {
			continue
		}
		setExprAt(prog, a, e)
		setExprAt(prog, b, e)
		om := evalOrderMap(prog)
		if om[pathKey(a)] < om[pathKey(b)] {
			return prog, ft, e, a, b
		}
		return prog, ft, e, b, a
	}
	return genCase(r)
}
