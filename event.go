package netsec

// EventKind identifies the kind of instrumentation event fed to a Monitor.
type EventKind int

const (
	// EventSwitch marks a runnable starting (or resuming) on a worker thread.
	// Thread-pool reuse is expressed as multiple Switch events carrying the
	// same ThreadID but different RequestID.
	EventSwitch EventKind = iota + 1
	// EventSetIdentity installs the request's base identity on the thread.
	EventSetIdentity
	// EventBorrow enters a nested borrowed-identity scope.
	EventBorrow
	// EventReturn leaves the innermost borrowed-identity scope.
	EventReturn
	// EventSpawn creates a child thread. A child may only inherit identity
	// through an explicit Snapshot passed via Event.Snapshot.
	EventSpawn
	// EventExit ends the request on the thread. The frame stack must be empty
	// at this point, otherwise a DirtyExit finding is raised and forced to zero.
	EventExit
	// eventSnapshot is synthetic: snapshots are taken through Monitor.Snapshot,
	// never ingested directly. Kept here so Provenance.Kind has a stable value.
	eventSnapshot
)

// String renders the event kind for diagnostics and test output.
func (k EventKind) String() string {
	switch k {
	case EventSwitch:
		return "switch"
	case EventSetIdentity:
		return "set_identity"
	case EventBorrow:
		return "borrow"
	case EventReturn:
		return "return"
	case EventSpawn:
		return "spawn"
	case EventExit:
		return "exit"
	case eventSnapshot:
		return "snapshot"
	default:
		return "unknown"
	}
}

// Identity is the security principal associated with a request scope.
// RequestID distinguishes logical requests; two identities that share a
// Principal still belong to different requests when RequestID differs.
type Identity struct {
	RequestID string
	Principal string
	Attrs     map[string]string
}

// Event is a single instrumentation observation from the Go host.
//
// Field usage by kind:
//
//	Switch      ThreadID, RequestID
//	SetIdentity ThreadID, Identity
//	Borrow      ThreadID, Identity, Tag
//	Return      ThreadID, Tag (optional matching tag)
//	Spawn       ThreadID, ChildID, Snapshot (explicit only), Inherit
//	Exit        ThreadID
type Event struct {
	Kind     EventKind
	ThreadID string

	// RequestID annotates Switch events with the logical request now running.
	RequestID string

	// Identity carries the identity for SetIdentity and Borrow events.
	Identity Identity

	// Tag names a borrow scope so the matching Return can be verified.
	Tag string

	// ChildID names the new thread on Spawn.
	ChildID string

	// Snapshot is the only legal way to carry identity into a spawned thread.
	Snapshot *Snapshot

	// Inherit, when true on Spawn without a Snapshot, models host code that
	// tries to implicitly inherit the current identity. It always raises an
	// ImplicitInheritance finding and the child starts with an empty stack.
	Inherit bool
}
