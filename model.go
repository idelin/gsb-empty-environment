package netsec

// Target identifies a versioned location: the object owning the location plus
// the (possibly empty) field path rooted at that object. An empty Field means
// the object as a whole (for example a length or the whole heap object).
//
// Target is comparable and can be used as a map key.
type Target struct {
	Object string
	Field  string
}

// ObjectGraph is the field-sensitive model of the heap and roots that the
// analysis consumes. Fields that hold references are expressed in Edges: an
// edge (object, field) points at the referenced object. Non-reference fields
// (length, capacity, scalar state) simply appear in check/read/write targets
// and do not need an edge.
type ObjectGraph struct {
	// Roots map a variable/root name to the object it denotes.
	Roots map[string]string

	// Edges maps an object to each of its reference fields and the object the
	// field points to. Multiple edges may share a destination, which is how
	// aliasing and recursive aliases are represented.
	Edges map[string]map[string]string
}

// Node kinds in the control-flow graph.
const (
	NodeEntry = "entry"
	NodeCheck = "check"
	NodeRead  = "read"
	NodeWrite = "write"
	NodeCall  = "call"
	NodeJoin  = "join"
	NodeExit  = "exit"
)

// Gap kinds.
const (
	// GapInvalidated: a dominating check exists but a write through some alias
	// changed the location afterward on the witnessing path.
	GapInvalidated = "invalidated"
	// GapUnchecked: the read has no dominating check fact on the witnessing
	// path.
	GapUnchecked = "unchecked"
)

// Node is a single control-flow node.
//
//   - Check nodes bind a check fact to the resolved versions of Targets.
//   - Read nodes consume a previously bound fact; a gap is reported when the
//     fact cannot be proven valid at the read.
//   - Write nodes assign one object field through one of its roots/aliases.
//   - Call nodes rely on a CallSummary for their possible write set.
type Node struct {
	ID   string
	Kind string

	// Root is the variable/root expression used by check/read/write nodes.
	Root string
	// Field is the field path relative to Root, using "." separators. An empty
	// field denotes the root object itself.
	Field string

	// Summary names a CallSummary for NodeCall nodes.
	Summary string

	// Comment is optional, human-readable context carried into gap paths.
	Comment string
}

// CFG is a directed control-flow graph. Successors must form a reducible or
// irreducible graph; the analysis uses a worklist and makes no assumption
// about structured loops.
type CFG struct {
	Entry      string
	Successors map[string][]string
}

// RootWrite is one write that a callee may perform, expressed relative to one
// of the callee parameters. The caller binds each Param to one of its own
// roots via ArgumentBinding.
type RootWrite struct {
	Param string
	Field string
}

// CallSummary is an inter-procedural summary: the set of locations the callee
// may write. A may-write is sufficient because any possible write through any
// alias invalidates the affected check facts.
type CallSummary struct {
	ID        string
	MayWrites []RootWrite
}

// ArgumentBinding binds a callee parameter to a caller root for one call site.
type ArgumentBinding struct {
	Param string
	Root  string
}

// CallSite is the caller-side information for a NodeCall node.
type CallSite struct {
	NodeID   string
	Bindings []ArgumentBinding
}

// Program bundles everything Analyze needs.
type Program struct {
	Graph     ObjectGraph
	Nodes     []Node
	CFG       CFG
	Calls     []CallSite
	Summaries []CallSummary
}

// Gap describes a failed check-to-read dependency: the fact bound at Check
// (if any) cannot be proven still valid at Read because some write on Path
// may have changed the location through an alias.
type Gap struct {
	Read   string
	Check  string
	Target Target

	// Path is the concrete node sequence from the entry to Read that witnesses
	// the invalidation. The node directly before the failing suffix is either
	// the entry (no dominating check), a write, or a call whose summary writes
	// the target.
	Path []string

	// Invalidator is the node that invalidated the fact on Path. It is empty
	// when the gap is "no dominating check at all".
	Invalidator string
	Kind        string
	Message     string
}

// FactView exposes the check facts that survive at one node, together with the
// object field versions they are bound to.
type FactView struct {
	Check   string
	Target  Target
	Version VersionView
}

// VersionView is the exported, read-only form of a field version.
type VersionView struct {
	// Label is a monotonically assigned version label; 0 is the initial
	// version, each check or write takes the next label.
	Label int
	// Top means the version is the join of conflicting versions (the location
	// may have been modified along some incoming path).
	Top bool
}

// StateView is the analysis state on entry to a node after the fixpoint.
type StateView struct {
	Versions map[Target]VersionView
	Facts    []FactView
}

// Result is the output of Analyze.
type Result struct {
	Gaps   []Gap
	States map[string]StateView

	// Labels records the version label assigned to each check/write node.
	Labels map[string]int
}
