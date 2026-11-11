package netsec

// SymID is a symbolic identifier for a protocol value, such as a ticket,
// nonce, connection id or stream id. Symbolic ids are scoped per symbol kind:
// the same numeric id used for different symbols must be given distinct
// SymID values (for example by using different high-order bits per kind).
type SymID uint64

// Message is one step of a protocol trace.
//
// A message belongs to exactly one connection (Conn). When executed it may
// produce fresh symbols (Produces) and consume symbols produced by earlier
// messages (Consumes). A message may legitimately consume the same symbol more
// than once and the trace may contain several messages that produce the same
// symbol; reduction handles both cases explicitly.
//
// Message values are treated as immutable by the reducer. Repair always
// produces a copied message instead of mutating an existing one.
type Message struct {
	// ID is the index of the message within the original trace and is never
	// reassigned by reduction. It uniquely identifies the step in reports.
	ID int

	// Conn is the connection that owns the message. Dependencies cross
	// connection boundaries freely: a message on one connection can consume a
	// symbol produced on another connection (for example a ticket issued by
	// the server and presented on a second connection).
	Conn string

	// Kind is an application defined message type, used only by caller
	// supplied Harness implementations.
	Kind string

	// Produces lists the symbols that exist only after this message runs.
	Produces []SymID

	// Consumes lists the symbols this message needs to be able to run.
	Consumes []SymID

	// Payload carries application defined data the Harness understands.
	Payload any
}

// Clone returns a shallow copy of the message with new symbol slices, so
// repaired messages never share backing arrays with the original.
func (m Message) Clone() Message {
	produces := append([]SymID(nil), m.Produces...)
	consumes := append([]SymID(nil), m.Consumes...)
	return Message{
		ID:       m.ID,
		Conn:     m.Conn,
		Kind:     m.Kind,
		Produces: produces,
		Consumes: consumes,
		Payload:  m.Payload,
	}
}

// Run describes one candidate evaluation performed by the reducer.
type Run struct {
	// Generation identifies the fresh session used by the run. It matches a
	// Session.Generation value handed out by the Harness.
	Generation uint64

	// Kept are the message ids of the evaluated candidate in execution order.
	Kept []int

	// Failure is the caller defined failure class. The empty string means the
	// candidate did not fail.
	Failure string

	// TimedOut reports whether the candidate exceeded the caller deadline.
	TimedOut bool

	// Accepted reports whether the candidate replaced the current best trace.
	Accepted bool
}

// ClosureCheck records the final minimality audit for one deletable closure.
type ClosureCheck struct {
	// Removed is the set of message ids whose joint deletion was probed.
	Removed []int

	// Failure is the class observed on the probe.
	Failure string

	// TimedOut reports whether the probe hit the caller deadline.
	TimedOut bool

	// StillRemovable is true when the probe failed with the target class, in
	// which case the reducer continued shrinking instead of declaring success.
	StillRemovable bool
}

// Result is the outcome of Minimize.
type Result struct {
	// Messages is the smallest trace found, in execution order.
	Messages []Message

	// TargetFailure is the failure class of the original trace; every
	// accepted candidate failed with this exact class.
	TargetFailure string

	// Runs is the full evaluation history, including rejected probes. It lets
	// callers (and tests) verify that every candidate used a fresh session.
	Runs []Run

	// ClosureChecks is the final audit over every deletable closure.
	ClosureChecks []ClosureCheck
}

// T is the subset of *testing.T that MinimizeT depends on. Accepting an
// interface keeps the package usable from custom test harnesses as well.
type T interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
}
