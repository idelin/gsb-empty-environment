package taskpkg

func boolPtr(b bool) *bool { return &b }

func leafCheck(id int, field, mark string, trusted bool, emit Emit) *Check {
	return &Check{ID: id, Field: field, Emit: emit, MarkName: mark, Trusted: trusted}
}

func pureCheck(id int, field string) *Check {
	return &Check{ID: id, Field: field, Emit: EmitNever}
}

func checkExpr(id int, c *Check) *Expr {
	return &Expr{ID: id, Kind: ExprCheck, Check: c}
}

func notExpr(id int, child *Expr) *Expr {
	return &Expr{ID: id, Kind: ExprNot, Child: child}
}

func andExpr(id int, l, r *Expr) *Expr {
	return &Expr{ID: id, Kind: ExprAnd, Left: l, Right: r}
}

func orExpr(id int, l, r *Expr) *Expr {
	return &Expr{ID: id, Kind: ExprOr, Left: l, Right: r}
}

func checkStmt(id int, c *Check) *Stmt {
	return &Stmt{ID: id, Kind: StmtCheck, Check: c}
}

func rejectStmt(id int, reason string) *Stmt {
	return &Stmt{ID: id, Kind: StmtReject, Reject: reason}
}

func forwardStmt(id int, name string, required ...Mark) *Stmt {
	return &Stmt{ID: id, Kind: StmtForward, Forward: &Forward{Name: name, Required: required}}
}

func seqStmt(id int, a, b *Stmt) *Stmt {
	return &Stmt{ID: id, Kind: StmtSeq, First: a, Second: b}
}

func branchStmt(id int, cond *Expr, then, els *Stmt) *Stmt {
	return &Stmt{ID: id, Kind: StmtBranch, Cond: cond, Then: then, Else: els}
}

func tryStmt(id int, body, handler *Stmt) *Stmt {
	return &Stmt{ID: id, Kind: StmtTry, Then: body, Handler: handler}
}

func mustCompile(t testingT, r *Rule) *Program {
	t.Helper()
	p, err := Compile(r)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func hasEdge(edges []EdgeEvent, kind EdgeKind) bool {
	for _, e := range edges {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func countLeaves(leaves []LeafEvent) int { return len(leaves) }
