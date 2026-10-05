// Package planner implements an incremental build planner.
//
// The planner distinguishes two kinds of dependencies:
//
//   - Content dependencies: a change of the dependency's content invalidates
//     the dependent task. Invalidation propagates only along these edges.
//   - Order-only dependencies: they constrain scheduling order but never
//     propagate invalidation.
//
// Tasks may discover additional dependencies while they run. Newly discovered
// dependencies can cause extra tasks to be scheduled, but a result produced
// from stale inputs is never committed: the discovering task is re-run once
// its newly discovered inputs are up to date.
//
// For every rebuilt node the planner records the shortest reason chain
// explaining why the rebuild happened.
package planner

// DepKind classifies a dependency edge.
type DepKind int

const (
	// Content means invalidation propagates along this edge.
	Content DepKind = iota
	// OrderOnly means the edge only constrains scheduling order.
	OrderOnly
)

// Dep is a dependency on another node (a file path or a task name).
type Dep struct {
	Node string
	Kind DepKind
}

// ContentDep declares a content dependency on node.
func ContentDep(node string) Dep { return Dep{Node: node, Kind: Content} }

// OrderDep declares an order-only dependency on node.
func OrderDep(node string) Dep { return Dep{Node: node, Kind: OrderOnly} }
