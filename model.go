package netsec

// Op is the mutex operation attached to a state-machine edge.
type Op int

const (
	// OpNone performs no lock operation.
	OpNone Op = iota
	// OpLock requests (and, when free, acquires) a mutex.
	OpLock
	// OpUnlock releases a previously acquired mutex.
	OpUnlock
)

// Guard is a Boolean condition on an edge. Condition variables referenced by
// a guard keep the same truth value for the whole execution of one analysis
// run: the analyzer enumerates every fixed assignment once and explores the
// concrete state space under that assignment.
//
// The concrete implementations are Var, Not, And and Or. A nil Guard is
// treated as the constant true. Guards are constructed by this package; the
// interface methods are unexported on purpose.
type Guard interface {
	vars(set map[string]struct{})
	guardNode()
}

// Var is a named Boolean condition variable.
type Var struct {
	Name string
}

func (v Var) vars(set map[string]struct{}) {
	set[v.Name] = struct{}{}
}
func (Var) guardNode() {}

// Not negates the inner guard.
type Not struct {
	Inner Guard
}

func (n Not) vars(set map[string]struct{}) {
	if n.Inner != nil {
		n.Inner.vars(set)
	}
}
func (Not) guardNode() {}

// And requires both operands to hold.
type And struct {
	Lhs Guard
	Rhs Guard
}

func (a And) vars(set map[string]struct{}) {
	if a.Lhs != nil {
		a.Lhs.vars(set)
	}
	if a.Rhs != nil {
		a.Rhs.vars(set)
	}
}
func (And) guardNode() {}

// Or requires at least one operand to hold.
type Or struct {
	Lhs Guard
	Rhs Guard
}

func (o Or) vars(set map[string]struct{}) {
	if o.Lhs != nil {
		o.Lhs.vars(set)
	}
	if o.Rhs != nil {
		o.Rhs.vars(set)
	}
}
func (Or) guardNode() {}

// Edge is a transition of one thread's finite state machine.
//
// For OpLock/OpUnlock, Lock must name one of Spec.Locks. For OpNone, Lock
// must be empty. When Guard is nil the edge is unconditional.
type Edge struct {
	// Name is used in trace output; empty names are auto-generated.
	Name  string
	From  string
	To    string
	Op    Op
	Lock  string
	Guard Guard
}

// Thread is one finite-state thread. A state without any enabled edge is a
// normal terminal state: a thread reaching it has exited.
type Thread struct {
	Name   string
	Start  string
	States []string
	Edges  []Edge
}

// Spec is the analysis input: exactly two threads and at most six mutexes.
type Spec struct {
	Threads [2]Thread
	Locks   []string
}

// DeadlockKind classifies the deadlock that was reached.
type DeadlockKind int

const (
	// DeadlockNone means no reachable deadlock exists.
	DeadlockNone DeadlockKind = iota
	// DeadlockRecursive means a thread requests a non-reentrant mutex it
	// already holds.
	DeadlockRecursive
	// DeadlockCycle means blocked threads form a wait-for cycle, including
	// the classic A-then-B / B-then-A pattern.
	DeadlockCycle
	// DeadlockOrphaned means a blocked thread waits on a mutex whose owner
	// has already terminated, so the request can never be granted.
	DeadlockOrphaned
)

// String returns a stable English label for the deadlock kind.
func (k DeadlockKind) String() string {
	switch k {
	case DeadlockRecursive:
		return "recursive"
	case DeadlockCycle:
		return "cycle"
	case DeadlockOrphaned:
		return "orphaned"
	default:
		return "none"
	}
}

// Step is one executed transition in the shortest interleaving trace. Edges
// whose lock request is denied never execute and therefore never appear.
type Step struct {
	Seq    int
	Thread string
	Edge   string
	From   string
	To     string
	Op     Op
	Lock   string
}

// Result reports the outcome of an analysis run.
type Result struct {
	// Reachable reports whether a deadlock state is reachable.
	Reachable bool
	// Kind classifies the reachable deadlock; DeadlockNone when Reachable is false.
	Kind DeadlockKind
	// Assignment gives the fixed truth values of every condition variable for
	// the run that reaches the deadlock. Variables absent from the input do
	// not appear.
	Assignment map[string]bool
	// Trace is the shortest interleaving (fewest executed edges) leading to
	// the deadlock under Assignment.
	Trace []Step
	// Explored is the number of distinct concrete states visited while
	// searching, including the initial state.
	Explored int

	// BlockedThread is the thread whose request cannot be granted.
	BlockedThread string
	// RequestedLock is the mutex named by that blocked request.
	RequestedLock string
	// Owner is the current holder of RequestedLock, empty when the mutex is free.
	Owner string
	// Edge is the blocked request edge of BlockedThread.
	Edge string
	// From is the state at which BlockedThread is blocked.
	From string
}
