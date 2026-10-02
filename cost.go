package cteopt

import "strings"

// Stats maps a base table name to its estimated row count.
type Stats map[string]float64

// Cost model constants (abstract CPU/IO units).
const (
	scanRowCost     = 1.0
	filterRowCost   = 0.5
	projRowCost     = 0.1
	joinRowCost     = 2.0
	writeRowCost    = 1.0   // spool one row into the memory buffer
	readRowCost     = 0.2   // read one row back from the buffer
	materializeFix  = 100.0 // fixed cost of setting up a spool buffer
	joinSelectivity = 0.02
)

// Estimate fills Rows and Cost for every node, bottom-up.
// Shared (DAG) nodes are estimated once.
func Estimate(n *Node, st Stats) { estimate(n, st, map[*Node]bool{}) }

func estimate(n *Node, st Stats, done map[*Node]bool) {
	if done[n] {
		return
	}
	done[n] = true
	for _, c := range n.Children {
		estimate(c, st, done)
	}
	switch n.Op {
	case Scan:
		rows := st[n.Table]
		if rows <= 0 {
			rows = 1000
		}
		n.Rows = rows
		n.Cost = rows * scanRowCost
	case Filter:
		c := n.Children[0]
		n.Rows = c.Rows * selectivity(n.Pred)
		n.Cost = c.Cost + c.Rows*filterRowCost
	case Project, Rename:
		c := n.Children[0]
		n.Rows = c.Rows
		n.Cost = c.Cost + c.Rows*projRowCost
	case Join:
		l, r := n.Children[0], n.Children[1]
		n.Rows = l.Rows * r.Rows * joinSelectivity
		n.Cost = l.Cost + r.Cost + (l.Rows+r.Rows)*joinRowCost
	case Materialize:
		c := n.Children[0]
		n.Rows = c.Rows
		n.Cost = c.Cost + c.Rows*writeRowCost + materializeFix
	case CTERef:
		n.Cost = n.Rows * readRowCost
	}
}

// selectivity estimates the fraction of rows surviving a predicate.
func selectivity(pred string) float64 {
	sel := 1.0
	for _, cond := range strings.Split(pred, " AND ") {
		switch {
		case strings.Contains(cond, "!="):
			sel *= 0.9
		case strings.Contains(cond, "="):
			sel *= 0.1
		default: // <, <=, >, >=
			sel *= 0.25
		}
	}
	return sel
}
