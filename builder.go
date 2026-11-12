package netsec

import "sort"

// Builder assembles a Graph with chained calls. It keeps node insertion
// idempotent so callers can reference tables and functions in any order.
type Builder struct {
	nodes map[string]Node
	edges []Edge
	roots []Root
}

// NewBuilder creates an empty graph builder.
func NewBuilder() *Builder {
	return &Builder{nodes: make(map[string]Node)}
}

// Table registers a table node. Builtin may be empty.
func (b *Builder) Table(id, builtin string) *Builder {
	b.nodes[id] = Node{ID: id, Kind: KindTable, Builtin: builtin}
	return b
}

// Function registers a function node. Builtin may be empty.
func (b *Builder) Function(id, builtin string) *Builder {
	b.nodes[id] = Node{ID: id, Kind: KindFunction, Builtin: builtin}
	return b
}

// Field adds a table field edge from[field] = to.
func (b *Builder) Field(from, field, to string) *Builder {
	b.edges = append(b.edges, Edge{From: from, To: to, Kind: EdgeField, Key: field})
	return b
}

// Metatable adds an edge from a value to its metatable.
func (b *Builder) Metatable(from, metatable string) *Builder {
	b.edges = append(b.edges, Edge{From: from, To: metatable, Kind: EdgeMetatable})
	return b
}

// Index adds an __index edge from a metatable to its __index value.
func (b *Builder) Index(metatable, target string) *Builder {
	b.edges = append(b.edges, Edge{From: metatable, To: target, Kind: EdgeIndex})
	return b
}

// Upvalue adds a 1-based upvalue edge from a function to its upvalue.
// name may be empty when the slot has no recorded name.
func (b *Builder) Upvalue(fn string, slot int, name, target string) *Builder {
	b.edges = append(b.edges, Edge{
		From:  fn,
		To:    target,
		Kind:  EdgeUpvalue,
		Key:   name,
		Index: slot,
	})
	return b
}

// Root marks a node as an audit root.
func (b *Builder) Root(id, label string) *Builder {
	b.roots = append(b.roots, Root{ID: id, Label: label})
	return b
}

// Build materializes the Graph. Nodes are emitted in ID order for stable
// diffs even though Audit does not depend on insertion order.
func (b *Builder) Build() *Graph {
	ids := make([]string, 0, len(b.nodes))
	for id := range b.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := make([]Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, b.nodes[id])
	}
	return &Graph{
		Nodes: nodes,
		Edges: append([]Edge(nil), b.edges...),
		Roots: append([]Root(nil), b.roots...),
	}
}
