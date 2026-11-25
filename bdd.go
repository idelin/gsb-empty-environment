package closure

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// NodeID is a canonical node in one shared ordered binary decision graph.
// Node 0 is false and node 1 is true.
type NodeID int

const (
	False NodeID = 0
	True  NodeID = 1
)

// Assignment maps variable names to Boolean values.
type Assignment map[string]bool

// Node is an immutable reduced-BDD node.
type Node struct {
	ID   NodeID `json:"id"`
	Var  int    `json:"var"`
	Name string `json:"name"`
	Low  NodeID `json:"low"`
	High NodeID `json:"high"`
}

type nodeKey struct {
	variable int
	low      NodeID
	high     NodeID
}

type applyKey struct {
	op   boolOp
	a, b NodeID
}

type boolOp uint8

const (
	opAnd boolOp = iota
	opOr
	opXor
)

// DefaultNodeBudget is used when a request does not specify a positive budget.
const DefaultNodeBudget = 1 << 16

// ErrBudgetExceeded is returned (possibly wrapped) when the shared graph needs
// more nonterminal nodes than its budget permits.
var ErrBudgetExceeded = errors.New("closure: BDD node budget exceeded")

// BudgetError reports an exhausted node budget.
type BudgetError struct {
	Budget int
	Nodes  int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("%s: budget=%d used=%d", ErrBudgetExceeded, e.Budget, e.Nodes)
}

func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }

// Graph is a shared, reduced, ordered BDD manager. The same unique table is
// used for the pass, rejection, and unbuildable regions.
type Graph struct {
	variables []string
	index     map[string]int
	nodes     []bddNode
	unique    map[nodeKey]NodeID
	cache     map[applyKey]NodeID
	budget    int
}

type bddNode struct {
	variable int
	low      NodeID
	high     NodeID
}

// NewGraph creates a shared manager in the supplied variable order. At most 24
// variables are permitted. A zero or negative budget means unlimited.
func NewGraph(order []string, budget int) (*Graph, error) {
	if len(order) > 24 {
		return nil, fmt.Errorf("closure: at most 24 build flags are allowed, got %d", len(order))
	}
	index := make(map[string]int, len(order))
	for i, name := range order {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("closure: variable %d has an empty name", i)
		}
		if previous, exists := index[name]; exists {
			return nil, fmt.Errorf("closure: duplicate build flag %q at positions %d and %d", name, previous, i)
		}
		index[name] = i
	}
	if budget <= 0 {
		budget = -1
	}
	return &Graph{
		variables: append([]string(nil), order...),
		index:     index,
		nodes:     make([]bddNode, 2, 16),
		unique:    make(map[nodeKey]NodeID),
		cache:     make(map[applyKey]NodeID),
		budget:    budget,
	}, nil
}

// Variables returns the graph's variable order.
func (g *Graph) Variables() []string { return append([]string(nil), g.variables...) }

// Index returns the ordered index of a named flag.
func (g *Graph) Index(name string) (int, bool) {
	i, ok := g.index[name]
	return i, ok
}

// Budget returns the configured nonterminal-node budget (-1 means unlimited).
func (g *Graph) Budget() int { return g.budget }

// NodeCount returns the number of allocated nonterminal nodes.
func (g *Graph) NodeCount() int { return len(g.nodes) - 2 }

// Node returns a copy of node id.
func (g *Graph) Node(id NodeID) (Node, bool) {
	if id < 0 || int(id) >= len(g.nodes) {
		return Node{}, false
	}
	if id == False {
		return Node{ID: id, Name: "false", Low: False, High: False}, true
	}
	if id == True {
		return Node{ID: id, Name: "true", Low: True, High: True}, true
	}
	n := g.nodes[id]
	return Node{ID: id, Var: n.variable, Name: g.variables[n.variable], Low: n.low, High: n.high}, true
}

func (g *Graph) variableIndex(name string) (int, error) {
	i, ok := g.index[name]
	if !ok {
		return -1, fmt.Errorf("closure: build flag %q is absent from variable order %v", name, g.variables)
	}
	return i, nil
}

// mk is the only node constructor. It reduces low==high and interns the rest,
// so construction is already reduced rather than tree-then-compress.
func (g *Graph) mk(variable int, low, high NodeID) (NodeID, error) {
	if low == high {
		return low, nil
	}
	key := nodeKey{variable: variable, low: low, high: high}
	if id, ok := g.unique[key]; ok {
		return id, nil
	}
	if g.budget >= 0 && g.NodeCount() >= g.budget {
		return 0, &BudgetError{Budget: g.budget, Nodes: g.NodeCount() + 1}
	}
	id := NodeID(len(g.nodes))
	g.nodes = append(g.nodes, bddNode{variable: variable, low: low, high: high})
	g.unique[key] = id
	return id, nil
}

// Literal builds the BDD for one signed variable.
func (g *Graph) Literal(lit Literal) (NodeID, error) {
	idx, err := g.variableIndex(lit.Flag)
	if err != nil {
		return 0, err
	}
	if lit.Negated {
		return g.mk(idx, True, False)
	}
	return g.mk(idx, False, True)
}

// Flag builds a positive or negative one-variable BDD.
func (g *Graph) Flag(name string, value bool) (NodeID, error) {
	return g.Literal(Literal{Flag: name, Negated: !value})
}

// Not returns the complement of root.
func (g *Graph) Not(root NodeID) (NodeID, error) {
	return g.apply(opXor, root, True)
}

// And returns a AND b.
func (g *Graph) And(a, b NodeID) (NodeID, error) { return g.apply(opAnd, a, b) }

// Or returns a OR b.
func (g *Graph) Or(a, b NodeID) (NodeID, error) { return g.apply(opOr, a, b) }

// AndMany conjoins roots.
func (g *Graph) AndMany(roots ...NodeID) (NodeID, error) {
	result := True
	var err error
	for _, root := range roots {
		result, err = g.And(result, root)
		if err != nil {
			return 0, err
		}
		if result == False {
			return False, nil
		}
	}
	return result, nil
}

// OrMany disjoins roots.
func (g *Graph) OrMany(roots ...NodeID) (NodeID, error) {
	result := False
	var err error
	for _, root := range roots {
		result, err = g.Or(result, root)
		if err != nil {
			return 0, err
		}
		if result == True {
			return True, nil
		}
	}
	return result, nil
}

func applyTerminal(op boolOp, a, b NodeID) (NodeID, bool) {
	switch op {
	case opAnd:
		if a == False || b == False {
			return False, true
		}
		if a == True {
			return b, true
		}
		if b == True {
			return a, true
		}
	case opOr:
		if a == True || b == True {
			return True, true
		}
		if a == False {
			return b, true
		}
		if b == False {
			return a, true
		}
	case opXor:
		if a == False {
			return b, true
		}
		if b == False {
			return a, true
		}
		if a == True {
			if b == True {
				return False, true
			}
			return 0, false
		}
		if b == True {
			return 0, false
		}
	}
	return 0, false
}

func (g *Graph) apply(op boolOp, a, b NodeID) (NodeID, error) {
	if result, ok := applyTerminal(op, a, b); ok {
		return result, nil
	}
	// XOR with true is complement and has a compact specialized recursion.
	if op == opXor && (a == True || b == True) {
		root := a
		other := b
		if a == True {
			root = b
			other = a
		}
		key := applyKey{op: op, a: root, b: other}
		if cached, ok := g.cache[key]; ok {
			return cached, nil
		}
		n := g.nodes[root]
		low, err := g.apply(op, n.low, True)
		if err != nil {
			return 0, err
		}
		high, err := g.apply(op, n.high, True)
		if err != nil {
			return 0, err
		}
		result, err := g.mk(n.variable, low, high)
		if err != nil {
			return 0, err
		}
		g.cache[key] = result
		return result, nil
	}

	key := applyKey{op: op, a: a, b: b}
	if cached, ok := g.cache[key]; ok {
		return cached, nil
	}

	na := g.nodes[a]
	nb := g.nodes[b]
	var variable int
	var lowA, highA, lowB, highB NodeID
	switch {
	case na.variable == nb.variable:
		variable = na.variable
		lowA, highA = na.low, na.high
		lowB, highB = nb.low, nb.high
	case na.variable < nb.variable:
		variable = na.variable
		lowA, highA = na.low, na.high
		lowB, highB = b, b
	default:
		variable = nb.variable
		lowA, highA = a, a
		lowB, highB = nb.low, nb.high
	}

	low, err := g.apply(op, lowA, lowB)
	if err != nil {
		return 0, err
	}
	high, err := g.apply(op, highA, highB)
	if err != nil {
		return 0, err
	}
	result, err := g.mk(variable, low, high)
	if err != nil {
		return 0, err
	}
	g.cache[key] = result
	return result, nil
}

// Eval evaluates root under values. Missing variables default to false.
func (g *Graph) Eval(root NodeID, values map[string]bool) bool {
	for root >= 2 {
		n := g.nodes[root]
		if values[g.variables[n.variable]] {
			root = n.high
		} else {
			root = n.low
		}
	}
	return root == True
}

// Satisfiable reports whether root has at least one satisfying assignment.
func (g *Graph) Satisfiable(root NodeID) bool { return root != False }

// Verify checks the manager's ordered and reduced invariants for every
// allocated node. It is useful in tests and host-side diagnostics.
func (g *Graph) Verify() error {
	for id := NodeID(2); int(id) < len(g.nodes); id++ {
		n := g.nodes[id]
		if n.low == n.high {
			return fmt.Errorf("closure: node %d is redundant", id)
		}
		for child, label := range map[NodeID]string{n.low: "low", n.high: "high"} {
			if child < 0 || int(child) >= len(g.nodes) {
				return fmt.Errorf("closure: node %d has invalid %s child %d", id, label, child)
			}
			if child >= 2 && g.nodes[child].variable <= n.variable {
				return fmt.Errorf("closure: node %d var %d has %s child with var %d", id, n.variable, label, g.nodes[child].variable)
			}
		}
		key := nodeKey{variable: n.variable, low: n.low, high: n.high}
		if canonical := g.unique[key]; canonical != id {
			return fmt.Errorf("closure: node %d is not canonical (unique table has %d)", id, canonical)
		}
	}
	return nil
}

// OneAssignment returns one satisfying assignment and true. Don't-care
// variables are present with false values to make evidence replay explicit.
func (g *Graph) OneAssignment(root NodeID) (Assignment, bool) {
	if root == False {
		return nil, false
	}
	values := make(Assignment, len(g.variables))
	for _, name := range g.variables {
		values[name] = false
	}
	for root >= 2 {
		n := g.nodes[root]
		name := g.variables[n.variable]
		if n.high != False {
			values[name] = true
			root = n.high
		} else {
			values[name] = false
			root = n.low
		}
	}
	if root != True {
		return nil, false
	}
	return values, true
}

// AllAssignments enumerates all assignments satisfying root. It is intended
// for cross-checks with at most six flags.
func (g *Graph) AllAssignments(root NodeID) []Assignment {
	var out []Assignment
	current := make(Assignment, len(g.variables))
	for _, name := range g.variables {
		current[name] = false
	}
	var walk func(node NodeID, depth int)
	walk = func(node NodeID, depth int) {
		if node == False {
			return
		}
		if depth == len(g.variables) {
			if node == True {
				assignment := make(Assignment, len(current))
				for k, v := range current {
					assignment[k] = v
				}
				out = append(out, assignment)
			}
			return
		}
		name := g.variables[depth]
		if node >= 2 && g.nodes[node].variable == depth {
			n := g.nodes[node]
			current[name] = false
			walk(n.low, depth+1)
			current[name] = true
			walk(n.high, depth+1)
			current[name] = false
			return
		}
		// The node skips this ordered variable, so both values are possible.
		current[name] = false
		walk(node, depth+1)
		current[name] = true
		walk(node, depth+1)
		current[name] = false
	}
	walk(root, 0)
	sort.Slice(out, func(i, j int) bool {
		for _, name := range g.variables {
			if out[i][name] != out[j][name] {
				return !out[i][name] && out[j][name]
			}
		}
		return false
	})
	return out
}

// Dot renders the shared graph reachable from roots in Graphviz format.
func (g *Graph) Dot(roots ...NodeID) string {
	seen := map[NodeID]bool{False: true, True: true}
	var visit func(NodeID)
	visit = func(id NodeID) {
		if id < 2 || seen[id] {
			return
		}
		seen[id] = true
		n := g.nodes[id]
		visit(n.low)
		visit(n.high)
	}
	for _, root := range roots {
		visit(root)
	}
	var b strings.Builder
	b.WriteString("digraph ROBDD {\n  rankdir=TB;\n  node [shape=box];\n")
	b.WriteString("  0 [label=\"0\",shape=box,style=filled,fillcolor=\"#f4cccc\"];\n")
	b.WriteString("  1 [label=\"1\",shape=box,style=filled,fillcolor=\"#d9ead3\"];\n")
	ids := make([]NodeID, 0, len(seen))
	for id := range seen {
		if id >= 2 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		n := g.nodes[id]
		fmt.Fprintf(&b, "  %d [label=%q];\n", id, g.variables[n.variable])
		fmt.Fprintf(&b, "  %d -> %d [style=dashed];\n", id, n.low)
		fmt.Fprintf(&b, "  %d -> %d;\n", id, n.high)
	}
	b.WriteString("}\n")
	return b.String()
}
