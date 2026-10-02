package physprop

import "fmt"

// Optimizer performs bottom-up physical property enforcement: every
// subplan is optimized under an ordering requirement passed down from its
// parent, and a Sort enforcer is inserted only when no available physical
// alternative already delivers the required order.
type Optimizer struct{}

// Optimize returns the cheapest physical plan for the logical plan.
func (o *Optimizer) Optimize(root LogicalPlan) PhysicalPlan {
	return o.best(root, nil)
}

// best returns the cheapest physical plan for node whose output ordering
// satisfies required, inserting a Sort enforcer when necessary.
func (o *Optimizer) best(node LogicalPlan, required Order) PhysicalPlan {
	var candidates []PhysicalPlan

	switch n := node.(type) {
	case *Scan:
		candidates = append(candidates, NewSeqScan(n))
		if n.Index != nil {
			candidates = append(candidates, NewIndexScan(n))
		}

	case *LogicalSort:
		// ORDER BY pushes its keys down as the child's requirement.
		// Option A: the child delivers the order itself (e.g. via an
		// index) and the Sort operator is elided entirely.
		candidates = append(candidates, o.best(n.Child, n.Keys))
		// Option B: enforce the order with an explicit Sort on top of
		// the cheapest (unordered) child plan.
		candidates = append(candidates, NewSort(n.Keys, o.best(n.Child, nil)))

	case *GroupBy:
		// HashAggregate: no ordering requirement on the child, but the
		// output is unordered.
		candidates = append(candidates, NewHashAggregate(n.Keys, o.best(n.Child, nil)))
		// StreamAggregate: requires the child ordered by the grouping
		// keys, and preserves that order on its output.
		candidates = append(candidates, NewStreamAggregate(n.Keys, o.best(n.Child, Order(n.Keys))))

	case *LogicalMergeJoin:
		// MergeJoin requires both inputs ordered on the join keys; the
		// requirements are pushed down so indexes can satisfy them.
		left := o.best(n.Left, n.LeftKeys)
		right := o.best(n.Right, n.RightKeys)
		candidates = append(candidates, NewMergeJoin(n.LeftKeys, n.RightKeys, left, right))

	default:
		panic(fmt.Sprintf("physprop: unknown logical node %T", node))
	}

	// Enforcement: pick the cheapest candidate that satisfies the
	// parent's requirement, adding a Sort enforcer when it does not.
	var best PhysicalPlan
	for _, c := range candidates {
		p := c
		if !Satisfies(p.Ordering(), required) {
			p = NewSort(required, p)
		}
		if best == nil || p.Cost() < best.Cost() {
			best = p
		}
	}
	return best
}
