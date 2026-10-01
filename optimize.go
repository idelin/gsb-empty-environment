package logplan

// referencedTables collects the set of table qualifiers used by an
// expression. Unqualified columns contribute "".
func referencedTables(e Expr) map[string]bool {
	set := map[string]bool{}
	var visit func(Expr)
	visit = func(e Expr) {
		switch n := e.(type) {
		case *ColumnRef:
			set[n.Table] = true
		case *BinaryExpr:
			visit(n.Left)
			visit(n.Right)
		}
	}
	visit(e)
	return set
}

// splitAnd flattens an AND-connected predicate into conjuncts.
func splitAnd(e Expr) []Expr {
	if b, ok := e.(*BinaryExpr); ok && b.Op == "AND" {
		return append(splitAnd(b.Left), splitAnd(b.Right)...)
	}
	return []Expr{e}
}

// joinAnd rebuilds a balanced AND chain from conjuncts.
func joinAnd(parts []Expr) Expr {
	switch len(parts) {
	case 0:
		return nil
	case 1:
		return parts[0]
	}
	return &BinaryExpr{Op: "AND", Left: joinAnd(parts[:len(parts)-1]), Right: parts[len(parts)-1]}
}

// outputTables returns the set of table aliases produced by a plan subtree.
func outputTables(p Plan) map[string]bool {
	set := map[string]bool{}
	Walk(p, func(n Plan) bool {
		if s, ok := n.(*Scan); ok {
			set[s.Alias] = true
		}
		return true
	})
	return set
}

// subsetOf reports whether every key of a is contained in b.
func subsetOf(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// pushDownPredicates rewrites Filter nodes so each conjunct is placed as
// low as possible: single-table conjuncts are pushed toward the matching
// Scan, and conjuncts spanning both join inputs are merged into the join
// condition.
func pushDownPredicates(p Plan) Plan {
	switch n := p.(type) {
	case *Scan:
		return n
	case *Project:
		return &Project{Exprs: n.Exprs, Child: pushDownPredicates(n.Child)}
	case *Filter:
		return pushFilter(splitAnd(n.Predicate), pushDownPredicates(n.Child))
	case *Join:
		return &Join{
			Cond:  n.Cond,
			Left:  pushDownPredicates(n.Left),
			Right: pushDownPredicates(n.Right),
		}
	}
	return p
}

// pushFilter routes each conjunct through a join or wraps the child.
func pushFilter(conjuncts []Expr, child Plan) Plan {
	join, ok := child.(*Join)
	if !ok {
		return &Filter{Predicate: joinAnd(conjuncts), Child: child}
	}
	leftTables := outputTables(join.Left)
	rightTables := outputTables(join.Right)
	var leftConds, rightConds, joinConds, stay []Expr
	for _, c := range conjuncts {
		refs := referencedTables(c)
		switch {
		case subsetOf(refs, leftTables):
			leftConds = append(leftConds, c)
		case subsetOf(refs, rightTables):
			rightConds = append(rightConds, c)
		case subsetOf(refs, union(leftTables, rightTables)):
			// References both sides of this join: enrich the join condition.
			joinConds = append(joinConds, c)
		default:
			stay = append(stay, c)
		}
	}
	left := join.Left
	if len(leftConds) > 0 {
		left = pushFilter(leftConds, left)
	}
	right := join.Right
	if len(rightConds) > 0 {
		right = pushFilter(rightConds, right)
	}
	cond := join.Cond
	if len(joinConds) > 0 {
		cond = joinAnd(append(splitAnd(cond), joinConds...))
	}
	newJoin := &Join{Cond: cond, Left: left, Right: right}
	if len(stay) > 0 {
		return &Filter{Predicate: joinAnd(stay), Child: newJoin}
	}
	return newJoin
}

func union(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// eliminateProjects removes redundant Project nodes:
//   - a Project directly under another Project is collapsed into the
//     outer one;
//   - a Project whose expressions exactly reproduce the output columns
//     of its child (e.g. SELECT u.* style expansions) is dropped.
func eliminateProjects(p Plan) Plan {
	switch n := p.(type) {
	case *Scan:
		return n
	case *Filter:
		return &Filter{Predicate: n.Predicate, Child: eliminateProjects(n.Child)}
	case *Join:
		return &Join{Cond: n.Cond, Left: eliminateProjects(n.Left), Right: eliminateProjects(n.Right)}
	case *Project:
		child := eliminateProjects(n.Child)
		if inner, ok := child.(*Project); ok {
			// Nested projection: the inner one is redundant.
			return &Project{Exprs: n.Exprs, Child: inner.Child}
		}
		if projectsAllColumns(n, child) {
			return child
		}
		return &Project{Exprs: n.Exprs, Child: child}
	}
	return p
}

// projectsAllColumns reports whether proj simply re-emits every output
// column of child unchanged.
func projectsAllColumns(proj *Project, child Plan) bool {
	scan, ok := child.(*Scan)
	if !ok {
		return false
	}
	// A Project over a bare Scan that selects every column of that scan
	// (written as t.*) carries no information.
	if len(proj.Exprs) == 1 {
		if col, ok := proj.Exprs[0].(*ColumnRef); ok && col.Name == "*" {
			return col.Table == "" || col.Table == scan.Alias
		}
	}
	return false
}
