package netsec

import "errors"

// EventKind identifies one auditable admission-control transition.
type EventKind int

const (
	// EventParentBound is emitted when a parent request is registered.
	EventParentBound EventKind = iota + 1
	// EventPromiseAdmitted is emitted only after all checks pass and the
	// promised stream has been reserved.
	EventPromiseAdmitted
	// EventPromiseRejected is emitted for every rejected PUSH_PROMISE.
	EventPromiseRejected
	// EventResourceDelivered is emitted when the business handler is called.
	EventResourceDelivered
	// EventParentCanceled records cancellation of a parent request stream.
	EventParentCanceled
	// EventPushCanceled records cancellation of a promised stream.
	EventPushCanceled
	// EventParentCompleted records the normal end of a parent stream.
	EventParentCompleted
	// EventGenerationAdvanced records a new physical connection incarnation.
	EventGenerationAdvanced
)

// String returns the stable name of the event kind.
func (k EventKind) String() string {
	switch k {
	case EventParentBound:
		return "parent_bound"
	case EventPromiseAdmitted:
		return "promise_admitted"
	case EventPromiseRejected:
		return "promise_rejected"
	case EventResourceDelivered:
		return "resource_delivered"
	case EventParentCanceled:
		return "parent_canceled"
	case EventPushCanceled:
		return "push_canceled"
	case EventParentCompleted:
		return "parent_completed"
	case EventGenerationAdvanced:
		return "generation_advanced"
	default:
		return "unknown"
	}
}

// CancelReason explains which side tore a stream down. Parent and push
// cancellations are intentionally separate values so the audit trail can
// never conflate a client abandoning its request with a server aborting a
// push.
type CancelReason int

const (
	// CancelUnspecified is used when no finer reason is available.
	CancelUnspecified CancelReason = iota
	// CancelByClient indicates the client sent RST_STREAM (parent or push).
	CancelByClient
	// CancelByServer indicates the proxy/server aborted the stream.
	CancelByServer
	// CancelByTimeout indicates an idle or deadline timeout fired.
	CancelByTimeout
)

// String returns the stable name of the cancellation reason.
func (r CancelReason) String() string {
	switch r {
	case CancelByClient:
		return "client"
	case CancelByServer:
		return "server"
	case CancelByTimeout:
		return "timeout"
	default:
		return "unspecified"
	}
}

// Event is one immutable audit entry. Err is non-nil only for rejection
// events. Generation, ParentID and PromisedID are recorded exactly as
// observed, even for frames that are ultimately refused.
type Event struct {
	Kind       EventKind
	ParentID   uint32
	PromisedID uint32
	Generation uint64
	Reason     CancelReason
	Err        error
}

// Stats is a point-in-time snapshot of admission counters. Parent and push
// cancellation are counted independently.
type Stats struct {
	Admitted       int
	Rejected       int
	Delivered      int
	ParentCanceled int
	PushCanceled   int
}

// Sentinel errors. Callers should use errors.Is rather than string matching.
var (
	// ErrInvalidStreamID covers illegal stream numbers, wrong parity, or an
	// even identifier presented as a client stream.
	ErrInvalidStreamID = errors.New("netsec: invalid stream identifier")
	// ErrStreamInUse means the promised stream identifier was already
	// reserved within the same connection generation.
	ErrStreamInUse = errors.New("netsec: stream identifier already in use")
	// ErrUnknownParent means no bound parent request matches the promise on
	// the current generation and no earlier generation ever bound it either.
	ErrUnknownParent = errors.New("netsec: unknown parent stream")
	// ErrGenerationMismatch means the referenced stream belongs to a prior
	// physical connection incarnation (a late or replayed frame).
	ErrGenerationMismatch = errors.New("netsec: connection generation mismatch")
	// ErrOriginNotAuthorized means the promised origin is absent from the
	// parent request's authorization set.
	ErrOriginNotAuthorized = errors.New("netsec: promised origin not authorized by parent")
	// ErrParentCanceled means the parent request was already canceled.
	ErrParentCanceled = errors.New("netsec: parent request canceled")
	// ErrParentCompleted means the parent stream ended normally before the
	// promise could be admitted or delivered.
	ErrParentCompleted = errors.New("netsec: parent request already completed")
	// ErrPushCanceled means the promised stream was canceled.
	ErrPushCanceled = errors.New("netsec: pushed stream canceled")
	// ErrAlreadyDelivered means a resource was delivered more than once.
	ErrAlreadyDelivered = errors.New("netsec: resource already delivered")
	// ErrUnknownAdmission means the promised stream is unknown on the current
	// connection generation.
	ErrUnknownAdmission = errors.New("netsec: unknown push admission")
)
