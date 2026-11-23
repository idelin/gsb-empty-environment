package envguard

import "fmt"

// MaxRuneLen is the largest representable output length. Lengths are tracked
// in int64; the policy ceiling of 2^44 is enforced explicitly.
const MaxRuneLen = int64(1) << 44

// NodeID identifies a hash-consed rope node inside a Graph.
type NodeID int

type nodeKind int

const (
	nLiteral nodeKind = iota
	nVar
	nConcat
	nLoop
	nCond
	nFold
	nSlice
	nSanitize
)

type node struct {
	kind nodeKind
	len  int64

	// literal / var
	text   string
	origin int

	// concat
	parts []NodeID
	// loop
	body  NodeID
	count int64
	// cond
	child    NodeID
	childB   NodeID
	selector *Origin
	// slice
	lo, hi int64
	// fold, slice, cond, sanitize share child
	san *Sanitizer

	// Identity sets over the whole subtree.
	allVars  bset // every variable origin referenced
	content  bset // every character-content origin
	dataCtl  bset // control caused by variable-shaped data
	branchCt bset // control introduced by condition selectors
	control  bset // dataCtl union branchCt

	// checkpoints for remove-type sanitizer offset mapping
	blocks []sanBlock
}

// sanBlock maps an output block start to the corresponding input offset.
type sanBlock struct {
	outStart int64
	inStart  int64
}

const sanBlockSize = int64(256)

// Graph is the shared, hash-consed concatenation DAG plus the host registry.
// Nodes are immutable once interned, so sub-ropes can be shared freely by
// different environment entries and tenants.
type Graph struct {
	reg   *registry
	nodes []*node

	litKey   map[string]NodeID
	varKey   map[int]NodeID
	conKey   map[string]NodeID
	loopKey  map[loopKey]NodeID
	condKey  map[condKey]NodeID
	foldKey  map[NodeID]NodeID
	sliceKey map[sliceKey]NodeID
	sanKey   map[sanKey]NodeID
}

type loopKey struct {
	body  NodeID
	count int64
}

type condKey struct {
	selector int
	a, b     NodeID
}

type sliceKey struct {
	child  NodeID
	lo, hi int64
}

type sanKey struct {
	child NodeID
	name  string
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	r := newRegistry()
	return &Graph{
		reg:      r,
		nodes:    []*node{nil},
		litKey:   map[string]NodeID{},
		varKey:   map[int]NodeID{},
		conKey:   map[string]NodeID{},
		loopKey:  map[loopKey]NodeID{},
		condKey:  map[condKey]NodeID{},
		foldKey:  map[NodeID]NodeID{},
		sliceKey: map[sliceKey]NodeID{},
		sanKey:   map[sanKey]NodeID{},
	}
}

// Bind registers a tenant input after validating it against its language.
func (g *Graph) Bind(in Input) error {
	_, err := g.reg.bind(in)
	return err
}

// DeclareSanitizer registers a host-trusted sanitizer relation.
func (g *Graph) DeclareSanitizer(s *Sanitizer) error {
	return g.reg.registerSanitizer(s)
}

func (g *Graph) intern(n *node) NodeID {
	g.growSets(n)
	id := NodeID(len(g.nodes))
	g.nodes = append(g.nodes, n)
	return id
}

func (g *Graph) growSets(n *node) {
	width := (len(g.reg.origins) + 63) / 64
	grow := func(s bset) bset {
		for len(s.w) < width {
			s.w = append(s.w, 0)
		}
		return s
	}
	n.allVars = grow(n.allVars)
	n.content = grow(n.content)
	n.control = grow(n.control)
	n.dataCtl = grow(n.dataCtl)
	n.branchCt = grow(n.branchCt)
}

func (g *Graph) accumulate(dst, src *node) *node {
	dst.allVars = dst.allVars.union(src.allVars)
	dst.content = dst.content.union(src.content)
	dst.dataCtl = dst.dataCtl.union(src.dataCtl)
	dst.branchCt = dst.branchCt.union(src.branchCt)
	dst.control = dst.dataCtl.union(dst.branchCt)
	return dst
}

func (g *Graph) copySets(dst, src *node) {
	dst.allVars = src.allVars
	dst.content = src.content
	dst.control = src.control
	dst.dataCtl = src.dataCtl
	dst.branchCt = src.branchCt
}

func addLen(a, b int64) int64 {
	s := a + b
	if s > MaxRuneLen || s < a {
		panic(fmt.Sprintf("envguard: rope exceeds max length %d", MaxRuneLen))
	}
	return s
}

func mulOverflows(part, count int64) bool {
	if part == 0 {
		return false
	}
	return count > MaxRuneLen/part
}
