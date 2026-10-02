package cteopt

import "math"

// Cost model constants (abstract cost units).
const (
	ScanPerRow  = 1.0
	CPUPerRow   = 1.0
	JoinPerRow  = 2.0
	MatWriteRow = 0.5 // write one row into the temp memory buffer
	MatReadRow  = 0.2 // read one row back from the buffer

	FilterSelectivity = 0.3

	// MinCostToMaterialize avoids materializing trivial subtrees where the
	// bookkeeping overhead would dominate.
	MinCostToMaterialize = 10.0
)

// Annotate walks the DAG bottom-up and fills EstRows and Cost on every node.
// Shared subtrees are costed once; Cost is the cost of computing the subtree
// rooted at the node a single time.
func Annotate(root *Node, tables map[string][]Row) {
	type result struct{ rows, cost float64 }
	memo := map[*Node]result{}
	var rec func(n *Node) result
	rec = func(n *Node) result {
		if r, ok := memo[n]; ok {
			return r
		}
		var childCost float64
		childRows := make([]float64, 0, len(n.Children))
		for _, c := range n.Children {
			cr := rec(c)
			childCost += cr.cost
			childRows = append(childRows, cr.rows)
		}
		var r result
		switch n.Op {
		case OpScan:
			r.rows = float64(len(tables[n.Label]))
			r.cost = r.rows * ScanPerRow
		case OpFilter:
			r.rows = childRows[0] * FilterSelectivity
			r.cost = childCost + childRows[0]*CPUPerRow
		case OpProject:
			r.rows = childRows[0]
			r.cost = childCost + childRows[0]*CPUPerRow
		case OpJoin:
			r.rows = math.Max(childRows[0], childRows[1])
			r.cost = childCost + (childRows[0]+childRows[1])*JoinPerRow
		case OpAggregate:
			r.rows = math.Min(childRows[0], 100)
			r.cost = childCost + childRows[0]*CPUPerRow
		case OpUnionAll:
			r.rows = childRows[0] + childRows[1]
			r.cost = childCost
		case OpMaterialize:
			r.rows = childRows[0]
			r.cost = childCost + childRows[0]*MatWriteRow
		}
		n.EstRows, n.Cost = r.rows, r.cost
		memo[n] = r
		return r
	}
	rec(root)
}
