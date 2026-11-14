package netsec

import (
	"sync"
	"time"
)

// AuditEventKind classifies audit records.
type AuditEventKind string

const (
	// AuditIntent is recorded for every evaluated intent, allowed or not.
	AuditIntent AuditEventKind = "intent"
	// AuditPolicyChange is recorded for every policy upsert or delete.
	AuditPolicyChange AuditEventKind = "policy_change"
)

// AuditEvent is one append-only audit record.
type AuditEvent struct {
	Kind     AuditEventKind
	At       time.Time
	TokenID  string
	Intent   *Intent
	Decision *Decision
	PolicyID string
	Actor    string
	Change   string
	Detail   string
}

// AuditSink receives audit events. Implementations must be safe for
// concurrent use.
type AuditSink interface {
	Record(AuditEvent)
}

// MemorySink is an in-memory AuditSink, handy for tests and as a
// building block for persistent sinks.
type MemorySink struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Record appends the event to the in-memory log.
func (m *MemorySink) Record(e AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

// Events returns a copy of all recorded events in order.
func (m *MemorySink) Events() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}
