package decor

import (
	"fmt"
	"strings"
)

// Rewrite is the result of decorrelation: the rewritten SQL plus the
// equivalent logical execution plan.
type Rewrite struct {
	Original string   // original SQL (normalized rendering)
	SQL      string   // rewritten SQL (correlation eliminated)
	Plan     Node     // logical execution plan for the rewritten query
	Rules    []string // descriptions of the rules that fired
	Changed  bool     // true when at least one subquery was unnested
}

// semiJoin describes one unnested subquery: a derived (deduplicated) table
// joined back to the outer query through equality keys.
type semiJoin struct {
	kind      string // "SEMI" or "CROSS" (uncorrelated EXISTS)
	derived   *SelectStmt
	alias     string
	leftKeys  []*ColumnRef // outer-side join keys
	rightKeys []*ColumnRef // derived-table join keys
}

// Decorrelate rewrites correlated EXISTS / IN subqueries in stmt's WHERE
// clause into semi-joins, and uncorrelated EXISTS into a cross join against
// a LIMIT 1 derived table. It returns the rewritten SQL and the logical
// plan (hash semi-join based).
func Decorrelate(stmt *SelectStmt) (*Rewrite, error) {
	outer := map[string]bool{}
	for _, t := range stmt.From {
		outer[t.EffectiveName()] = true
	}

	rw := &Rewrite{Original: RenderSelect(stmt)}
	var semis []*semiJoin
	var remaining []Expr

	for _, c := range splitAnd(stmt.Where) {
		switch e := c.(type) {
		case *ExistsExpr:
			sj, err := buildSemiFromExists(e, outer)
			if err != nil {
				return nil, err
			}
			semis = append(semis, sj)
			if sj.kind == "SEMI" {
				rw.Rules = append(rw.Rules,
					"EXISTS decorrelated -> SEMI JOIN on "+joinKeyText(sj))
			} else {
				rw.Rules = append(rw.Rules,
					"uncorrelated EXISTS -> CROSS JOIN (subquery LIMIT 1)")
			}
		case *InExpr:
			if e.Subquery == nil || e.Negated {
				remaining = append(remaining, c)
				continue
			}
			sj, err := buildSemiFromIn(e, outer)
			if err != nil {
				return nil, err
			}
			semis = append(semis, sj)
			rw.Rules = append(rw.Rules,
				"IN decorrelated -> SEMI JOIN on "+joinKeyText(sj))
		default:
			remaining = append(remaining, c)
		}
	}

	if len(semis) == 0 {
		rw.SQL = rw.Original
		rw.Plan = planForSelect(stmt)
		return rw, nil
	}

	rw.Changed = true
	rw.SQL = renderRewritten(stmt, semis, remaining)
	rw.Plan = buildPlan(stmt, semis, remaining)
	return rw, nil
}

func joinKeyText(sj *semiJoin) string {
	parts := make([]string, len(sj.leftKeys))
	for i := range sj.leftKeys {
		parts[i] = RenderExpr(sj.leftKeys[i]) + " = " + RenderExpr(sj.rightKeys[i])
	}
	return strings.Join(parts, " AND ")
}

// buildSemiFromExists converts EXISTS (subquery) into a semi join.
func buildSemiFromExists(e *ExistsExpr, outer map[string]bool) (*semiJoin, error) {
	sub := e.Subquery
	corr, local := partitionPredicates(sub, outer)
	sj := &semiJoin{kind: "SEMI", alias: sub.From[0].EffectiveName()}

	if len(corr) == 0 {
		// Uncorrelated EXISTS: true iff the subquery yields >= 1 row.
		// Rewrite as CROSS JOIN (subquery LIMIT 1) — empty side empties the join.
		one := 1
		sub.Limit = &one
		sj.kind = "CROSS"
		sj.derived = sub
		return sj, nil
	}

	var proj []SelectItem
	seen := map[string]bool{}
	for _, c := range corr {
		outerRef, subRef, err := extractEqualityKey(c, outer)
		if err != nil {
			return nil, err
		}
		sj.leftKeys = append(sj.leftKeys, outerRef)
		sj.rightKeys = append(sj.rightKeys, subRef)
		if key := RenderExpr(subRef); !seen[key] {
			seen[key] = true
			proj = append(proj, SelectItem{Expr: subRef})
		}
	}
	sj.derived = &SelectStmt{
		Distinct: true,
		Items:    proj,
		From:     sub.From,
		Where:    joinAnd(local),
	}
	return sj, nil
}

// buildSemiFromIn converts x IN (SELECT col ...) into a semi join whose
// primary key is x = col, plus any correlated equality predicates.
func buildSemiFromIn(e *InExpr, outer map[string]bool) (*semiJoin, error) {
	sub := e.Subquery
	if len(sub.Items) != 1 || sub.Items[0].Star || sub.Items[0].TableStar != "" {
		return nil, fmt.Errorf("cannot decorrelate: IN subquery must project exactly one column")
	}
	subCol, ok := sub.Items[0].Expr.(*ColumnRef)
	if !ok {
		return nil, fmt.Errorf("cannot decorrelate: IN subquery must project a plain column, got %s",
			RenderExpr(sub.Items[0].Expr))
	}
	leftCol, ok := e.Left.(*ColumnRef)
	if !ok {
		return nil, fmt.Errorf("cannot decorrelate: IN left side must be a plain column, got %s",
			RenderExpr(e.Left))
	}

	corr, local := partitionPredicates(sub, outer)
	sj := &semiJoin{kind: "SEMI", alias: sub.From[0].EffectiveName()}
	sj.leftKeys = append(sj.leftKeys, leftCol)
	sj.rightKeys = append(sj.rightKeys, subCol)

	proj := []SelectItem{sub.Items[0]}
	seen := map[string]bool{RenderExpr(subCol): true}
	for _, c := range corr {
		outerRef, subRef, err := extractEqualityKey(c, outer)
		if err != nil {
			return nil, err
		}
		sj.leftKeys = append(sj.leftKeys, outerRef)
		sj.rightKeys = append(sj.rightKeys, subRef)
		if key := RenderExpr(subRef); !seen[key] {
			seen[key] = true
			proj = append(proj, SelectItem{Expr: subRef})
		}
	}
	sj.derived = &SelectStmt{
		Distinct: true,
		Items:    proj,
		From:     sub.From,
		Where:    joinAnd(local),
	}
	return sj, nil
}

// extractEqualityKey splits a correlated conjunct of the form
// outer.col = sub.col into its two sides. Only equality predicates can be
// turned into hash-join keys.
func extractEqualityKey(c Expr, outer map[string]bool) (outerRef, subRef *ColumnRef, err error) {
	be, ok := c.(*BinaryExpr)
	if !ok || be.Op != "=" {
		return nil, nil, fmt.Errorf("cannot decorrelate non-equality correlated predicate: %s", RenderExpr(c))
	}
	lc, lok := be.Left.(*ColumnRef)
	rc, rok := be.Right.(*ColumnRef)
	if !lok || !rok {
		return nil, nil, fmt.Errorf("cannot decorrelate correlated predicate with non-column operand: %s", RenderExpr(c))
	}
	lOuter, rOuter := outer[lc.Table], outer[rc.Table]
	switch {
	case lOuter && !rOuter:
		return lc, rc, nil
	case rOuter && !lOuter:
		return rc, lc, nil
	default:
		return nil, nil, fmt.Errorf("correlated predicate does not link outer query to subquery: %s", RenderExpr(c))
	}
}

// partitionPredicates splits the subquery's WHERE conjuncts into correlated
// (reference outer-query aliases) and local predicates.
func partitionPredicates(sub *SelectStmt, outer map[string]bool) (correlated, local []Expr) {
	for _, c := range splitAnd(sub.Where) {
		refs := map[string]bool{}
		referencedTables(c, refs)
		isCorr := false
		for t := range refs {
			if outer[t] {
				isCorr = true
				break
			}
		}
		if isCorr {
			correlated = append(correlated, c)
		} else {
			local = append(local, c)
		}
	}
	return correlated, local
}

func splitAnd(e Expr) []Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*BinaryExpr); ok && b.Op == "AND" {
		return append(splitAnd(b.Left), splitAnd(b.Right)...)
	}
	return []Expr{e}
}

func joinAnd(es []Expr) Expr {
	if len(es) == 0 {
		return nil
	}
	out := es[0]
	for _, x := range es[1:] {
		out = &BinaryExpr{Op: "AND", Left: out, Right: x}
	}
	return out
}

func referencedTables(e Expr, out map[string]bool) {
	switch x := e.(type) {
	case *ColumnRef:
		if x.Table != "" {
			out[x.Table] = true
		}
	case *BinaryExpr:
		referencedTables(x.Left, out)
		referencedTables(x.Right, out)
	case *NotExpr:
		referencedTables(x.X, out)
	case *ExistsExpr:
		for _, it := range x.Subquery.Items {
			if it.Expr != nil {
				referencedTables(it.Expr, out)
			}
		}
		if x.Subquery.Where != nil {
			referencedTables(x.Subquery.Where, out)
		}
	case *InExpr:
		referencedTables(x.Left, out)
		if x.Subquery != nil && x.Subquery.Where != nil {
			referencedTables(x.Subquery.Where, out)
		}
		for _, le := range x.List {
			referencedTables(le, out)
		}
	}
}

// renderRewritten renders the decorrelated query: outer projection, outer
// FROM, one SEMI/CROSS JOIN per unnested subquery, and residual predicates.
func renderRewritten(stmt *SelectStmt, semis []*semiJoin, remaining []Expr) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	items := make([]string, 0, len(stmt.Items))
	for _, it := range stmt.Items {
		switch {
		case it.Star:
			// Semi joins only return left-side columns, so expand * to the
			// outer tables only.
			for _, t := range stmt.From {
				items = append(items, t.EffectiveName()+".*")
			}
		case it.TableStar != "":
			items = append(items, it.TableStar+".*")
		default:
			items = append(items, RenderExpr(it.Expr))
		}
	}
	b.WriteString(strings.Join(items, ", "))
	b.WriteString(" FROM ")
	tables := make([]string, 0, len(stmt.From))
	for _, t := range stmt.From {
		x := t.Name
		if t.Alias != "" {
			x += " " + t.Alias
		}
		tables = append(tables, x)
	}
	b.WriteString(strings.Join(tables, ", "))
	for _, sj := range semis {
		if sj.kind == "CROSS" {
			b.WriteString(" CROSS JOIN (")
			b.WriteString(RenderSelect(sj.derived))
			b.WriteString(") ")
			b.WriteString(sj.alias)
			continue
		}
		b.WriteString(" SEMI JOIN (")
		b.WriteString(RenderSelect(sj.derived))
		b.WriteString(") ")
		b.WriteString(sj.alias)
		b.WriteString(" ON ")
		conds := make([]string, len(sj.leftKeys))
		for i := range sj.leftKeys {
			conds[i] = RenderExpr(sj.leftKeys[i]) + " = " + RenderExpr(sj.rightKeys[i])
		}
		b.WriteString(strings.Join(conds, " AND "))
	}
	if len(remaining) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(RenderExpr(joinAnd(remaining)))
	}
	return b.String()
}
