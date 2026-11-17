package netsec

import (
	"sync"
	"time"
)

// EventKind classifies an audit event.
type EventKind string

const (
	// EventDeclassification records a declassification that was committed.
	EventDeclassification EventKind = "declassification"
	// EventDeclassificationRolledBack records a declassification whose
	// transaction failed and therefore did not consume its authorization.
	EventDeclassificationRolledBack EventKind = "declassification_rolled_back"
	// EventOutputPublished records a public output that was committed.
	EventOutputPublished EventKind = "output_published"
	// EventOutputDenied records an output rejected because its label was not
	// empty.
	EventOutputDenied EventKind = "output_denied"
	// EventAuthorizationDenied records a missing, forged, or mismatched
	// authorization.
	EventAuthorizationDenied EventKind = "authorization_denied"
	// EventAuthorizationReuse records a repeated or already-consumed
	// authorization.
	EventAuthorizationReuse EventKind = "authorization_reuse"
	// EventExecutionFailed records the final failed transaction.
	EventExecutionFailed EventKind = "execution_failed"
)

// Event is one audit record. It never contains secret values; labels and
// source sets are metadata, and Value is populated only for committed public
// output.
type Event struct {
	Time        time.Time
	RequestID   uint64
	Kind        EventKind
	NodeID      uint64
	Label       Label
	Allowed     Label
	Remaining   Label
	OutputIndex int
	Value       int
	Reason      string
}

// AuditLog is a concurrency-safe append-only event log.
type AuditLog struct {
	mu     sync.Mutex
	events []Event
}

// NewAuditLog creates an empty audit log.
func NewAuditLog() *AuditLog { return &AuditLog{} }

// Append adds one event.
func (l *AuditLog) Append(event Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *AuditLog) appendAll(events []Event) {
	if l == nil || len(events) == 0 {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, events...)
	l.mu.Unlock()
}

// Events returns a snapshot of all events.
func (l *AuditLog) Events() []Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// Len returns the number of recorded events.
func (l *AuditLog) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}
