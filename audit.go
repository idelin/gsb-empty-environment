package netsec

import (
	"context"
	"fmt"
	"sort"
)

// Reachable describes one node reached during the audit.
type Reachable struct {
	// Node is the reached node ID.
	Node string
	// Kind is the node kind.
	Kind NodeKind
	// Builtin is the node's builtin identifier, possibly empty.
	Builtin string
	// Root is the label of the root from which the shortest path starts.
	Root string
	// Depth is the number of edges from that root.
	Depth int
}

// Step is one hop of a reachability path. The initial step carries the
// root label in Root and has a zero Edge.
type Step struct {
	// Node is the node at this step.
	Node string
	// Root is set only on the initial step.
	Root string
	// Edge is the kind of edge used to arrive; zero on the root step.
	Edge EdgeKind
	// Key is the field name or upvalue name.
	Key string
	// Index is the 1-based upvalue slot for EdgeUpvalue.
	Index int
}

// Finding reports one dangerous sink and a shortest path to it.
type Finding struct {
	// Sink is the dangerous capability category.
	Sink SinkKind
	// Node is the sink node ID.
	Node string
	// Builtin is the sink's builtin identifier when known.
	Builtin string
	// Path is the shortest path from a root to the sink.
	Path []Step
}

// Report is the result of an audit.
type Report struct {
	// Reachable lists every node reachable from any root.
	Reachable []Reachable
	// Findings lists every reachable dangerous sink with a shortest path.
	Findings []Finding
}

// Safe reports whether the sandbox exposes no dangerous capability.
func (r *Report) Safe() bool {
	return r != nil && len(r.Findings) == 0
}

// PathString renders the finding path in a Lua-flavoured access notation.
func (f *Finding) PathString() string {
	if f == nil || len(f.Path) == 0 {
		return ""
	}
	out := f.Path[0].render()
	for i := 1; i < len(f.Path); i++ {
		out += " " + f.Path[i].render()
	}
	return out
}

func (s Step) render() string {
	if s.Root != "" {
		return fmt.Sprintf("%s(%s)", s.Root, s.Node)
	}
	switch s.Edge {
	case EdgeField:
		return fmt.Sprintf(".%s -> %s", s.Key, s.Node)
	case EdgeMetatable:
		return fmt.Sprintf("[metatable] -> %s", s.Node)
	case EdgeIndex:
		return fmt.Sprintf("[__index] -> %s", s.Node)
	case EdgeUpvalue:
		if s.Key != "" {
			return fmt.Sprintf("[upvalue#%d %s] -> %s", s.Index, s.Key, s.Node)
		}
		return fmt.Sprintf("[upvalue#%d] -> %s", s.Index, s.Node)
	default:
		return "-> " + s.Node
	}
}

func (f *Finding) String() string {
	return fmt.Sprintf("%s reachable at node %q: %s", f.Sink, f.Node, f.PathString())
}

// bfsState records how a node was first reached.
type bfsState struct {
	depth int
	root  string
	role  libRole
	from  string
	edge  Edge
}

// Audit computes node reachability from the configured roots over the Lua
// object graph and reports every dangerous sink with a shortest path.
//
// Multi-source BFS is used, so each node is attributed to the root that
// reaches it with the fewest edges; ties are broken by root and adjacency
// order, making the output deterministic.
func Audit(ctx context.Context, g *Graph) (*Report, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}

	nodes := make(map[string]Node, len(g.Nodes))
	adj := make(map[string][]Edge, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		adj[n.ID] = nil
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	for id := range adj {
		sort.Slice(adj[id], func(i, j int) bool {
			return edgeLess(adj[id][i], adj[id][j])
		})
	}

	visited := make(map[string]*bfsState, len(g.Nodes))
	var findings []Finding
	queue := make([]string, 0, len(g.Nodes))

	recordSink := func(n Node, sink SinkKind) {
		findings = append(findings, Finding{
			Sink:    sink,
			Node:    n.ID,
			Builtin: n.Builtin,
			Path:    buildPath(n.ID, g.Roots, visited),
		})
	}

	for _, r := range g.Roots {
		if _, ok := visited[r.ID]; ok {
			continue
		}
		n := nodes[r.ID]
		st := &bfsState{depth: 0, root: r.Label, role: roleForBuiltin(n.Builtin)}
		visited[r.ID] = st
		queue = append(queue, r.ID)
		if sink, ok := classifyBuiltin(n.Builtin); ok {
			recordSink(n, sink)
		}
	}

	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := queue[head]
		ust := visited[u]
		for _, e := range adj[u] {
			vn := nodes[e.To]
			if _, seen := visited[e.To]; seen {
				continue
			}

			role := nextRole(vn, e, ust.role)
			visited[e.To] = &bfsState{
				depth: ust.depth + 1,
				root:  ust.root,
				role:  role,
				from:  u,
				edge:  e,
			}
			queue = append(queue, e.To)

			if sink, ok := classifyBuiltin(vn.Builtin); ok {
				recordSink(vn, sink)
			} else if sink, ok := classifyStructural(e, ust.role); ok {
				recordSink(vn, sink)
			}
		}
	}

	report := &Report{
		Reachable: make([]Reachable, 0, len(queue)),
		Findings:  finalizeFindings(findings),
	}
	for _, id := range queue {
		st := visited[id]
		n := nodes[id]
		report.Reachable = append(report.Reachable, Reachable{
			Node:    id,
			Kind:    n.Kind,
			Builtin: n.Builtin,
			Root:    st.root,
			Depth:   st.depth,
		})
	}
	sort.Slice(report.Reachable, func(i, j int) bool {
		return report.Reachable[i].Node < report.Reachable[j].Node
	})
	return report, nil
}

// nextRole resolves the library role of a freshly discovered node. An
// explicit builtin marker wins; otherwise entering a library table by its
// field name establishes the role, which is inherited through further
// hops such as __index forwarding.
func nextRole(n Node, e Edge, parent libRole) libRole {
	if r := roleForBuiltin(n.Builtin); r != roleNone {
		return r
	}
	if e.Kind == EdgeField {
		if r := roleForField(e.Key); r != roleNone {
			return r
		}
	}
	return parent
}

func buildPath(id string, roots []Root, visited map[string]*bfsState) []Step {
	// Walk the parent chain from the sink back to its root.
	chain := make([]string, 0, visited[id].depth+1)
	for cur := id; ; {
		chain = append(chain, cur)
		if visited[cur].depth == 0 {
			break
		}
		cur = visited[cur].from
	}

	path := make([]Step, len(chain))
	rootID := chain[len(chain)-1]
	path[0] = Step{Node: rootID, Root: visited[rootID].root}
	for i := len(chain) - 2; i >= 0; i-- {
		nodeID := chain[i]
		st := visited[nodeID]
		path[len(chain)-1-i] = Step{
			Node:  nodeID,
			Edge:  st.edge.Kind,
			Key:   st.edge.Key,
			Index: st.edge.Index,
		}
	}
	return path
}

func finalizeFindings(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Sink != findings[j].Sink {
			return findings[i].Sink < findings[j].Sink
		}
		return findings[i].Node < findings[j].Node
	})
	return findings
}

func edgeLess(a, b Edge) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Index != b.Index {
		return a.Index < b.Index
	}
	return a.To < b.To
}

func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
