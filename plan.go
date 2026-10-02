// Package cteopt implements common-subexpression recognition and
// materialized reuse (CTE) for DAG-shaped query plans.
package cteopt

import (
	"fmt"
	"strings"
)

// Row is a single tuple flowing through the plan.
type Row []any

// Op identifies the physical operator of a plan node.
type Op int

const (
	OpScan Op = iota
	OpFilter
	OpProject
	OpJoin
	OpAggregate
	OpUnionAll
	OpMaterialize
)

func (o Op) String() string {
	switch o {
	case OpScan:
		return "Scan"
	case OpFilter:
		return "Filter"
	case OpProject:
		return "Project"
	case OpJoin:
		return "Join"
	case OpAggregate:
		return "Aggregate"
	case OpUnionAll:
		return "UnionAll"
	case OpMaterialize:
		return "Materialize"
	}
	return "?"
}

// Node is a vertex of the plan DAG. A CTE referenced by two branches is
// represented by a single *Node shared by two parents.
type Node struct {
	Op       Op
	Label    string // table name, predicate text, etc. Part of the fingerprint.
	Children []*Node

	Pred     func(Row) bool // Filter
	Proj     func(Row) Row  // Project
	KeyLeft  func(Row) any  // Join build/probe keys
	KeyRight func(Row) any
	GroupBy  func(Row) any // Aggregate
	AggOp    string        // "count" or "sum"
	AggField int           // column index for "sum"

	EstRows float64 // estimated output rows, filled by Annotate
	Cost    float64 // estimated subtree cost, filled by Annotate
}

// Scan reads table `name`.
func Scan(name string) *Node { return &Node{Op: OpScan, Label: name} }

// Filter keeps rows matching pred. label must textually describe pred.
func Filter(label string, pred func(Row) bool, child *Node) *Node {
	return &Node{Op: OpFilter, Label: label, Pred: pred, Children: []*Node{child}}
}

// Project maps each row through proj.
func Project(label string, proj func(Row) Row, child *Node) *Node {
	return &Node{Op: OpProject, Label: label, Proj: proj, Children: []*Node{child}}
}

// Join hash-joins left and right on the given key extractors.
func Join(label string, left, right *Node, kl, kr func(Row) any) *Node {
	return &Node{Op: OpJoin, Label: label, Children: []*Node{left, right}, KeyLeft: kl, KeyRight: kr}
}

// Aggregate groups rows and applies aggOp ("count" or "sum" of aggField).
func Aggregate(label string, groupBy func(Row) any, aggOp string, aggField int, child *Node) *Node {
	return &Node{Op: OpAggregate, Label: label, Children: []*Node{child}, GroupBy: groupBy, AggOp: aggOp, AggField: aggField}
}

// UnionAll concatenates two inputs.
func UnionAll(left, right *Node) *Node {
	return &Node{Op: OpUnionAll, Children: []*Node{left, right}}
}

// Explain renders the plan DAG as an indented tree. Shared subtrees are
// printed once and later occurrences are marked as references.
func Explain(root *Node) string {
	var b strings.Builder
	seen := map[*Node]int{}
	ids := map[*Node]int{}
	next := 0
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		indent := strings.Repeat("  ", depth)
		if seen[n] > 0 {
			fmt.Fprintf(&b, "%s%s [%s] -> ref #%d\n", indent, n.Op, n.Label, ids[n])
			return
		}
		next++
		seen[n] = next
		ids[n] = next
		fmt.Fprintf(&b, "%s%s [%s] #%d rows=%.0f cost=%.1f\n",
			indent, n.Op, n.Label, next, n.EstRows, n.Cost)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return b.String()
}
