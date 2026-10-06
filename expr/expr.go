// Package expr evaluates arithmetic DAGs of decimal.Values.
//
// Nodes may be shared by multiple parents (a DAG, not a tree). Every
// node declares its own rounding policy: nodes with a Scale are rounded
// half-even to that many decimal places, all other nodes stay exact
// rationals. Results are memoized per node; modifying a node with Set
// invalidates the caches of the node and all its transitive successors.
//
// Evaluation is resumable: an Evaluator runs on an explicit stack and
// can be suspended after a budget of node completions, then resumed
// later without recomputing finished work. Every completed node yields
// a Step recording its value and, when rounded, the rounding evidence.
package expr

import (
	"errors"
	"fmt"

	"gauge/decimal"
)

// ErrSuspended is returned by Evaluator.Run when the completion budget
// is exhausted before the root value is known. Call Run again to resume.
var ErrSuspended = errors.New("expr: evaluation suspended, budget exhausted")

// Op identifies the arithmetic performed by a Node.
type Op int

const (
	OpConst Op = iota // a constant decimal.Value
	OpMul             // A * B
	OpDiv             // A / B
)

func (op Op) String() string {
	switch op {
	case OpConst:
		return "const"
	case OpMul:
		return "mul"
	case OpDiv:
		return "div"
	}
	return "unknown"
}

// Node is one vertex of the expression DAG. A and B name operand nodes
// for OpMul and OpDiv. Scale, when non-nil, marks the node for half-even
// rounding to that many decimal places; nil keeps the node exact.
type Node struct {
	Op    Op
	Value decimal.Value // OpConst only
	A, B  string        // operand node IDs for OpMul and OpDiv
	Scale *int
}

type cacheEntry struct {
	exact    decimal.Value // value before node-level rounding
	value    decimal.Value // value after node-level rounding
	evidence *decimal.RoundEvidence
}

type nodeState struct {
	node     Node
	children map[string]struct{} // IDs of nodes that reference this one
	cache    *cacheEntry
}

// Graph is a mutable expression DAG with memoized evaluation.
type Graph struct {
	nodes map[string]*nodeState
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{nodes: make(map[string]*nodeState)}
}

// Set adds or replaces the node with the given ID. Replacing a node
// invalidates its own cached value and the caches of all transitive
// successors; unrelated nodes keep their caches.
func (g *Graph) Set(id string, n Node) {
	if old, ok := g.nodes[id]; ok {
		for _, dep := range []string{old.node.A, old.node.B} {
			if dep == "" {
				continue
			}
			if ds, ok := g.nodes[dep]; ok {
				delete(ds.children, id)
			}
		}
	}
	ns := &nodeState{node: n, children: make(map[string]struct{})}
	if old, ok := g.nodes[id]; ok {
		ns.children = old.children
	}
	g.nodes[id] = ns
	for _, dep := range []string{n.A, n.B} {
		if dep == "" {
			continue
		}
		if ds, ok := g.nodes[dep]; ok {
			ds.children[id] = struct{}{}
		}
	}
	g.invalidate(id)
}

// invalidate clears the cache of id and every transitive successor.
func (g *Graph) invalidate(id string) {
	stack := []string{id}
	seen := map[string]struct{}{id: {}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ns := g.nodes[cur]
		ns.cache = nil
		for child := range ns.children {
			if _, ok := seen[child]; !ok {
				seen[child] = struct{}{}
				stack = append(stack, child)
			}
		}
	}
}

// ClearCache drops every memoized value, forcing the next evaluation to
// recompute from scratch. Useful for cross-checking cached results.
func (g *Graph) ClearCache() {
	for _, ns := range g.nodes {
		ns.cache = nil
	}
}

// Step records the completion of one node during evaluation.
type Step struct {
	NodeID   string
	Op       string
	Exact    string // exact value before node-level rounding
	Value    string // node value after node-level rounding
	Cached   bool   // value came from the memo cache
	Rounded  bool   // node declared a rounding policy
	Evidence string // half-even rounding evidence, when Rounded
}

// Eval evaluates id to completion and returns its value along with the
// trace of steps. It is shorthand for NewEvaluator(g, id).Run(0).
func (g *Graph) Eval(id string) (decimal.Value, []Step, error) {
	e := NewEvaluator(g, id)
	v, err := e.Run(0)
	return v, e.Steps(), err
}

type frame struct {
	id    string
	stage int // 0: not started, 1: waiting for A, 2: waiting for B
	left  decimal.Value
}

// Evaluator evaluates one root node on an explicit stack so that a run
// can be suspended after a budget of node completions and later resumed
// exactly where it stopped. Steps accumulate across Run calls.
type Evaluator struct {
	g       *Graph
	stack   []frame
	steps   []Step
	pending decimal.Value
	hasPend bool
	done    bool
	result  decimal.Value
	err     error
}

// NewEvaluator creates an Evaluator for root. The graph's memo cache is
// shared: resumed runs and later evaluations reuse completed nodes.
func NewEvaluator(g *Graph, root string) *Evaluator {
	return &Evaluator{g: g, stack: []frame{{id: root}}}
}

// Steps returns the trace accumulated so far.
func (e *Evaluator) Steps() []Step {
	out := make([]Step, len(e.steps))
	copy(out, e.steps)
	return out
}

// Run evaluates until the root value is known or budget node
// completions have been produced; budget <= 0 means unlimited. It
// returns ErrSuspended when the budget runs out mid-evaluation; call
// Run again to resume. Once finished, Run returns the cached result.
func (e *Evaluator) Run(budget int) (decimal.Value, error) {
	if e.err != nil {
		return decimal.Value{}, e.err
	}
	if e.done {
		return e.result, nil
	}
	count := 0
	for len(e.stack) > 0 {
		top := &e.stack[len(e.stack)-1]
		if e.hasPend {
			// Deliver the just-completed child value to its parent.
			ns := e.g.nodes[top.id]
			if top.stage == 1 {
				top.left = e.pending
				e.hasPend = false
				top.stage = 2
				e.stack = append(e.stack, frame{id: ns.node.B})
				continue
			}
			right := e.pending
			e.hasPend = false
			exact, err := combine(ns.node, top.left, right)
			if err != nil {
				e.err = fmt.Errorf("expr: node %q: %v", top.id, err)
				return decimal.Value{}, e.err
			}
			if err := e.complete(top.id, ns, exact); err != nil {
				e.err = err
				return decimal.Value{}, e.err
			}
			count++
			if budget > 0 && count >= budget && len(e.stack) > 0 {
				return decimal.Value{}, ErrSuspended
			}
			continue
		}
		ns, ok := e.g.nodes[top.id]
		if !ok {
			e.err = fmt.Errorf("expr: undefined node %q", top.id)
			return decimal.Value{}, e.err
		}
		if ns.cache != nil {
			e.emit(top.id, ns, ns.cache, true)
			e.stack = e.stack[:len(e.stack)-1]
			e.pending = ns.cache.value
			e.hasPend = true
			count++
			if budget > 0 && count >= budget && len(e.stack) > 0 {
				return decimal.Value{}, ErrSuspended
			}
			continue
		}
		switch ns.node.Op {
		case OpConst:
			if err := e.complete(top.id, ns, ns.node.Value); err != nil {
				e.err = err
				return decimal.Value{}, e.err
			}
			count++
			if budget > 0 && count >= budget && len(e.stack) > 0 {
				return decimal.Value{}, ErrSuspended
			}
		case OpMul, OpDiv:
			top.stage = 1
			e.stack = append(e.stack, frame{id: ns.node.A})
		default:
			e.err = fmt.Errorf("expr: node %q: unknown op %d", top.id, ns.node.Op)
			return decimal.Value{}, e.err
		}
	}
	e.done = true
	e.result = e.pending
	return e.result, nil
}

// complete finishes the node on top of the stack from its exact value:
// it applies the node's rounding policy, memoizes, emits a Step and
// makes the value pending for the parent frame.
func (e *Evaluator) complete(id string, ns *nodeState, exact decimal.Value) error {
	entry := &cacheEntry{exact: exact, value: exact}
	if ns.node.Scale != nil {
		rounded, ev, err := exact.Round(*ns.node.Scale)
		if err != nil {
			return fmt.Errorf("expr: node %q: %v", id, err)
		}
		entry.value = rounded
		entry.evidence = &ev
	}
	ns.cache = entry
	e.emit(id, ns, entry, false)
	e.stack = e.stack[:len(e.stack)-1]
	e.pending = entry.value
	e.hasPend = true
	return nil
}

func (e *Evaluator) emit(id string, ns *nodeState, entry *cacheEntry, cached bool) {
	step := Step{
		NodeID:  id,
		Op:      ns.node.Op.String(),
		Exact:   entry.exact.String(),
		Value:   entry.value.String(),
		Cached:  cached,
		Rounded: entry.evidence != nil,
	}
	if entry.evidence != nil {
		step.Evidence = entry.evidence.String()
	}
	e.steps = append(e.steps, step)
}

// combine applies a binary op to its two operand values, exactly.
func combine(n Node, a, b decimal.Value) (decimal.Value, error) {
	switch n.Op {
	case OpMul:
		return a.Mul(b), nil
	case OpDiv:
		return a.Div(b)
	}
	return decimal.Value{}, fmt.Errorf("expr: not a binary op: %s", n.Op)
}
