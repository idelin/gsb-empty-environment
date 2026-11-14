package netsec

import (
	"context"
	"sync"
)

// emitLocked appends an audit event to the in-memory trail and returns it for
// external delivery. The caller must hold s.mu.
func (s *State) emitLocked(eventType, holderID string, shareCount int, cause error) AuditEvent {
	s.auditSeq++
	event := AuditEvent{
		Seq:        s.auditSeq,
		Type:       eventType,
		Generation: s.generation,
		SealID:     s.seal.SealID,
		HolderID:   holderID,
		Phase:      s.phase,
		ShareCount: shareCount,
		Threshold:  s.seal.Threshold,
		Time:       timeNow(),
	}
	if cause != nil {
		event.Cause = cause.Error()
	}
	s.events = append(s.events, event)
	return event
}

// deliver records and notifies outside the state lock so slow sinks cannot
// block other operations or deadlock back into the state machine.
func (s *State) deliver(ctx context.Context, events []AuditEvent) {
	for _, event := range events {
		if s.sink != nil {
			_ = s.sink.Record(ctx, event)
		}
		if s.notifier != nil {
			_ = s.notifier.Notify(ctx, event)
		}
	}
}

// Phase returns the current lifecycle phase.
func (s *State) Phase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// Generation returns the active generation, or zero when uninitialized.
func (s *State) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// IsUnsealed reports whether the active generation has been unsealed.
func (s *State) IsUnsealed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase == phaseUnsealed
}

// PendingCount returns the number of distinct validated shares recorded for
// the active generation.
func (s *State) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.received)
}

// Threshold returns the active threshold, or zero when uninitialized.
func (s *State) Threshold() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seal.Threshold
}

// Nonce returns a copy of the active sealing nonce.
func (s *State) Nonce() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.nonce...)
}

// SealConfigView returns a copy of the active seal configuration.
func (s *State) SealConfigView() SealConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSealConfig(s.seal)
}

// MaxObservedGeneration returns the generation high-water mark, which is used
// to reject rollback attempts across rotations and snapshot imports.
func (s *State) MaxObservedGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxObservedGeneration
}

// AuditEvents returns a copy of the in-memory audit trail.
func (s *State) AuditEvents() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, len(s.events))
	copy(out, s.events)
	return out
}

// MemoryAuditSink is a concurrency-safe AuditSink that retains events in
// memory. It is suitable for tests and as a fan-in adapter for real sinks.
type MemoryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Record appends an event to the sink.
func (m *MemoryAuditSink) Record(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
	return nil
}

// Events returns a copy of the recorded events.
func (m *MemoryAuditSink) Events() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}

// SliceNotifier fans audit events out to in-memory subscribers for tests and
// alert adapters.
type SliceNotifier struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Notify appends an event to the notifier.
func (n *SliceNotifier) Notify(_ context.Context, event AuditEvent) error {
	n.mu.Lock()
	n.events = append(n.events, event)
	n.mu.Unlock()
	return nil
}

// Events returns a copy of the notified events.
func (n *SliceNotifier) Events() []AuditEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]AuditEvent, len(n.events))
	copy(out, n.events)
	return out
}
