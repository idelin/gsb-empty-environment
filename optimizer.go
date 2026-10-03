package relalg

// Optimize 对逻辑计划依次应用规则优化：
//  1. 谓词关联下推：将 Filter 的合取谓词按引用列归属下推到 Join 两侧，
//     跨两侧的等值条件合并进 Join 的 ON 条件；
//  2. 投影消除：合并嵌套 Project，并移除输出与子算子完全一致的恒等投影。
func Optimize(p Plan) Plan {
	p = pushDownPredicates(p)
	p = eliminateProjections(p)
	return p
}

// pushDownPredicates 自底向上递归地下推 Filter。
func pushDownPredicates(p Plan) Plan {
	switch n := p.(type) {
	case *Filter:
		child := pushDownPredicates(n.Input)
		if j, ok := child.(*Join); ok {
			return distributeFilter(n.Cond, j)
		}
		return &Filter{Cond: n.Cond, Input: child}
	case *Project:
		return &Project{Exprs: n.Exprs, Input: pushDownPredicates(n.Input)}
	case *Join:
		return &Join{
			Kind:  n.Kind,
			Cond:  n.Cond,
			Left:  pushDownPredicates(n.Left),
			Right: pushDownPredicates(n.Right),
		}
	default:
		return p
	}
}

// distributeFilter 将 Filter 的合取项按引用列归属分配到 Join 的左、右、
// ON 条件或保留在 Join 之上。
func distributeFilter(cond Expr, j *Join) Plan {
	leftSet := stringSet(j.Left.Aliases())
	rightSet := stringSet(j.Right.Aliases())
	var leftConds, rightConds, joinConds, rest []Expr
	for _, c := range splitAnd(cond) {
		refs, qualified := referencedTables(c)
		switch {
		case !qualified || len(refs) == 0:
			// 含未限定列或为常量条件：保守保留在 Join 之上。
			rest = append(rest, c)
		case subsetOf(refs, leftSet):
			leftConds = append(leftConds, c)
		case subsetOf(refs, rightSet):
			rightConds = append(rightConds, c)
		case isJoinKey(c, leftSet, rightSet):
			joinConds = append(joinConds, c)
		default:
			rest = append(rest, c)
		}
	}
	if len(leftConds) > 0 {
		j.Left = pushDownPredicates(&Filter{Cond: combineAnd(leftConds), Input: j.Left})
	}
	if len(rightConds) > 0 {
		j.Right = pushDownPredicates(&Filter{Cond: combineAnd(rightConds), Input: j.Right})
	}
	if len(joinConds) > 0 {
		all := append(splitAnd(j.Cond), joinConds...)
		j.Cond = combineAnd(all)
	}
	if len(rest) > 0 {
		return &Filter{Cond: combineAnd(rest), Input: j}
	}
	return j
}

// eliminateProjections 合并嵌套 Project 并移除恒等投影。
func eliminateProjections(p Plan) Plan {
	switch n := p.(type) {
	case *Project:
		input := eliminateProjections(n.Input)
		if inner, ok := input.(*Project); ok {
			// 投影均为纯列子集，外层投影列表直接作用于更下层输入。
			input = inner.Input
		}
		if isIdentityProjection(n.Exprs, input.Schema()) {
			return input
		}
		return &Project{Exprs: n.Exprs, Input: input}
	case *Filter:
		return &Filter{Cond: n.Cond, Input: eliminateProjections(n.Input)}
	case *Join:
		return &Join{
			Kind:  n.Kind,
			Cond:  n.Cond,
			Left:  eliminateProjections(n.Left),
			Right: eliminateProjections(n.Right),
		}
	default:
		return p
	}
}

// isIdentityProjection 判断投影输出是否与子算子 Schema 完全一致。
func isIdentityProjection(exprs []Expr, schema []string) bool {
	if len(exprs) == 1 {
		if s, ok := exprs[0].(*Star); ok && s.Table == "" {
			return true
		}
	}
	if len(exprs) != len(schema) {
		return false
	}
	for i, e := range exprs {
		c, ok := e.(*ColumnRef)
		if !ok || c.String() != schema[i] {
			return false
		}
	}
	return true
}

// splitAnd 将 AND 合取式拆分为合取项列表。
func splitAnd(e Expr) []Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*BinaryExpr); ok && b.Op == "AND" {
		return append(splitAnd(b.Left), splitAnd(b.Right)...)
	}
	return []Expr{e}
}

// combineAnd 将合取项列表重新组合为左结合的 AND 表达式。
func combineAnd(exprs []Expr) Expr {
	if len(exprs) == 0 {
		return nil
	}
	out := exprs[0]
	for _, e := range exprs[1:] {
		out = &BinaryExpr{Op: "AND", Left: out, Right: e}
	}
	return out
}

// referencedTables 返回表达式中限定列引用的表集合；
// 若存在未限定列，第二个返回值为 false。
func referencedTables(e Expr) (map[string]bool, bool) {
	refs := map[string]bool{}
	qualified := true
	WalkExpr(e, func(x Expr) {
		if c, ok := x.(*ColumnRef); ok {
			if c.Table == "" {
				qualified = false
			} else {
				refs[c.Table] = true
			}
		}
	})
	return refs, qualified
}

// isJoinKey 判断等值条件是否连接 Join 两侧（一侧一列）。
func isJoinKey(e Expr, leftSet, rightSet map[string]bool) bool {
	b, ok := e.(*BinaryExpr)
	if !ok || b.Op != "=" {
		return false
	}
	lt, lok := referencedTables(b.Left)
	rt, rok := referencedTables(b.Right)
	if !lok || !rok || len(lt) == 0 || len(rt) == 0 {
		return false
	}
	return (subsetOf(lt, leftSet) && subsetOf(rt, rightSet)) ||
		(subsetOf(lt, rightSet) && subsetOf(rt, leftSet))
}

func stringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, s := range items {
		set[s] = true
	}
	return set
}

func subsetOf(sub, super map[string]bool) bool {
	for k := range sub {
		if !super[k] {
			return false
		}
	}
	return true
}
