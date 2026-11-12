package netsec

import "fmt"

// ObjectID identifies a directory object in a Graph.
type ObjectID uint32

// InvalidObject is a sentinel meaning "no object".
const InvalidObject = ObjectID(^uint32(0))

// Dir describes a single directory object.
type Dir struct {
	// Name is a human-readable label used in reports and witnesses.
	Name string
	// Parent is the parent directory object. A self-loop (Parent ==
	// the object's own ID) marks a global filesystem root: parent
	// traversal stops there.
	Parent ObjectID
}

// Graph is a static directory object graph. Objects are identified by
// their index in the slice passed to NewGraph.
type Graph struct {
	names    []string
	parent   []ObjectID
	children [][]ObjectID
}

// NewGraph builds a Graph from dir descriptions. It validates that
// every parent reference is in range and that walking parents from
// any object terminates at a self-loop (a global root).
func NewGraph(dirs []Dir) (*Graph, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("netsec: graph must contain at least one object")
	}
	g := &Graph{
		names:    make([]string, len(dirs)),
		parent:   make([]ObjectID, len(dirs)),
		children: make([][]ObjectID, len(dirs)),
	}
	for i, d := range dirs {
		if d.Parent == InvalidObject || d.Parent >= ObjectID(len(dirs)) {
			return nil, fmt.Errorf("netsec: object %d (%q): parent %d out of range", i, d.Name, d.Parent)
		}
		g.names[i] = d.Name
		g.parent[i] = d.Parent
		g.children[d.Parent] = append(g.children[d.Parent], ObjectID(i))
	}
	for i := range dirs {
		if _, ok := g.globalRoot(ObjectID(i)); !ok {
			return nil, fmt.Errorf("netsec: object %d (%q): parent chain does not terminate at a global root", i, g.names[i])
		}
	}
	return g, nil
}

// NumObjects returns the number of directory objects in the graph.
func (g *Graph) NumObjects() int { return len(g.parent) }

// Name returns the label of object o, or a placeholder if o is out of
// range.
func (g *Graph) Name(o ObjectID) string {
	if !g.Valid(o) {
		return fmt.Sprintf("<invalid:%d>", o)
	}
	if g.names[o] == "" {
		return fmt.Sprintf("<obj:%d>", o)
	}
	return g.names[o]
}

// Parent returns the parent of object o. The parent of a global root
// is itself.
func (g *Graph) Parent(o ObjectID) ObjectID { return g.parent[o] }

// Children returns the direct children of object o.
func (g *Graph) Children(o ObjectID) []ObjectID { return g.children[o] }

// Valid reports whether o is a valid object ID in the graph.
func (g *Graph) Valid(o ObjectID) bool { return o < ObjectID(len(g.parent)) }

// globalRoot follows parent links from o until a self-loop. It returns
// false if the walk does not terminate within NumObjects+1 steps, which
// indicates a parent cycle that is not a global root.
func (g *Graph) globalRoot(o ObjectID) (ObjectID, bool) {
	for i := 0; i <= len(g.parent); i++ {
		p := g.parent[o]
		if p == o {
			return o, true
		}
		o = p
	}
	return InvalidObject, false
}

// insideSet computes, for every object, whether it lies inside the
// subtree rooted at root (root itself counts as inside).
func (g *Graph) insideSet(root ObjectID) []bool {
	n := len(g.parent)
	inside := make([]bool, n)
	const (
		unknown = 0
		yes     = 1
		no      = 2
	)
	memo := make([]int8, n)
	for i := 0; i < n; i++ {
		var chain []ObjectID
		o := ObjectID(i)
		res := false
		for {
			if memo[o] != unknown {
				res = memo[o] == yes
				break
			}
			if o == root {
				res = true
				break
			}
			p := g.parent[o]
			if p == o {
				res = false
				break
			}
			chain = append(chain, o)
			o = p
		}
		mark := int8(no)
		if res {
			mark = yes
		}
		for _, c := range chain {
			memo[c] = mark
		}
		inside[i] = res
	}
	return inside
}
