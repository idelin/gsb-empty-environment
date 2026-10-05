package main

const TmpName = "__t"

func copyExpr(e Expr) Expr {
	switch n := e.(type) {
	case *Const:
		return &Const{n.V}
	case *Var:
		return &Var{n.Name}
	case *Add:
		return &Add{copyExpr(n.L), copyExpr(n.R)}
	case *And:
		return &And{copyExpr(n.L), copyExpr(n.R)}
	case *Or:
		return &Or{copyExpr(n.L), copyExpr(n.R)}
	case *Not:
		return &Not{copyExpr(n.E)}
	case *Call:
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = copyExpr(a)
		}
		return &Call{n.F, args}
	}
	panic("copyExpr")
}

func copyStmt(s Stmt) Stmt {
	switch n := s.(type) {
	case *Assign:
		return &Assign{n.X, copyExpr(n.E)}
	case *Print:
		return &Print{copyExpr(n.E)}
	case *If:
		return &If{copyExpr(n.C), copyProg(n.Then), copyProg(n.Else)}
	}
	panic("copyStmt")
}

func copyProg(p []Stmt) []Stmt {
	out := make([]Stmt, len(p))
	for i, s := range p {
		out[i] = copyStmt(s)
	}
	return out
}

func setExprAt(prog []Stmt, path []int, ne Expr) {
	parent := nodeAt(prog, path[:len(path)-1])
	i := path[len(path)-1]
	switch n := parent.(type) {
	case *Assign:
		n.E = ne
	case *Print:
		n.E = ne
	case *If:
		n.C = ne
	case *Add:
		if i == 0 {
			n.L = ne
		} else {
			n.R = ne
		}
	case *And:
		if i == 0 {
			n.L = ne
		} else {
			n.R = ne
		}
	case *Or:
		if i == 0 {
			n.L = ne
		} else {
			n.R = ne
		}
	case *Not:
		n.E = ne
	case *Call:
		n.Args[i] = ne
	default:
		panic("setExprAt: 非法父节点")
	}
}

func insertStmt(list []Stmt, idx int, s Stmt) []Stmt {
	out := make([]Stmt, 0, len(list)+1)
	out = append(out, list[:idx]...)
	out = append(out, s)
	out = append(out, list[idx:]...)
	return out
}

// Refactor：在 O1 所属语句前插入 __t = e，把 O1、O2 替换为 __t。
func Refactor(prog []Stmt, e Expr, o1, o2 []int) []Stmt {
	p := copyProg(prog)
	setExprAt(p, o2, &Var{TmpName})
	setExprAt(p, o1, &Var{TmpName})
	sp := enclosingStmt(p, o1)
	idx := sp[len(sp)-1]
	bind := &Assign{X: TmpName, E: copyExpr(e)}
	if len(sp) == 1 {
		return insertStmt(p, idx, bind)
	}
	owner := nodeAt(p, sp[:len(sp)-2]).(*If)
	if sp[len(sp)-2] == 1 {
		owner.Then = insertStmt(owner.Then, idx, bind)
	} else {
		owner.Else = insertStmt(owner.Else, idx, bind)
	}
	return p
}
