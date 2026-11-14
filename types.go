package netsec

import (
	"context"
	"strings"
	"time"
)

// RequestKind is the action a data subject asked the service to perform.
type RequestKind string

const (
	RequestExport RequestKind = "export"
	RequestDelete RequestKind = "delete"
)

func (k RequestKind) valid() bool {
	return k == RequestExport || k == RequestDelete
}

// SystemKind identifies the role of a copy system. Every registered system is
// required to produce a receipt for every request, including systems that hold
// no data for the subject (they return a zero-record receipt).
type SystemKind string

const (
	SystemPrimary     SystemKind = "primary"
	SystemBackup      SystemKind = "backup"
	SystemCache       SystemKind = "cache"
	SystemSearchIndex SystemKind = "search_index"
	SystemDerived     SystemKind = "derived"
)

func (k SystemKind) valid() bool {
	switch k {
	case SystemPrimary, SystemBackup, SystemCache, SystemSearchIndex, SystemDerived:
		return true
	default:
		return false
	}
}

// StandardSystemKinds returns the complete set of copy roles a compliant
// deployment is expected to register.
func StandardSystemKinds() []SystemKind {
	return []SystemKind{
		SystemPrimary,
		SystemBackup,
		SystemCache,
		SystemSearchIndex,
		SystemDerived,
	}
}

// Subject identifies the natural person who made the request. ID is opaque to
// this package and is never included in exported package logs.
type Subject struct {
	ID string
}

// Scope limits the request to a set of personal-data categories. An empty
// Categories slice means all personal data about the subject. A scope never
// limits which copy systems are contacted: every registered system must
// acknowledge the request so a backup, cache or index cannot be omitted.
type Scope struct {
	Categories []string
}

func (s Scope) clone() Scope {
	if s.Categories == nil {
		return Scope{}
	}
	return Scope{Categories: append([]string(nil), s.Categories...)}
}

func normalizeScope(s Scope) (Scope, error) {
	if len(s.Categories) == 0 {
		return Scope{}, nil
	}

	normalized := make([]string, 0, len(s.Categories))
	seen := make(map[string]struct{}, len(s.Categories))
	for _, category := range s.Categories {
		category = strings.TrimSpace(category)
		if category == "" {
			return Scope{}, invalidRequestf("scope contains an empty data category")
		}
		if _, ok := seen[category]; ok {
			continue
		}
		seen[category] = struct{}{}
		normalized = append(normalized, category)
	}
	return Scope{Categories: normalized}, nil
}

// Request is the immutable registration record for a data subject request.
type Request struct {
	ID         string
	Subject    Subject
	Kind       RequestKind
	Scope      Scope
	Deadline   time.Time
	CreatedAt  time.Time
	VerifiedAt time.Time
}

// Registration is the input to Tracker.Register. The identity proof is
// verified before any request state is created.
type Registration struct {
	Subject  Subject
	Kind     RequestKind
	Scope    Scope
	Deadline time.Time
	Proof    IdentityProof
}

// CopyTask is the unit of work handed to a copy-system handler. Attempt starts
// at one. A handler must return a receipt describing what it processed; if it
// holds no matching data it should return a zero-record receipt rather than
// skipping the system.
type CopyTask struct {
	RequestID  string
	Subject    Subject
	Action     RequestKind
	Scope      Scope
	Deadline   time.Time
	SystemID   string
	SystemKind SystemKind
	Attempt    int
}

// Receipt is proof that one copy system processed one request. The tracker
// fills CompletedAt when a handler leaves it zero and rejects receipts whose
// request, system, subject or action metadata does not match the task.
type Receipt struct {
	RequestID       string
	SystemID        string
	SubjectID       string
	Action          RequestKind
	CompletedAt     time.Time
	RecordsAffected int64
	Evidence        string
}

// Receipt returns a receipt with its identifying fields pre-populated from the
// task. Handlers only need to add RecordsAffected, Evidence and optionally
// CompletedAt.
func (t CopyTask) Receipt() Receipt {
	return Receipt{
		RequestID: t.RequestID,
		SystemID:  t.SystemID,
		SubjectID: t.Subject.ID,
		Action:    t.Action,
	}
}

// CopyHandler processes one request in one copy system.
type CopyHandler interface {
	Process(ctx context.Context, task CopyTask) (Receipt, error)
}

var _ CopyHandler = CopyHandlerFunc(nil)

// CopyHandlerFunc adapts a function to CopyHandler.
type CopyHandlerFunc func(ctx context.Context, task CopyTask) (Receipt, error)

// Process implements CopyHandler.
func (f CopyHandlerFunc) Process(ctx context.Context, task CopyTask) (Receipt, error) {
	return f(ctx, task)
}

// System describes a copy system and its handler.
type System struct {
	ID      string
	Kind    SystemKind
	Handler CopyHandler
}

// SystemInfo is a handler-free description of a registered copy system.
type SystemInfo struct {
	ID   string
	Kind SystemKind
}

// ItemState is the processing state of one copy system.
type ItemState string

const (
	ItemPending   ItemState = "pending"
	ItemRetrying  ItemState = "retrying"
	ItemCompleted ItemState = "completed"
	ItemFailed    ItemState = "failed"
)

// ItemStatus is a point-in-time view of one copy system.
type ItemStatus struct {
	SystemID      string
	SystemKind    SystemKind
	State         ItemState
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	CompletedAt   time.Time
	Receipt       *Receipt
}

// RequestState is the aggregate state of a data subject request.
type RequestState string

const (
	// RequestPending means at least one system is still eligible for retry.
	RequestPending RequestState = "pending"
	// RequestBlocked means at least one system exhausted its retry budget.
	RequestBlocked RequestState = "blocked"
	// RequestCompleted means every registered system returned a valid
	// receipt.
	RequestCompleted RequestState = "completed"
)

// RequestStatus is an immutable snapshot of a request and all of its copy
// systems. Complete is true only when every registered system has a receipt.
type RequestStatus struct {
	Request          Request
	State            RequestState
	Complete         bool
	Overdue          bool
	OverdueBy        time.Duration
	OverdueAlertedAt time.Time
	CompletedAt      time.Time
	Items            []ItemStatus
	MissingSystems   []string
}

// AlertKind categorizes an operational alert.
type AlertKind string

const (
	AlertOverdue AlertKind = "overdue"
)

// Alert is emitted when a request misses its registered deadline.
type Alert struct {
	ID             string
	Kind           AlertKind
	RequestID      string
	SubjectID      string
	Action         RequestKind
	Deadline       time.Time
	CreatedAt      time.Time
	OverdueBy      time.Duration
	MissingSystems []string
	Message        string
}

func (a Alert) clone() Alert {
	a.MissingSystems = append([]string(nil), a.MissingSystems...)
	return a
}

// AlertSink receives alerts. Returning an error leaves the alert pending so a
// later Tick can retry delivery.
type AlertSink interface {
	Send(ctx context.Context, alert Alert) error
}

var _ AlertSink = AlertSinkFunc(nil)

// AlertSinkFunc adapts a function to AlertSink.
type AlertSinkFunc func(ctx context.Context, alert Alert) error

// Send implements AlertSink.
func (f AlertSinkFunc) Send(ctx context.Context, alert Alert) error {
	return f(ctx, alert)
}

// AlertRecord is the delivery state of an overdue alert.
type AlertRecord struct {
	Alert       Alert
	DeliveredAt time.Time
	LastError   string
}
