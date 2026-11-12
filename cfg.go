// Package netsec is a bounded effect analysis intended to be called by a
// Go-oriented security review service.
//
// The analysis answers one narrow question: when an authentication call
// fails, can a wrapper function still present a success to its caller?
// Input is a finite control-flow graph (CFG) whose blocks carry a small,
// explicit vocabulary of operations:
//
//   - authentication calls (normal edge = success, exception edge = failure),
//   - error variable assignments, including clearing an error,
//   - panic recovery,
//   - returns classified as success or explicit denial,
//   - protected callbacks that must only run after an authenticated success,
//   - generic panics whose exceptional flow merges back into the graph.
//
// The analyzer enumerates concrete paths and, for every path, tracks two
// independent facts:
//
//   - the most recent successful credential (the only fact that can clear a
//     failure), and
//   - the origin of the error currently held in each error variable.
//
// An authentication failure is a sticky state: overwriting or discarding the
// error value, recovering from a panic, or merging with another edge never
// removes it. Only an explicit new authentication success on the same path
// clears the failure. Path enumeration (rather than a single merged fixpoint)
// keeps an exceptional edge distinct from the normal edges it later joins,
// which lets the analyzer distinguish a benign merge from an error that has
// been silently swallowed.
package netsec

// EdgeKind classifies a CFG edge.
type EdgeKind int

const (
	// EdgeNormal is ordinary intra-function control flow.
	EdgeNormal EdgeKind = iota
	// EdgeException is the error/panic edge of its source block.
	EdgeException
)

func (k EdgeKind) String() string {
	switch k {
	case EdgeException:
		return "exception"
	default:
		return "normal"
	}
}

// Edge connects two blocks. An authentication or panic block with an
// exceptional outcome uses EdgeException; every other transfer uses
// EdgeNormal.
type Edge struct {
	From string
	To   string
	Kind EdgeKind
}

// CFG is the finite control-flow graph handed to the analyzer.
type CFG struct {
	Entry  string
	Blocks map[string]*Block
	Edges  []Edge
}

// Block is a basic block: an ordered list of operations followed by its
// outgoing edges.
type Block struct {
	ID  string
	Ops []Op
}

// Op is one node of the effect vocabulary. The concrete types OpAuth,
// OpErrorAssign, OpRecover, OpReturn, OpProtectedCallback and OpPanic
// implement this interface.
type Op interface {
	opTag()
}

// OpAuth models an authentication call, e.g.
//
//	cred, err := authenticator.Authenticate(req)
//
// The call itself carries no outcome: the edge leaving its block does.
// A normal edge records an explicit new authentication success and binds the
// returned credential in CredVar; an exception edge records a failure whose
// returned error is held in ErrVar (or, when the call panics, an uncaught
// panic that unwinds until an OpRecover).
type OpAuth struct {
	// Site identifies the call site for reports.
	Site string
	// CredVar receives the credential on the normal (success) edge.
	CredVar string
	// ErrVar receives the error on the exception edge. Empty means the
	// failure surfaces only as a panic.
	ErrVar string
}

// OpErrorAssign models an assignment into an error variable, including a
// nil/zero assignment that clears the variable.
type OpErrorAssign struct {
	Site string
	// Var is the assigned error variable.
	Var string
	// Source describes the right-hand side, e.g. another error variable,
	// fmt.Errorf(...), or "nil". It is informational; whether the variable
	// still carries an authentication error is decided structurally.
	Source string
	// Clear marks a nil/zero assignment (err = nil).
	Clear bool
}

// OpPanic models a generic panic. Its block transfers on EdgeException while
// the panic is in flight; an OpRecover consumes it.
type OpPanic struct {
	Site   string
	Reason string
}

// OpRecover models a recover()-based handler reached on an exception edge.
// After recovery the unwound panic is consumed. ErrVar, when non-empty,
// receives the recovered error value.
type OpRecover struct {
	Site   string
	ErrVar string
}

// OpReturn models a function return.
type OpReturn struct {
	Site string
	// Denied marks an explicit security denial, e.g.
	// return nil, ErrPermissionDenied. Otherwise the return presents a
	// success to the wrapper's caller.
	Denied bool
	// ErrVar names the error value returned alongside the result, if any.
	ErrVar string
}

// OpProtectedCallback models an action that is only safe after an
// authenticated success, such as invoking a privileged handler with the
// caller's credential.
type OpProtectedCallback struct {
	Site   string
	Name   string
	Target string
}

func (OpAuth) opTag()              {}
func (OpErrorAssign) opTag()       {}
func (OpPanic) opTag()             {}
func (OpRecover) opTag()           {}
func (OpReturn) opTag()            {}
func (OpProtectedCallback) opTag() {}
