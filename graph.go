package netsec

// NodeKind identifies the concrete Lua value kind represented by a Node.
// Only values that can carry identity or further edges (tables and
// functions) need to appear in the graph. Scalars such as numbers, booleans
// and strings are leaves and are therefore not modeled.
type NodeKind int

const (
	// KindTable represents a Lua table.
	KindTable NodeKind = iota + 1
	// KindFunction represents a Lua function (Lua or Go backed).
	KindFunction
)

func (k NodeKind) String() string {
	switch k {
	case KindTable:
		return "table"
	case KindFunction:
		return "function"
	default:
		return "unknown"
	}
}

// EdgeKind describes how one Lua value leads to another.
type EdgeKind int

const (
	// EdgeField is a table field access: From[Key] == To.
	EdgeField EdgeKind = iota + 1
	// EdgeMetatable links a value to its metatable: getmetatable(From) == To.
	EdgeMetatable
	// EdgeIndex links a metatable to its __index value. It is the semantic
	// form of the common Meta[Key("__index")] lookup; either representation
	// is traversed, EdgeIndex only makes the path output explicit.
	EdgeIndex
	// EdgeUpvalue links a function to one of its upvalues. Index is the
	// 1-based upvalue slot and Key optionally records the upvalue name.
	EdgeUpvalue
)

func (k EdgeKind) String() string {
	switch k {
	case EdgeField:
		return "field"
	case EdgeMetatable:
		return "metatable"
	case EdgeIndex:
		return "index"
	case EdgeUpvalue:
		return "upvalue"
	default:
		return "unknown"
	}
}

// Node is a vertex of the Lua global object graph.
type Node struct {
	// ID is the unique, stable identity of the value inside the graph.
	ID string
	// Kind is either KindTable or KindFunction.
	Kind NodeKind
	// Builtin records the well-known builtin identifier of the value, for
	// example "debug", "io.open", "os.execute" or "string.dump". It may be
	// empty for host-defined tables and functions.
	Builtin string
}

// Edge is a directed reachability edge between two nodes.
type Edge struct {
	// From is the source node ID.
	From string
	// To is the destination node ID.
	To string
	// Kind selects the access semantics.
	Kind EdgeKind
	// Key is the field name (EdgeField) or upvalue name (EdgeUpvalue).
	Key string
	// Index is the 1-based upvalue slot for EdgeUpvalue; ignored otherwise.
	Index int
}

// Root is an audit starting point. A sandbox audit always starts from the
// sandbox environment table and additionally from every shared metatable
// that is reachable by sandboxed code regardless of the environment, such
// as the string and number metatables.
type Root struct {
	// ID is the node ID of the root value.
	ID string
	// Label names the root for path output, for example RootSandbox.
	Label string
}

// Standard root labels.
const (
	RootSandbox          = "sandbox"
	RootStringMetatable  = "metatable:string"
	RootNumberMetatable  = "metatable:number"
	RootBooleanMetatable = "metatable:boolean"
)

// Graph is the full object graph extracted from the embedded Lua state:
// nodes, the field/metatable/__index/upvalue edges between them and the
// roots the audit must start from.
type Graph struct {
	Nodes []Node
	Edges []Edge
	Roots []Root
}

// Validate checks structural invariants of the graph and returns the first
// problem encountered. Audit calls Validate before traversing.
func (g *Graph) Validate() error {
	if g == nil {
		return errf("audit: nil graph")
	}
	if len(g.Nodes) == 0 {
		return errf("audit: graph has no nodes")
	}
	if len(g.Roots) == 0 {
		return errf("audit: graph has no roots")
	}

	seen := make(map[string]Node, len(g.Nodes))
	for i, n := range g.Nodes {
		if n.ID == "" {
			return errf("audit: node %d has empty ID", i)
		}
		if n.Kind != KindTable && n.Kind != KindFunction {
			return errf("audit: node %q has unknown kind %d", n.ID, n.Kind)
		}
		if _, dup := seen[n.ID]; dup {
			return errf("audit: duplicate node ID %q", n.ID)
		}
		seen[n.ID] = n
	}

	for i, e := range g.Edges {
		if _, ok := seen[e.From]; !ok {
			return errf("audit: edge %d source %q is not a node", i, e.From)
		}
		if _, ok := seen[e.To]; !ok {
			return errf("audit: edge %d destination %q is not a node", i, e.To)
		}
		if e.Kind < EdgeField || e.Kind > EdgeUpvalue {
			return errf("audit: edge %d has unknown kind %d", i, e.Kind)
		}
		if e.Kind == EdgeUpvalue && e.Index <= 0 {
			return errf("audit: upvalue edge %q -> %q requires a 1-based index", e.From, e.To)
		}
	}

	for i, r := range g.Roots {
		if _, ok := seen[r.ID]; !ok {
			return errf("audit: root %d references unknown node %q", i, r.ID)
		}
		if r.Label == "" {
			return errf("audit: root %q has empty label", r.ID)
		}
	}
	return nil
}
