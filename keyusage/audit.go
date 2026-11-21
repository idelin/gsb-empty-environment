package keyusage

import (
	"sync"
	"time"
)

// AuditEvent is recorded for every rejected call.
type AuditEvent struct {
	Time      time.Time
	KeyID     string
	Algorithm Algorithm
	Purpose   Purpose
	Target    Target
	Reason    string
	Err       error
}

// AuditSink receives rejection events. Implementations must be safe for
// concurrent use.
type AuditSink interface {
	Record(AuditEvent)
}

// MemoryAuditSink stores rejected-call events in memory. It is safe for
// concurrent use.
type MemoryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Record appends an event.
func (s *MemoryAuditSink) Record(event AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

// Events returns a copy of all recorded rejection events.
func (s *MemoryAuditSink) Events() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEvent(nil), s.events...)
}

// Len returns the number of recorded rejection events.
func (s *MemoryAuditSink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}
