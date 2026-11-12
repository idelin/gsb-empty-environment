package taskpkg

// EdgeKind identifies a control edge. Every non-terminal transition in the
// interpreter and the compiled program emits exactly one edge event, so
// short-circuit, exception and rejection paths all have explicit edges.
type EdgeKind int

const (
	EdgeLeafTrue EdgeKind = iota
	EdgeLeafFalse
	EdgeLeafRaise
	EdgeNot
	EdgeAndTrue
	EdgeAndFalse // short-circuit edge
	EdgeOrFalse
	EdgeOrTrue // short-circuit edge
	EdgeSeq
	EdgeBranchThen
	EdgeBranchElse
	EdgeTryEnter
	EdgeTryNormal
	EdgeTryCatch
	EdgeReject
	EdgeForwardAllow
	EdgeForwardDeny
	EdgeRaiseEscape
)

// String returns a stable human-readable edge name.
func (k EdgeKind) String() string {
	names := [...]string{
		"leaf.true", "leaf.false", "leaf.raise",
		"not",
		"and.true", "and.false",
		"or.false", "or.true",
		"seq",
		"branch.then", "branch.else",
		"try.enter", "try.normal", "try.catch",
		"reject",
		"forward.allow", "forward.deny",
		"raise.escape",
	}
	if int(k) < 0 || int(k) >= len(names) {
		return "edge.unknown"
	}
	return names[k]
}

// EdgeEvent records one explicit control-edge traversal.
type EdgeEvent struct {
	Kind EdgeKind
	From int // source AST node id; 0 for synthetic seq/try scaffolding
	To   int // target AST node id; 0 for a terminal edge
}

// LeafEvent records one leaf check occurrence, in actual execution order.
type LeafEvent struct {
	NodeID int  // AST node id of this occurrence (checks may occur repeatedly)
	Check  int  // check/input index
	Value  bool // returned boolean; false on a raise
	Raised bool
	// Cached is set by the compiled program when a pure check occurrence was
	// served from the memo table instead of being re-executed. The original
	// interpreter never caches. Effectful occurrences are never cached.
	Cached bool
	Marks  []Mark // marks produced by this occurrence (empty when cached/raised)
}

// Decision is the terminal outcome of evaluating a rule.
type Decision int

const (
	DecisionPassed Decision = iota // body completed without reject/deny
	DecisionForwarded
	DecisionRejected
	DecisionDenied // forward blocked because a required mark was missing
	DecisionUnhandled
)

func (d Decision) String() string {
	switch d {
	case DecisionPassed:
		return "passed"
	case DecisionForwarded:
		return "forwarded"
	case DecisionRejected:
		return "rejected"
	case DecisionDenied:
		return "denied"
	case DecisionUnhandled:
		return "unhandled"
	default:
		return "unknown"
	}
}

// Trace is the observable execution record for one input assignment.
type Trace struct {
	Edges    []EdgeEvent
	Leaves   []LeafEvent
	Marks    []Mark // marks in production order
	Decision Decision
	// ForwardName is set for forwarded/denied outcomes.
	ForwardName string
	// RejectReason is set for rejected outcomes.
	RejectReason string
	// DenyMissing records the missing mark that caused a forward deny.
	DenyMissing []Mark
}

// HasMark reports whether the mark set contains m, applying trust scoping:
// a trusted requirement is only satisfied by a trusted mark.
func HasMark(marks []Mark, m Mark) bool {
	for _, got := range marks {
		if got.Field != m.Field || got.Name != m.Name {
			continue
		}
		if m.Trusted && !got.Trusted {
			continue
		}
		return true
	}
	return false
}

// MissingRequired returns the first required mark absent from the set, in
// requirement order.
func MissingRequired(marks []Mark, required []Mark) (Mark, bool) {
	for _, m := range required {
		if !HasMark(marks, m) {
			return m, true
		}
	}
	return Mark{}, false
}

// CompareTraces reports whether an interpreter trace and a compiled-program
// trace agree on edge sequence, leaf occurrence order, marks and decision.
// The compiled trace may flag pure leaf occurrences as Cached; that is the
// only permitted divergence, and effectful occurrences must never be cached.
func CompareTraces(want, got *Trace) error {
	if len(want.Edges) != len(got.Edges) {
		return errf("edge count: want %d, got %d", len(want.Edges), len(got.Edges))
	}
	for i := range want.Edges {
		if want.Edges[i] != got.Edges[i] {
			return errf("edge %d: want %+v, got %+v", i, want.Edges[i], got.Edges[i])
		}
	}
	if len(want.Leaves) != len(got.Leaves) {
		return errf("leaf count: want %d, got %d", len(want.Leaves), len(got.Leaves))
	}
	for i := range want.Leaves {
		w, g := want.Leaves[i], got.Leaves[i]
		if w.NodeID != g.NodeID || w.Check != g.Check || w.Value != g.Value || w.Raised != g.Raised {
			return errf("leaf %d: want %+v, got %+v", i, w, g)
		}
		if !sameMarks(w.Marks, g.Marks) {
			return errf("leaf %d marks: want %v, got %v", i, w.Marks, g.Marks)
		}
		if g.Cached {
			// Caching is legal only for occurrences the interpreter sees as
			// side-effect-free; the compiled evaluator enforces this too.
			if w.Raised || len(w.Marks) != 0 {
				return errf("leaf %d: effectful occurrence was cached", i)
			}
		}
	}
	if !sameMarks(want.Marks, got.Marks) {
		return errf("mark sequence: want %v, got %v", want.Marks, got.Marks)
	}
	if want.Decision != got.Decision ||
		want.ForwardName != got.ForwardName ||
		want.RejectReason != got.RejectReason {
		return errf("decision: want %s/%s/%s, got %s/%s/%s",
			want.Decision, want.ForwardName, want.RejectReason,
			got.Decision, got.ForwardName, got.RejectReason)
	}
	return nil
}

func sameMarks(a, b []Mark) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
