package netsec

// EventKind distinguishes the two audit records involved in a credential
// revocation review.
type EventKind int

const (
	// KindUse is a credential usage record.
	KindUse EventKind = iota + 1
	// KindRevoke is a credential deletion/revocation record.
	KindRevoke
)

// Interval is the inclusive possible-occurrence window of an event.
// Bounds are expressed in an abstract discrete time domain (integer ticks);
// both Lo and Hi are feasible times, so an empty window is one where Lo > Hi.
type Interval struct {
	Lo int64
	Hi int64
}

// Contains reports whether t falls inside the inclusive window.
func (in Interval) Contains(t int64) bool {
	return in.Lo <= t && t <= in.Hi
}

// Empty reports whether the window admits no time at all.
func (in Interval) Empty() bool {
	return in.Lo > in.Hi
}

// Event is a single cloud-audit record.
//
// Seq is the trusted commit sequence assigned inside a Feed: when two events
// share a Feed, the event with the smaller Seq is guaranteed to have happened
// strictly before the event with the larger Seq. Events from different feeds
// have no trusted ordering. Lo/Hi bound when the event could have happened;
// arrival order is deliberately not part of the input.
type Event struct {
	ID   string
	Feed string
	Seq  int64
	Kind EventKind
	Cred string
	Lo   int64
	Hi   int64
}

// Window returns the event's raw possible-occurrence interval.
func (e Event) Window() Interval {
	return Interval{Lo: e.Lo, Hi: e.Hi}
}

// Verdict is the analytic conclusion for a use/revoke pair sharing a
// credential.
type Verdict int

const (
	// VerdictUnknown is returned when the input windows are mutually
	// inconsistent and no feasible ordering exists.
	VerdictUnknown Verdict = iota
	// VerdictBefore means the usage necessarily happened strictly before
	// the revocation (no revocation-boundary crossing).
	VerdictBefore
	// VerdictAfter means the usage necessarily happened strictly after the
	// revocation, i.e. use of an already-revoked credential.
	VerdictAfter
	// VerdictStraddle means both orderings (and possibly same-time
	// occurrence) remain feasible: the pair may cross the revocation
	// boundary and needs human review.
	VerdictStraddle
)

func (v Verdict) String() string {
	switch v {
	case VerdictBefore:
		return "necessarily-before"
	case VerdictAfter:
		return "necessarily-after"
	case VerdictStraddle:
		return "possibly-straddles"
	default:
		return "unknown"
	}
}

// ConflictKind categorizes why an input or conclusion could not be trusted.
type ConflictKind int

const (
	// ConflictInconsistentWindow marks an event whose window collapses
	// under the trusted sequence constraints (no feasible occurrence time).
	ConflictInconsistentWindow ConflictKind = iota + 1
	// ConflictDuplicateSeq marks two events on one feed sharing a seq.
	ConflictDuplicateSeq
	// ConflictCheckMismatch marks a pair whose analytic verdict disagrees
	// with exhaustive small-domain enumeration.
	ConflictCheckMismatch
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictInconsistentWindow:
		return "inconsistent-window"
	case ConflictDuplicateSeq:
		return "duplicate-sequence"
	case ConflictCheckMismatch:
		return "enumeration-mismatch"
	default:
		return "unknown-conflict"
	}
}

// Conflict retains a detected problem instead of discarding the offending
// event or silently picking one side.
type Conflict struct {
	Kind    ConflictKind
	EventID string
	Detail  string
}
