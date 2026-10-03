package decorrelator

import "fmt"

// Unnest rewrites correlated EXISTS / IN subqueries in stmt's WHERE clause
// into joins, returning a logical plan free of subqueries:
//
//   - correlated EXISTS          -> HashSemiJoin
//   - correlated IN              -> HashSemiJoin
//   - uncorrelated IN            -> HashInnerJoin against a deduplicated
//     (Distinct) subquery result, preserving bag semantics
//
// Correlation equality predicates become hash-join keys; any other
// correlated predicate becomes a residual join condition; predicates
// touching only inner tables are pushed down as filters on the inner side.
func Unnest(stmt *SelectStmt) (PlanNode, error) {
	outerAliases := aliasSet(stmt.From)
	plan := buildFrom(stmt.From)
	var residual []Expr
	for _, conj := range splitAnd(stmt.Where) {
		switch e := conj.(type) {
		case *ExistsExpr:
			join, err := decorrelateExists(plan, outerAliases, e.Subquery)
			if err != nil {
				return nil, err
			}
			plan = join
		case *InExpr:
			join, err := decorrelateIn(plan, outerAliases, e)
			if err != nil {
				return nil, err
			}
			plan = join
		default:
			residual = append(residual, conj)
		}
	}
	if len(residual) > 0 {
		plan = &Filter{Input: plan, Pred: joinAnd(residual)}
	}
	return &Project{Input: plan, Star: stmt.Star, Items: stmt.Columns}, nil
}

func decorrelateExists(left PlanNode, outer map[string]bool, sub *SelectStmt) (PlanNode, error) {
	inner := aliasSet(sub.From)
	right := buildFrom(sub.From)
	join := &HashSemiJoin{Left: left}
	var innerFilters []Expr
	for _, conj := range splitAnd(sub.Where) {
		class, err := classifyPredicate(conj, outer, inner)
		if err != nil {
			return nil, err
		}
		switch class {
		case classCorrEq:
			b := conj.(*BinaryExpr)
			if sideLocation(b.Left, outer, inner) == sideOuter {
				join.LeftKeys = append(join.LeftKeys, b.Left)
				join.RightKeys = append(join.RightKeys, b.Right)
			} else {
				join.LeftKeys = append(join.LeftKeys, b.Right)
				join.RightKeys = append(join.RightKeys, b.Left)
			}
		case classCorrOther:
			join.Residual = append(join.Residual, conj)
		case classInnerOnly:
			innerFilters = append(innerFilters, conj)
		case classOuterOnly:
			return nil, fmt.Errorf("unsupported: subquery predicate %q references only the outer query", ExprString(conj))
		}
	}
	if len(innerFilters) > 0 {
		right = &Filter{Input: right, Pred: joinAnd(innerFilters)}
	}
	join.Right = right
	return join, nil
}

func decorrelateIn(left PlanNode, outer map[string]bool, in *InExpr) (PlanNode, error) {
	sub := in.Subquery
	if sub.Star || len(sub.Columns) != 1 || sub.Columns[0].Name == "*" || sub.Columns[0].Literal {
		return nil, fmt.Errorf("IN subquery must select exactly one column")
	}
	for t := range referencedTables(in.Left) {
		if t != "" && !outer[t] {
			return nil, fmt.Errorf("IN left operand %q references unknown table %q", ExprString(in.Left), t)
		}
	}
	inner := aliasSet(sub.From)
	right := buildFrom(sub.From)
	correlated := false
	var leftKeys, rightKeys, residual, innerFilters []Expr
	for _, conj := range splitAnd(sub.Where) {
		class, err := classifyPredicate(conj, outer, inner)
		if err != nil {
			return nil, err
		}
		switch class {
		case classCorrEq:
			correlated = true
			b := conj.(*BinaryExpr)
			if sideLocation(b.Left, outer, inner) == sideOuter {
				leftKeys = append(leftKeys, b.Left)
				rightKeys = append(rightKeys, b.Right)
			} else {
				leftKeys = append(leftKeys, b.Right)
				rightKeys = append(rightKeys, b.Left)
			}
		case classCorrOther:
			correlated = true
			residual = append(residual, conj)
		case classInnerOnly:
			innerFilters = append(innerFilters, conj)
		case classOuterOnly:
			return nil, fmt.Errorf("unsupported: subquery predicate %q references only the outer query", ExprString(conj))
		}
	}
	if len(innerFilters) > 0 {
		right = &Filter{Input: right, Pred: joinAnd(innerFilters)}
	}
	inCol := &ColumnRef{Table: sub.Columns[0].Table, Name: sub.Columns[0].Name}
	leftKeys = append([]Expr{in.Left}, leftKeys...)
	rightKeys = append([]Expr{inCol}, rightKeys...)
	if correlated {
		return &HashSemiJoin{Left: left, Right: right, LeftKeys: leftKeys, RightKeys: rightKeys, Residual: residual}, nil
	}
	// Uncorrelated IN: deduplicate the subquery result so the inner join
	// does not multiply outer rows.
	return &HashInnerJoin{Left: left, Right: &Distinct{Input: right}, LeftKeys: leftKeys, RightKeys: rightKeys}, nil
}

// aliasSet maps each FROM entry's alias (defaulting to the table name).
func aliasSet(refs []TableRef) map[string]bool {
	s := make(map[string]bool, len(refs))
	for _, r := range refs {
		s[r.Alias] = true
	}
	return s
}

// buildFrom builds left-deep scans, using cross joins for multiple tables.
func buildFrom(refs []TableRef) PlanNode {
	var plan PlanNode = &Scan{Table: refs[0].Name, Alias: refs[0].Alias}
	for _, r := range refs[1:] {
		plan = &HashInnerJoin{Left: plan, Right: &Scan{Table: r.Name, Alias: r.Alias}, Cross: true}
	}
	return plan
}

// splitAnd flattens a WHERE expression into top-level conjuncts.
func splitAnd(e Expr) []Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*BinaryExpr); ok && b.Op == "AND" {
		return append(splitAnd(b.Left), splitAnd(b.Right)...)
	}
	return []Expr{e}
}

func joinAnd(parts []Expr) Expr {
	e := parts[0]
	for _, p := range parts[1:] {
		e = &BinaryExpr{Op: "AND", Left: e, Right: p}
	}
	return e
}

// referencedTables collects the table qualifiers used by an expression.
// Unqualified columns are recorded under "" and treated as inner-side.
func referencedTables(e Expr) map[string]bool {
	refs := map[string]bool{}
	var walk func(Expr)
	walk = func(x Expr) {
		switch v := x.(type) {
		case *ColumnRef:
			refs[v.Table] = true
		case *BinaryExpr:
			walk(v.Left)
			walk(v.Right)
		}
	}
	walk(e)
	return refs
}

type predClass int

const (
	classInnerOnly predClass = iota // touches only subquery tables
	classCorrEq                     // equality between outer and inner side
	classCorrOther                  // other predicate mixing outer and inner
	classOuterOnly                  // touches only outer tables
)

func classifyPredicate(e Expr, outer, inner map[string]bool) (predClass, error) {
	refs := referencedTables(e)
	hasOuter, hasInner := false, false
	for t := range refs {
		switch {
		case t == "" || inner[t]:
			hasInner = true
		case outer[t]:
			hasOuter = true
		default:
			return 0, fmt.Errorf("predicate %q references unknown table %q", ExprString(e), t)
		}
	}
	switch {
	case hasOuter && hasInner:
		if b, ok := e.(*BinaryExpr); ok && b.Op == "=" {
			l := sideLocation(b.Left, outer, inner)
			r := sideLocation(b.Right, outer, inner)
			if (l == sideOuter && r == sideInner) || (l == sideInner && r == sideOuter) {
				return classCorrEq, nil
			}
		}
		return classCorrOther, nil
	case hasOuter:
		return classOuterOnly, nil
	default:
		return classInnerOnly, nil
	}
}

type side int

const (
	sideInner side = iota
	sideOuter
	sideMixed
)

func sideLocation(e Expr, outer, inner map[string]bool) side {
	refs := referencedTables(e)
	hasOuter, hasInner := false, false
	for t := range refs {
		if t == "" || inner[t] {
			hasInner = true
		} else if outer[t] {
			hasOuter = true
		}
	}
	switch {
	case hasOuter && hasInner:
		return sideMixed
	case hasOuter:
		return sideOuter
	default:
		return sideInner
	}
}
