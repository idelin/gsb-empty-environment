package decorrelator

import (
	"fmt"
	"strings"
)

// PlanNode is a node in the rewritten logical query plan.
type PlanNode interface{ planNode() }

// Scan reads a base table.
type Scan struct {
	Table string
	Alias string
}

// Filter keeps rows matching Pred.
type Filter struct {
	Input PlanNode
	Pred  Expr
}

// Project selects output columns.
type Project struct {
	Input PlanNode
	Star  bool
	Items []SelectItem
}

// Distinct removes duplicate rows (used to preserve IN semantics when an
// uncorrelated IN subquery is converted into an inner join).
type Distinct struct {
	Input PlanNode
}

// HashSemiJoin emits each left row once, if it has at least one match in
// the right input on the equality keys (plus optional residual conditions).
type HashSemiJoin struct {
	Left, Right         PlanNode
	LeftKeys, RightKeys []Expr
	Residual            []Expr
}

// HashInnerJoin joins left and right rows on equality keys.
// Cross marks a cross product (comma-separated FROM entries).
type HashInnerJoin struct {
	Left, Right         PlanNode
	LeftKeys, RightKeys []Expr
	Cross               bool
}

func (*Scan) planNode()          {}
func (*Filter) planNode()        {}
func (*Project) planNode()       {}
func (*Distinct) planNode()      {}
func (*HashSemiJoin) planNode()  {}
func (*HashInnerJoin) planNode() {}

func children(n PlanNode) []PlanNode {
	switch v := n.(type) {
	case *Filter:
		return []PlanNode{v.Input}
	case *Project:
		return []PlanNode{v.Input}
	case *Distinct:
		return []PlanNode{v.Input}
	case *HashSemiJoin:
		return []PlanNode{v.Left, v.Right}
	case *HashInnerJoin:
		return []PlanNode{v.Left, v.Right}
	}
	return nil
}

func describe(n PlanNode) string {
	switch v := n.(type) {
	case *Scan:
		if v.Alias != "" && v.Alias != v.Table {
			return fmt.Sprintf("Scan(%s %s)", v.Table, v.Alias)
		}
		return fmt.Sprintf("Scan(%s)", v.Table)
	case *Filter:
		return fmt.Sprintf("Filter(%s)", ExprString(v.Pred))
	case *Project:
		if v.Star {
			return "Project(*)"
		}
		var cols []string
		for _, it := range v.Items {
			if it.Table != "" {
				cols = append(cols, it.Table+"."+it.Name)
			} else {
				cols = append(cols, it.Name)
			}
		}
		return fmt.Sprintf("Project(%s)", strings.Join(cols, ", "))
	case *Distinct:
		return "Distinct()"
	case *HashSemiJoin:
		return fmt.Sprintf("HashSemiJoin(%s)", joinCond(v.LeftKeys, v.RightKeys, v.Residual))
	case *HashInnerJoin:
		if v.Cross {
			return "CrossJoin"
		}
		return fmt.Sprintf("HashInnerJoin(%s)", joinCond(v.LeftKeys, v.RightKeys, nil))
	}
	return fmt.Sprintf("%T", n)
}

func joinCond(leftKeys, rightKeys, residual []Expr) string {
	var parts []string
	for i := range leftKeys {
		parts = append(parts, ExprString(leftKeys[i])+" = "+ExprString(rightKeys[i]))
	}
	for _, r := range residual {
		parts = append(parts, ExprString(r))
	}
	if len(parts) == 0 {
		return "TRUE"
	}
	return strings.Join(parts, " AND ")
}

// Explain renders the plan as an indented tree.
func Explain(root PlanNode) string {
	var b strings.Builder
	b.WriteString(describe(root))
	b.WriteString("\n")
	explainChildren(&b, root, "")
	return b.String()
}

func explainChildren(b *strings.Builder, n PlanNode, prefix string) {
	kids := children(n)
	for i, c := range kids {
		last := i == len(kids)-1
		connector := "├─ "
		nextPrefix := prefix + "│  "
		if last {
			connector = "└─ "
			nextPrefix = prefix + "   "
		}
		b.WriteString(prefix + connector + describe(c) + "\n")
		explainChildren(b, c, nextPrefix)
	}
}

// RenderSQL renders the plan as illustrative pseudo-SQL. SEMI JOIN is not
// standard SQL; it denotes the semi-join operator in the plan.
func RenderSQL(n PlanNode) string {
	if p, ok := n.(*Project); ok {
		cols := "*"
		if !p.Star {
			var parts []string
			for _, it := range p.Items {
				if it.Table != "" {
					parts = append(parts, it.Table+"."+it.Name)
				} else {
					parts = append(parts, it.Name)
				}
			}
			cols = strings.Join(parts, ", ")
		}
		return "SELECT " + cols + " FROM " + renderFrom(p.Input)
	}
	return renderFrom(n)
}

func renderFrom(n PlanNode) string {
	switch v := n.(type) {
	case *Scan:
		if v.Alias != "" && v.Alias != v.Table {
			return v.Table + " " + v.Alias
		}
		return v.Table
	case *HashSemiJoin:
		return renderFrom(v.Left) + " SEMI JOIN " + renderFrom(v.Right) +
			" ON " + joinCond(v.LeftKeys, v.RightKeys, v.Residual)
	case *HashInnerJoin:
		if v.Cross {
			return renderFrom(v.Left) + " CROSS JOIN " + renderFrom(v.Right)
		}
		return renderFrom(v.Left) + " INNER JOIN " + renderFrom(v.Right) +
			" ON " + joinCond(v.LeftKeys, v.RightKeys, nil)
	case *Filter:
		return "( SELECT * FROM " + renderFrom(v.Input) + " WHERE " + ExprString(v.Pred) + " )"
	case *Distinct:
		return "( SELECT DISTINCT * FROM " + renderFrom(v.Input) + " )"
	case *Project:
		return "( " + RenderSQL(v) + " )"
	}
	return fmt.Sprintf("%T", n)
}
