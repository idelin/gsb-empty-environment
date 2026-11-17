package netsec

import "time"

// EventKind identifies a lifecycle record emitted by an Admission.
type EventKind uint8

const (
	// EventParentRegistered records a newly bound parent request.
	EventParentRegistered EventKind = iota + 1
	// EventParentCanceled records cancellation of a parent request.
	EventParentCanceled
	// EventParentEnded records normal end of a parent request lifetime.
	EventParentEnded
	// EventPushAdmitted records a promise that passed admission and allocation.
	EventPushAdmitted
	// EventPushCanceled records cancellation of one push. The Reason field
	// distinguishes a direct push cancellation from propagation from its parent.
	EventPushCanceled
	// EventPushCompleted records normal completion of one push.
	EventPushCompleted
	// EventPromiseRejected records a promise that was not admitted.
	EventPromiseRejected
	// EventAdmissionClosed records connection-wide admission shutdown.
	EventAdmissionClosed
)

func (k EventKind) String() string {
	switch k {
	case EventParentRegistered:
		return "parent_registered"
	case EventParentCanceled:
		return "parent_canceled"
	case EventParentEnded:
		return "parent_ended"
	case EventPushAdmitted:
		return "push_admitted"
	case EventPushCanceled:
		return "push_canceled"
	case EventPushCompleted:
		return "push_completed"
	case EventPromiseRejected:
		return "promise_rejected"
	case EventAdmissionClosed:
		return "admission_closed"
	default:
		return "unknown"
	}
}

// Event is an immutable lifecycle record. Err is set for rejected promises and
// for allocation failures.
type Event struct {
	Kind                 EventKind
	ConnectionGeneration uint64
	ParentStreamID       uint32
	PushStreamID         uint32
	Source               Source
	Reason               string
	Err                  error
	Time                 time.Time
}

// Stats contains monotonic counters. Parent cancellations and direct push
// cancellations are deliberately separate counters.
type Stats struct {
	ParentsRegistered      uint64
	ParentCancellations    uint64
	ParentCompletions      uint64
	PushesAdmitted         uint64
	PushCancellations      uint64
	PushesCanceledByParent uint64
	PushesCompleted        uint64
	PromiseRejections      uint64
	AllocationFailures     uint64
	AdmissionClosures      uint64
}

type eventLog struct {
	buf   []Event
	next  int
	full  bool
	limit int
}

func newEventLog(limit int) eventLog {
	if limit < 0 {
		limit = 0
	}
	return eventLog{limit: limit}
}

func (l *eventLog) add(event Event) {
	if l.limit == 0 {
		return
	}
	if len(l.buf) < l.limit {
		l.buf = append(l.buf, event)
		return
	}
	l.buf[l.next] = event
	l.next = (l.next + 1) % l.limit
	l.full = true
}

func (l *eventLog) snapshot() []Event {
	if len(l.buf) == 0 {
		return nil
	}
	if !l.full {
		out := make([]Event, len(l.buf))
		copy(out, l.buf)
		return out
	}
	out := make([]Event, 0, len(l.buf))
	out = append(out, l.buf[l.next:]...)
	out = append(out, l.buf[:l.next]...)
	return out
}
