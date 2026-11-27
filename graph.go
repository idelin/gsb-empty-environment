package minijit

import (
	"fmt"
	"strings"
)

// GraphNode is one canonicalized heap node.
type GraphNode struct {
	Class  string
	Fields []GraphField
}

// GraphField is a named field with a primitive value or a canonical node id.
type GraphField struct {
	Name      string
	Primitive Value
	IsRef     bool
	RefID     int
}

// Graph is a canonicalized reachable object graph.
type Graph struct {
	Root    Value
	RootRef int
	HasRoot bool
	Nodes   []GraphNode
}

type graphBuilder struct {
	nodes []GraphNode
	index map[*Object]int
}

// CanonicalGraph performs a deterministic DFS from root, assigning canonical
// ids to every reachable object so that two independent executions producing
// isomorphic heaps compare equal despite different pointers and allocation
// ids.
func CanonicalGraph(root Value) *Graph {
	gb := &graphBuilder{index: map[*Object]int{}}
	g := &Graph{Root: canonicalPrimitive(root)}
	if root.IsObject() {
		g.HasRoot = true
		g.RootRef = gb.visit(root.AsObject())
	}
	g.Nodes = gb.nodes
	return g
}

func (gb *graphBuilder) visit(o *Object) int {
	if id, ok := gb.index[o]; ok {
		return id
	}
	id := len(gb.nodes)
	gb.index[o] = id
	gb.nodes = append(gb.nodes, GraphNode{Class: o.Class()})
	fields := make([]GraphField, 0, len(o.fields))
	for _, f := range o.fields {
		gf := GraphField{Name: f.Name, Primitive: canonicalPrimitive(f.Value)}
		if f.Value.IsObject() && f.Value.AsObject() != nil {
			gf.IsRef = true
			gf.RefID = gb.visit(f.Value.AsObject())
		}
		fields = append(fields, gf)
	}
	gb.nodes[id].Fields = fields
	return id
}

func canonicalPrimitive(v Value) Value {
	if v.IsFunction() {
		if v.AsFunction() == nil {
			return StringValue("<func:nil>")
		}
		return StringValue("<func:" + v.AsFunction().Name + ">")
	}
	return v
}

// Equals compares two canonical graphs node-by-node.
func (g *Graph) Equals(other *Graph) bool {
	if g == nil || other == nil {
		return g == other
	}
	if g.HasRoot != other.HasRoot || g.RootRef != other.RootRef {
		return false
	}
	if !g.HasRoot && !StrictEqual(g.Root, other.Root) {
		return false
	}
	if len(g.Nodes) != len(other.Nodes) {
		return false
	}
	for i := range g.Nodes {
		a, b := g.Nodes[i], other.Nodes[i]
		if a.Class != b.Class || len(a.Fields) != len(b.Fields) {
			return false
		}
		for j := range a.Fields {
			af, bf := a.Fields[j], b.Fields[j]
			if af.Name != bf.Name || af.IsRef != bf.IsRef {
				return false
			}
			if af.IsRef {
				if af.RefID != bf.RefID {
					return false
				}
				continue
			}
			if !StrictEqual(af.Primitive, bf.Primitive) {
				return false
			}
		}
	}
	return true
}

// ResultsEquivalent reports whether two terminal values and their reachable
// heap graphs are structurally identical.
func ResultsEquivalent(a, b Value) bool {
	return CanonicalGraph(a).Equals(CanonicalGraph(b))
}

// DebugString renders a canonical graph for diagnostics.
func (g *Graph) DebugString() string {
	var sb strings.Builder
	if g.HasRoot {
		fmt.Fprintf(&sb, "root -> node#%d\n", g.RootRef)
	} else {
		fmt.Fprintf(&sb, "root = %s\n", g.Root.String())
	}
	for id, n := range g.Nodes {
		fmt.Fprintf(&sb, "node#%d [%s]", id, n.Class)
		for _, f := range n.Fields {
			if f.IsRef {
				fmt.Fprintf(&sb, " %s->node#%d", f.Name, f.RefID)
			} else {
				fmt.Fprintf(&sb, " %s=%s", f.Name, f.Primitive.String())
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
