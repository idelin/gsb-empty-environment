package netsec

import "fmt"

// EdgeKind selects the transfer function of a control-flow edge.
type EdgeKind int

const (
	// AssignEdge is x += C (the only supported assignment form).
	AssignEdge EdgeKind = iota
	// GuardEdge is Lo <= x_X - x_Y <= Hi.
	GuardEdge
)

// Edge is a single control-flow transition.
type Edge struct {
	From, To int
	Kind     EdgeKind
	X, Y     int   // variable indices (X for assign; X, Y for guard)
	C        int64 // assign delta
	Lo, Hi   int64 // guard bounds on x_X - x_Y
}

// Access is an array read/write at a node: Array[Index + Offset].
type Access struct {
	ID     int
	Node   int
	Array  string
	Index  int // variable index of the subscript
	Offset int64
}

// Graph is an integer control-flow graph. Variable "0" (index 0) is the
// constant zero and cannot be assigned. Node 0 is the entry node.
type Graph struct {
	vars     []string
	varIdx   map[string]int
	arrays   map[string]int64
	edges    []Edge
	accesses []Access
	nodes    int
	out, in  [][]int
}

// NewGraph returns an empty graph with the zero variable and entry node
// already created.
func NewGraph() *Graph {
	g := &Graph{varIdx: make(map[string]int), arrays: make(map[string]int64)}
	g.Var("0")
	g.Node()
	return g
}

// Entry returns the entry node id.
func (g *Graph) Entry() int { return 0 }

// Var returns the index of a program variable, creating it if needed.
func (g *Graph) Var(name string) int {
	if ix, ok := g.varIdx[name]; ok {
		return ix
	}
	ix := len(g.vars)
	g.vars = append(g.vars, name)
	g.varIdx[name] = ix
	return ix
}

// Array declares an array with the given length (number of elements).
func (g *Graph) Array(name string, length int64) {
	if length <= 0 {
		panic(fmt.Sprintf("netsec: array %q has non-positive length %d", name, length))
	}
	g.arrays[name] = length
}

// Node adds a basic block and returns its id.
func (g *Graph) Node() int {
	id := g.nodes
	g.nodes++
	g.out = append(g.out, nil)
	g.in = append(g.in, nil)
	return id
}

func (g *Graph) edge(e Edge) {
	id := len(g.edges)
	g.edges = append(g.edges, e)
	g.out[e.From] = append(g.out[e.From], id)
	g.in[e.To] = append(g.in[e.To], id)
}

// Assign adds an edge performing v += delta.
func (g *Graph) Assign(from, to int, v string, delta int64) {
	ix := g.Var(v)
	if ix == 0 {
		panic("netsec: cannot assign to the zero variable")
	}
	g.edge(Edge{From: from, To: to, Kind: AssignEdge, X: ix, C: delta})
}

// Guard adds an edge taken only when lo <= x - y <= hi.
// Use -Inf / Inf for absent bounds.
func (g *Graph) Guard(from, to int, x, y string, lo, hi int64) {
	g.edge(Edge{From: from, To: to, Kind: GuardEdge, X: g.Var(x), Y: g.Var(y), Lo: lo, Hi: hi})
}

// Assume adds an edge taken only when lo <= x <= hi (a guard against the
// constant zero). Useful for initialisation facts such as Assume(..., "i", 0, 0).
func (g *Graph) Assume(from, to int, x string, lo, hi int64) {
	g.Guard(from, to, x, "0", lo, hi)
}

// AccessAt registers an array access Array[idx + offset] at a node.
func (g *Graph) AccessAt(node int, array, idx string, offset int64) {
	if _, ok := g.arrays[array]; !ok {
		panic(fmt.Sprintf("netsec: unknown array %q", array))
	}
	g.accesses = append(g.accesses, Access{
		ID: len(g.accesses), Node: node, Array: array,
		Index: g.Var(idx), Offset: offset,
	})
}
