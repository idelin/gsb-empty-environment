package recovmon

// ViolationKind classifies why an event was rejected.
type ViolationKind string

const (
	// ViolationSpanOutOfRange means an event leaves the raw byte interval.
	ViolationSpanOutOfRange ViolationKind = "span_out_of_range"
	// ViolationInvalidSpan means an interval is empty or negative.
	ViolationInvalidSpan ViolationKind = "invalid_span"
	// ViolationUnbalancedEnd means an end event has no matching open field.
	ViolationUnbalancedEnd ViolationKind = "unbalanced_field_end"
	// ViolationNesting means a field starts outside or crosses its parent.
	ViolationNesting ViolationKind = "field_nesting_violation"
	// ViolationConsumedDiscard means a consume event reads discarded bytes.
	ViolationConsumedDiscard ViolationKind = "consume_after_discard"
	// ViolationRecoverWithoutError means recovery happened with no pending error.
	ViolationRecoverWithoutError ViolationKind = "recovery_without_error"
	// ViolationReprocessed means the recovery touches already processed bytes.
	ViolationReprocessed ViolationKind = "reprocessed_range"
	// ViolationNotFieldBoundary means the resume offset is no unconsumed field start.
	ViolationNotFieldBoundary ViolationKind = "not_unconsumed_field_boundary"
	// ViolationAuthDiscard means discarded bytes overlap authenticated coverage.
	ViolationAuthDiscard ViolationKind = "authenticated_discard_overlap"
)

// FieldInfo is the evidence snapshot of one observed field.
type FieldInfo struct {
	Name      string
	Path      string
	Declared  Interval
	Consumed  Interval
	Depth     int
	Truncated bool
	Open      bool
}

// Violation is the structured evidence of one rejected event.
type Violation struct {
	// Index is the zero-based position of the offending event.
	Index int
	Kind  ViolationKind

	// Event is the rejected event as delivered by the caller.
	Event Event

	// Discard is the byte range the recovery attempted to throw away.
	Discard Interval
	// Target is the requested resume offset.
	Target int64
	// FirstBadByte is the earliest offset that makes the move illegal.
	FirstBadByte int64

	// AuthHits are authenticated intervals touched by the discard.
	AuthHits []Interval
	// CandidateBoundaries lists legal resume offsets near the target.
	CandidateBoundaries []int64

	// OpenFields is the active nesting stack at the moment of violation.
	OpenFields []FieldInfo
	// TruncatedFields lists fields whose declared range was never completed.
	TruncatedFields []FieldInfo

	// RecoveryCount counts recovery events seen so far, including this one.
	RecoveryCount int
	Message       string
}

// Report is a point-in-time snapshot of monitor state.
type Report struct {
	Raw            Interval
	Authenticated  []Interval
	Consumed       []Interval
	Discarded      []Interval
	Fields         []FieldInfo
	Violations     []Violation
	FirstViolation *Violation
}
