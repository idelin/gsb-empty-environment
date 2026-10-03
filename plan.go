package decor

import (
	"strings"
)

// Node is a logical plan operator.
type Node interface {
	Children() []Node
	String() string // one-line operator description
}

// Scan reads a base table under an alias.
type Scan struct {
	Table string
	Alias string
}

// Filter keeps rows satisfying Pred.
type Filter struct {
	Pred  Expr
	Child Node
}

// Project keeps only the listed columns.
type Project struct {
	Cols  []*ColumnRef
	Child Node
}

// Distinct removes duplicate rows.
type Distinct struct {
	Child Node
}

// Limit keeps at most N rows.
type Limit struct {
	N     int
	Child Node
}

// HashSemiJoin emits left rows whose key tuple exists in the (hashed) right
// side. This is the physical operator correlated EXISTS/IN lower to.
type HashSemiJoin struct {
	LeftKeys  []*ColumnRef
	RightKeys []*ColumnRef
	Left      Node
	Right     Node
}

// CrossJoin emits the cartesian product (used for uncorrelated EXISTS with
// a LIMIT 1 right side).
type CrossJoin struct {
	Left  Node
	Right Node
}

func (n *Scan) Children() []Node         { return nil }
func (n *Filter) Children() []Node       { return []Node{n.Child} }
func (n *Project) Children() []Node      { return []Node{n.Child} }
func (n *Distinct) Children() []Node     { return []Node{n.Child} }
func (n *Limit) Children() []Node        { return []Node{n.Child} }
func (n *HashSemiJoin) Children() []Node { return []Node{n.Left, n.Right} }
func (n *CrossJoin) Children() []Node    { return []Node{n.Left, n.Right} }

func (n *Scan) String() string {
	if n.Alias != "" && n.Alias != n.Table {
		return "Scan " + n.Table + " AS " + n.Alias
	}
	return "Scan " + n.Table
}

func (n *Filter) String() string { return "Filter " + RenderExpr(n.Pred) }

func (n *Project) String() string {
	parts := make([]string, len(n.Cols))
	for i, c := range n.Cols {
		parts[i] = RenderExpr(c)
	}
	return "Project " + strings.Join(parts, ", ")
}

func (n *Distinct) String() string { return "Distinct" }
func (n *Limit) String() string    { return "Limit " + itoa(n.N) }

func (n *HashSemiJoin) String() string {
	conds := make([]string, len(n.LeftKeys))
	for i := range n.LeftKeys {
		conds[i] = RenderExpr(n.LeftKeys[i]) + " = " + RenderExpr(n.RightKeys[i])
	}
	return "HashSemiJoin ON " + strings.Join(conds, " AND ")
}

func (n *CrossJoin) String() string { return "CrossJoin" }

// RenderPlan renders a plan tree with box-drawing connectors.
func RenderPlan(n Node) string {
	var b strings.Builder
	b.WriteString(n.String())
	b.WriteString("\n")
	children := n.Children()
	for i, c := range children {
		renderPlan(&b, c, "", i == len(children)-1)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderPlan(b *strings.Builder, n Node, prefix string, last bool) {
	conn := "├─ "
	childPrefix := prefix + "│  "
	if last {
		conn = "└─ "
		childPrefix = prefix + "   "
	}
	b.WriteString(prefix + conn + n.String() + "\n")
	children := n.Children()
	for i, c := range children {
		renderPlan(b, c, childPrefix, i == len(children)-1)
	}
}

// planForSelect builds a plain (non-decorrelated) plan for a SELECT.
func planForSelect(stmt *SelectStmt) Node {
	n := scanOrCross(stmt.From)
	if stmt.Where != nil {
		n = &Filter{Pred: stmt.Where, Child: n}
	}
	return n
}

// buildPlan builds the logical plan for a decorrelated query.
func buildPlan(stmt *SelectStmt, semis []*semiJoin, remaining []Expr) Node {
	left := scanOrCross(stmt.From)
	for _, sj := range semis {
		right := planForDerived(sj.derived)
		if sj.kind == "CROSS" {
			left = &CrossJoin{Left: left, Right: right}
			continue
		}
		left = &HashSemiJoin{
			LeftKeys:  sj.leftKeys,
			RightKeys: sj.rightKeys,
			Left:      left,
			Right:     right,
		}
	}
	if len(remaining) > 0 {
		left = &Filter{Pred: joinAnd(remaining), Child: left}
	}
	return left
}

// planForDerived builds Scan -> Filter -> Project -> Distinct -> Limit.
func planForDerived(s *SelectStmt) Node {
	n := scanOrCross(s.From)
	if s.Where != nil {
		n = &Filter{Pred: s.Where, Child: n}
	}
	if allColumnRefs(s.Items) {
		cols := make([]*ColumnRef, len(s.Items))
		for i, it := range s.Items {
			cols[i] = it.Expr.(*ColumnRef)
		}
		n = &Project{Cols: cols, Child: n}
	}
	if s.Distinct {
		n = &Distinct{Child: n}
	}
	if s.Limit != nil {
		n = &Limit{N: *s.Limit, Child: n}
	}
	return n
}

func allColumnRefs(items []SelectItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if it.Expr == nil {
			return false
		}
		if _, ok := it.Expr.(*ColumnRef); !ok {
			return false
		}
	}
	return true
}

func scanOrCross(from []TableRef) Node {
	var n Node
	for _, t := range from {
		s := &Scan{Table: t.Name, Alias: t.EffectiveName()}
		if n == nil {
			n = s
		} else {
			n = &CrossJoin{Left: n, Right: s}
		}
	}
	return n
}
