// Package graphcanon computes exact canonical encodings for small rooted,
// directed, node-labeled multigraphs (at most 8 nodes).
//
// Canonicalization is done by exact enumeration of every root-fixing
// numbering (at most 7! = 5040 for 8 nodes), taking the lexicographically
// minimal encoding. It never uses recursive hashing with cycle truncation:
// every numbering is fully materialized and compared. An independent
// permutation checker (see verify.go, which shares no logic with the
// canonicalizer) can exhaustively validate any claimed isomorphism proof.
package graphcanon

import "errors"

const (
	// MaxNodes is the largest supported graph size. Eight nodes keep exact
	// enumeration of all numberings cheap (7! = 5040).
	MaxNodes = 8
	// MaxMultiplicity is the largest supported parallel-edge multiplicity.
	MaxMultiplicity = 255
)

var (
	ErrBadNodeCount   = errors.New("graphcanon: node count must be between 1 and 8")
	ErrNodeOutOfRange = errors.New("graphcanon: node index out of range")
	ErrLabelCount     = errors.New("graphcanon: labels length must equal node count")
	ErrNoSuchEdge     = errors.New("graphcanon: no edge to remove")
	ErrTooManyEdges   = errors.New("graphcanon: parallel edge multiplicity exceeds 255")
)

// Graph is a rooted, directed, node-labeled multigraph with at most
// MaxNodes nodes.
//
// Node indices are stable identities: mutations never renumber, add, or
// remove nodes, so an index refers to the same node across all versions.
// Every successful mutation call increments the version counter, even if
// it leaves the structure unchanged (e.g. setting an unchanged label);
// the canonicalization service relies on this to detect staleness.
type Graph struct {
	n       int
	root    int
	labels  []string
	adj     [][]int // adj[i][j] = multiplicity of the edge i -> j
	version uint64
}

// NewGraph creates a graph with n nodes (1..MaxNodes), the given root, and
// per-node labels. The labels slice is copied.
func NewGraph(n, root int, labels []string) (*Graph, error) {
	if n < 1 || n > MaxNodes {
		return nil, ErrBadNodeCount
	}
	if root < 0 || root >= n {
		return nil, ErrNodeOutOfRange
	}
	if len(labels) != n {
		return nil, ErrLabelCount
	}
	adj := make([][]int, n)
	for i := range adj {
		adj[i] = make([]int, n)
	}
	cp := make([]string, n)
	copy(cp, labels)
	return &Graph{n: n, root: root, labels: cp, adj: adj}, nil
}

// N returns the number of nodes.
func (g *Graph) N() int { return g.n }

// Root returns the root node index.
func (g *Graph) Root() int { return g.root }

// Version returns the mutation counter. It starts at 0 and increments on
// every successful mutation call.
func (g *Graph) Version() uint64 { return g.version }

// Label returns the label of node i.
func (g *Graph) Label(i int) string { return g.labels[i] }

// Multiplicity returns the number of parallel edges from -> to.
func (g *Graph) Multiplicity(from, to int) int { return g.adj[from][to] }

// AddEdge adds one parallel edge from -> to, incrementing its multiplicity.
// Edge insertion order is irrelevant to the graph's identity: the graph is
// stored as a multiplicity matrix, not an edge list.
func (g *Graph) AddEdge(from, to int) error {
	if from < 0 || from >= g.n || to < 0 || to >= g.n {
		return ErrNodeOutOfRange
	}
	if g.adj[from][to] >= MaxMultiplicity {
		return ErrTooManyEdges
	}
	g.adj[from][to]++
	g.version++
	return nil
}

// RemoveEdge removes one parallel edge from -> to.
func (g *Graph) RemoveEdge(from, to int) error {
	if from < 0 || from >= g.n || to < 0 || to >= g.n {
		return ErrNodeOutOfRange
	}
	if g.adj[from][to] == 0 {
		return ErrNoSuchEdge
	}
	g.adj[from][to]--
	g.version++
	return nil
}

// SetLabel sets the label of a node. The version increments even if the
// label is unchanged, so callers can use it as a cheap "touch".
func (g *Graph) SetLabel(node int, label string) error {
	if node < 0 || node >= g.n {
		return ErrNodeOutOfRange
	}
	g.labels[node] = label
	g.version++
	return nil
}

// Permute returns a new graph in which node i of g has identity perm[i].
// The result is isomorphic to g by construction; perm witnesses it.
func Permute(g *Graph, perm []int) (*Graph, error) {
	n := g.n
	if len(perm) != n {
		return nil, ErrNodeOutOfRange
	}
	seen := make([]bool, n)
	for _, p := range perm {
		if p < 0 || p >= n || seen[p] {
			return nil, ErrNodeOutOfRange
		}
		seen[p] = true
	}
	labels := make([]string, n)
	for i := 0; i < n; i++ {
		labels[perm[i]] = g.labels[i]
	}
	out, err := NewGraph(n, perm[g.root], labels)
	if err != nil {
		return nil, err
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			out.adj[perm[i]][perm[j]] = g.adj[i][j]
		}
	}
	return out, nil
}
