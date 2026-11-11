// Package ownership implements a tenant-generation based process ownership
// model for a Go process host (supervisor).
//
// # Identity model
//
// Every spawned process inherits the tenant generation of its parent at
// creation time. A generation is immutable for the lifetime of the process:
// setpgid/setsid and other process-group or session changes never move a
// process between generations, and a process that outlives its parent keeps
// the generation it was born with. The reachable descendant set of a tenant
// generation is therefore exactly the set of processes that carry that
// generation, independent of the current process-group topology.
//
// # Event sourcing
//
// All state transitions are expressed as events (Spawned, Exited,
// GroupChanged, Sealed) applied to a Tracker. This makes the model
// deterministic and independently testable against the failure scenarios a
// host must survive: a parent exiting before its children, children
// reorganizing their process groups, and PID reuse by a process belonging to
// a different tenant.
//
// # Cleanup protocol
//
// Tenant shutdown (see Shutdown) first seals the generation, which rejects any
// subsequent Spawned event for that generation (the new-spawn lock), then
// terminates the entire reachable descendant set captured at that instant and
// waits until every member has produced an Exited event or the context
// expires. SIGTERM is escalated to SIGKILL after a configurable grace period.
package ownership

import "strconv"

// Gen is an immutable tenant generation identifier. A tenant gets a fresh Gen
// each time it is (re)started, so processes from distinct tenant incarnations
// can never share a generation even when their PIDs collide.
//
// Gen values are comparable and safe to use as map keys. Their fields must be
// treated as read-only.
type Gen struct {
	// TenantID identifies the tenant and never changes within a tenant's life.
	TenantID string
	// Epoch distinguishes successive incarnations of the same tenant.
	Epoch uint64
}

// Valid reports whether g carries a usable tenant identity.
func (g Gen) Valid() bool {
	return g.TenantID != ""
}

// String renders g in the stable "tenantID:epoch" wire format. Use ParseGen
// to recover the value; the encoding is used in environment markers handed to
// supervised children.
func (g Gen) String() string {
	return g.TenantID + ":" + strconv.FormatUint(g.Epoch, 10)
}

// ParseGen parses a Gen previously rendered with Gen.String.
func ParseGen(s string) (Gen, error) {
	colon := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			colon = i
			break
		}
	}
	if colon <= 0 || colon == len(s)-1 {
		return Gen{}, &ParseError{Value: s}
	}
	tenant := s[:colon]
	epoch, err := strconv.ParseUint(s[colon+1:], 10, 64)
	if err != nil {
		return Gen{}, &ParseError{Value: s, Err: err}
	}
	return Gen{TenantID: tenant, Epoch: epoch}, nil
}

// ParseError is returned by ParseGen for malformed generation encodings.
type ParseError struct {
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Err != nil {
		return "ownership: malformed generation " + strconv.Quote(e.Value) + ": " + e.Err.Error()
	}
	return "ownership: malformed generation " + strconv.Quote(e.Value)
}

func (e *ParseError) Unwrap() error { return e.Err }

// Event is a state transition consumed by Tracker.Apply.
type Event interface {
	isOwnershipEvent()
}

// Spawned reports the creation of a process.
//
// Generation is the tenant generation the process inherits at creation. It
// must equal the generation of ParentPID when the parent is itself tracked.
// ParentPID may be zero or unknown for a root process adopted by the host.
// PGID is the process group at creation (often equal to PID); it is recorded
// for diagnostics only and never participates in ownership decisions.
type Spawned struct {
	PID        int
	ParentPID  int
	Generation Gen
	PGID       int
}

// Exited reports that a process terminated. Exiting does not free its
// generation membership until any delayed bookkeeping is pruned explicitly.
type Exited struct {
	PID    int
	Status int
}

// GroupChanged reports a setpgid/setsid transition. It updates the recorded
// process-group and session metadata only; the process's generation, and thus
// its ownership, is untouched.
type GroupChanged struct {
	PID      int
	PGID     int
	SID      int
	FromPGID int
	FromSID  int
}

// Sealed closes a tenant generation against further spawns. Applying Sealed is
// idempotent.
type Sealed struct {
	Generation Gen
}

func (Spawned) isOwnershipEvent()      {}
func (Exited) isOwnershipEvent()       {}
func (GroupChanged) isOwnershipEvent() {}
func (Sealed) isOwnershipEvent()       {}
