// Package cteopt implements common-subexpression recognition and cost-based
// materialization reuse (CTE spooling) for DAG-shaped query plans.
package cteopt

import (
	"fmt"
	"strings"
)

// Op identifies the physical operator of a plan node.
type Op int

const (
	Scan        Op = iota // base table scan
	Filter                // predicate filter
	Project               // column projection
	Join                  // nested-loop equi-join
	Rename                // re-prefix row columns with an alias
	Materialize           // compute child once, spool rows into a memory buffer
	CTERef                // read rows from a Materialize buffer
)

func (o Op) String() string {
	switch o {
	case Scan:
		return "Scan"
	case Filter:
		return "Filter"
	case Project:
		return "Project"
	case Join:
		return "Join"
	case Rename:
		return "Rename"
	case Materialize:
		return "Materialize"
	case CTERef:
		return "CTERef"
	}
	return "?"
}

// Node is a vertex of the plan DAG. A subplan referenced from several
// branches (e.g. a CTE used twice) appears as a shared *Node.
type Node struct {
	Op       Op
	Table    string   // Scan: table name
	Alias    string   // Scan/Rename: output column prefix
	Pred     string   // Filter: predicate, e.g. "o.amount > 100 AND o.id < 5"
	Cols     []string // Project: columns ("*" = all)
	JoinKey  [2]string
	Name     string // Materialize/CTERef: spool name
	Src      *Node  // CTERef: the Materialize it reads from
	Buf      []Row  // Materialize: filled at execution time
	Rows     float64
	Cost     float64
	Children []*Node
}

// String renders the plan tree with estimates.
func (n *Node) String() string {
	var b strings.Builder
	n.format(&b, 0, map[*Node]bool{})
	return b.String()
}

func (n *Node) format(b *strings.Builder, depth int, seen map[*Node]bool) {
	indent := strings.Repeat("  ", depth)
	var detail string
	switch n.Op {
	case Scan:
		detail = n.Table + " AS " + n.Alias
	case Filter:
		detail = n.Pred
	case Project:
		detail = strings.Join(n.Cols, ", ")
	case Join:
		detail = n.JoinKey[0] + " = " + n.JoinKey[1]
	case Rename:
		detail = n.Alias
	case Materialize:
		detail = n.Name
	case CTERef:
		detail = n.Name
	}
	fmt.Fprintf(b, "%s%s(%s) rows=%.0f cost=%.0f", indent, n.Op, detail, n.Rows, n.Cost)
	if seen[n] && n.Op != CTERef {
		fmt.Fprintf(b, "  [shared]")
	}
	b.WriteByte('\n')
	if seen[n] {
		return
	}
	seen[n] = true
	for _, c := range n.Children {
		c.format(b, depth+1, seen)
	}
	if n.Op == CTERef && n.Src != nil && !seen[n.Src] {
		fmt.Fprintf(b, "%sspool %s:\n", indent, n.Name)
		n.Src.format(b, depth+1, seen)
	}
}
