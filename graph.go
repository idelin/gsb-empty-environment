package wafdecision

// NodeKind labels a node of the compiled decision DAG.
type NodeKind uint8

const (
	// NodeTest evaluates detector Leaf and dispatches on its Boolean result.
	NodeTest NodeKind = iota + 1
	// NodeAllow terminates evaluation with an allow verdict.
	NodeAllow
	// NodeDeny terminates evaluation with a deny verdict.
	NodeDeny
)

// Node is one node of the compiled decision DAG.
//
// For a test node, OnFalse/OnTrue are the node identifiers reached when the
// detector returns false or true respectively. Terminal nodes store -1 in
// both successor fields.
type Node struct {
	Kind    NodeKind
	Leaf    int
	OnFalse int
	OnTrue  int
}

// Detector is the pure per-leaf function invoked at runtime. Implementations
// must be safe for concurrent use; a panic is converted into a deny verdict.
type Detector func() bool

// Stats summarizes one request evaluation.
type Stats struct {
	// Tested is the set of leaf identifiers that were invoked.
	Tested map[int]bool
	// TotalCost is the sum of the costs of the invoked leaves.
	TotalCost uint64
	// CallCount is the number of detector invocations. It equals len(Tested)
	// because memoization guarantees each leaf runs at most once per request.
	CallCount int
}

// Verdict is the final allow/deny decision for a request.
type Verdict uint8

const (
	// Allow means the rule evaluated to false: the request is permitted.
	Allow Verdict = iota
	// Deny means the rule evaluated to true or a detector failed: the request
	// is rejected.
	Deny
)

// Outcome is the result of Decide, including failure information.
type Outcome struct {
	Verdict Verdict
	Stats   Stats
	// Panicked reports whether a detector panicked; such requests are denied.
	Panicked bool
	// PanicLeaf is the identifier of the leaf whose detector panicked, if any.
	PanicLeaf int
	// PanicValue holds the recovered panic value.
	PanicValue any
}

// Graph is a compiled, immutable decision DAG ready for production use.
type Graph struct {
	nodes   []Node
	root    int
	specs   []LeafSpec
	costs   []uint64
	formula Formula
	// WorstCost is the minimum achievable maximum total cost over all truth
	// assignments, computed by the compiler.
	WorstCost uint64
	// MidExpectedCost is the expected total cost at selectivity midpoints,
	// assuming independent detectors.
	MidExpectedCost float64
	// StatesVisited is the number of distinct partial-assignment states
	// explored during compilation.
	StatesVisited int
	// LeafCount is the number of distinct detector leaves.
	LeafCount int
}

// Nodes returns a copy of the DAG nodes, indexed by node identifier.
func (g *Graph) Nodes() []Node {
	out := make([]Node, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Root returns the node identifier of the DAG entry point.
func (g *Graph) Root() int { return g.root }

// Specs returns the ordered leaf specifications used for compilation.
func (g *Graph) Specs() []LeafSpec {
	out := make([]LeafSpec, len(g.specs))
	copy(out, g.specs)
	return out
}

// SourceFormula returns the original rule compiled into this graph.
func (g *Graph) SourceFormula() Formula { return g.formula }

// Eval evaluates the graph without panic recovery. Detectors are invoked
// through d keyed by leaf identifier; a missing entry returns false.
//
// Use Decide in production: Eval is intended for deterministic tests and for
// callers that handle detector failures themselves.
func (g *Graph) Eval(d map[int]Detector) (Verdict, Stats) {
	v, st, _ := g.run(d, false)
	return v, st
}

// Decide evaluates the graph with uniform failure handling. Any detector
// panic is recovered and converted into Deny; partial cost accounting is
// preserved and the failing leaf is reported on the Outcome.
func (g *Graph) Decide(d map[int]Detector) Outcome {
	v, st, failure := g.run(d, true)
	if failure == nil {
		return Outcome{Verdict: v, Stats: st}
	}
	return Outcome{
		Verdict:    Deny,
		Stats:      st,
		Panicked:   true,
		PanicLeaf:  failure.leaf,
		PanicValue: failure.value,
	}
}

type panicFailure struct {
	leaf  int
	value any
}

// run walks the DAG. The local tested bitmap enforces the at-most-once
// contract even if the DAG reaches a test node through more than one path.
func (g *Graph) run(d map[int]Detector, recoverPanic bool) (Verdict, Stats, *panicFailure) {
	st := Stats{Tested: make(map[int]bool)}
	var tested uint16
	id := g.root
	for {
		nd := g.nodes[id]
		switch nd.Kind {
		case NodeAllow:
			return Allow, st, nil
		case NodeDeny:
			return Deny, st, nil
		case NodeTest:
			bit := uint16(1) << uint(nd.Leaf)
			if tested&bit != 0 {
				// Unreachable in well-formed graphs; keep the invariant explicit.
				if id = nd.OnFalse; id < 0 {
					return Deny, st, nil
				}
				continue
			}
			var result bool
			failure := invokeDetector(d, nd.Leaf, recoverPanic, &result)
			tested |= bit
			st.Tested[nd.Leaf] = true
			st.CallCount++
			st.TotalCost += g.costs[nd.Leaf]
			if failure != nil {
				return Deny, st, failure
			}
			if result {
				id = nd.OnTrue
			} else {
				id = nd.OnFalse
			}
		default:
			return Deny, st, &panicFailure{value: "wafdecision: corrupt graph node"}
		}
	}
}

func invokeDetector(d map[int]Detector, leaf int, recoverPanic bool, result *bool) (failure *panicFailure) {
	det, ok := d[leaf]
	if !ok || det == nil {
		*result = false
		return nil
	}
	if !recoverPanic {
		*result = det()
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			failure = &panicFailure{leaf: leaf, value: r}
		}
	}()
	*result = det()
	return nil
}
