package netsec

import (
	"context"
	"time"
)

// AuditEvent describes a state protection decision. It never contains state
// payloads, credentials, lock tokens, or key material.
type AuditEvent struct {
	Time        time.Time
	Action      string
	Allowed     bool
	CallerID    string
	WorkspaceID string
	AccountID   string
	Detail      string
	Error       string
}

// AuditSink receives security-relevant decisions. Implementations must not
// block indefinitely and must treat event fields as untrusted text.
type AuditSink interface {
	Record(ctx context.Context, event AuditEvent)
}

// AuditFunc adapts a function to AuditSink.
type AuditFunc func(ctx context.Context, event AuditEvent)

// Record implements AuditSink.
func (f AuditFunc) Record(ctx context.Context, event AuditEvent) {
	f(ctx, event)
}

func (s *Store) audit(ctx context.Context, action string, caller Caller, allowed bool, detail string, err error) {
	if s.auditSink == nil {
		return
	}
	event := AuditEvent{
		Time:        s.now(),
		Action:      action,
		Allowed:     allowed,
		CallerID:    caller.ID,
		WorkspaceID: caller.WorkspaceID,
		AccountID:   caller.AccountID,
		Detail:      detail,
	}
	if err != nil {
		event.Error = err.Error()
	}
	s.auditSink.Record(ctx, event)
}
