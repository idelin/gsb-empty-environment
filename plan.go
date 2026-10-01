package logplan

import (
	"fmt"
	"strings"
)

// Plan is a node in the relational-algebra logical operator tree.
type Plan interface {
	// Children returns the child operators (0, 1 or 2 depending on node).
	Children() []Plan
	// Name is the operator name, e.g. "Scan".
	Name() string
	// Detail is the operator-specific payload, e.g. "users AS u".
	Detail() string
	planNode()
}

// Scan reads a base table. Leaf operator.
type Scan struct {
	Table string
	Alias string
}

func (*Scan) planNode()        {}
func (*Scan) Children() []Plan { return nil }
func (*Scan) Name() string     { return "Scan" }
func (s *Scan) Detail() string {
	if s.Alias != "" && s.Alias != s.Table {
		return fmt.Sprintf("%s AS %s", s.Table, s.Alias)
	}
	return s.Table
}

// Project selects a list of output expressions.
type Project struct {
	Exprs []Expr
	Child Plan
}

func (*Project) planNode()          {}
func (p *Project) Children() []Plan { return []Plan{p.Child} }
func (*Project) Name() string       { return "Project" }
func (p *Project) Detail() string {
	parts := make([]string, len(p.Exprs))
	for i, e := range p.Exprs {
		parts[i] = e.String()
	}
	return strings.Join(parts, ", ")
}

// Filter keeps rows satisfying a predicate (sigma).
type Filter struct {
	Predicate Expr
	Child     Plan
}

func (*Filter) planNode()          {}
func (f *Filter) Children() []Plan { return []Plan{f.Child} }
func (*Filter) Name() string       { return "Filter" }
func (f *Filter) Detail() string   { return f.Predicate.String() }

// Join combines two inputs on a condition (inner join).
type Join struct {
	Cond  Expr
	Left  Plan
	Right Plan
}

func (*Join) planNode()          {}
func (j *Join) Children() []Plan { return []Plan{j.Left, j.Right} }
func (*Join) Name() string       { return "Join" }
func (j *Join) Detail() string   { return j.Cond.String() }

// Walk performs a pre-order traversal of the plan tree, calling fn on
// every node. Returning false from fn stops the traversal.
func Walk(p Plan, fn func(Plan) bool) {
	if p == nil || !fn(p) {
		return
	}
	for _, c := range p.Children() {
		Walk(c, fn)
	}
}

// String renders the plan as an indented operator tree, e.g.:
//
//	Project(u.name)
//	└── Join(u.id = o.uid)
//	    ├── Scan(users AS u)
//	    └── Scan(orders AS o)
func String(p Plan) string {
	var sb strings.Builder
	if p == nil {
		return ""
	}
	sb.WriteString(p.Name())
	if d := p.Detail(); d != "" {
		sb.WriteString("(" + d + ")")
	}
	sb.WriteString("\n")
	children := p.Children()
	for i, c := range children {
		writeTree(&sb, c, "", i == len(children)-1)
	}
	return sb.String()
}

func writeTree(sb *strings.Builder, p Plan, prefix string, last bool) {
	if p == nil {
		return
	}
	connector := "└── "
	childPrefix := prefix + "    "
	if !last {
		connector = "├── "
		childPrefix = prefix + "│   "
	}
	sb.WriteString(prefix + connector + p.Name())
	if d := p.Detail(); d != "" {
		sb.WriteString("(" + d + ")")
	}
	sb.WriteString("\n")
	children := p.Children()
	for i, c := range children {
		writeTree(sb, c, childPrefix, i == len(children)-1)
	}
}
