package netsec

import (
	"context"
	"sync"
	"time"
)

// AuthMechanism identifies the bind performed against a referral target.
type AuthMechanism string

const (
	AuthNone            AuthMechanism = ""
	AuthSimple          AuthMechanism = "simple"
	AuthAnonymousSimple AuthMechanism = "anonymous-simple"
)

// AuditEvent records a referral decision or bind. It never contains a
// password or any other credential material.
type AuditEvent struct {
	Time                time.Time     `json:"time"`
	Kind                string        `json:"kind"`
	Source              string        `json:"source,omitempty"`
	From                string        `json:"from,omitempty"`
	Target              string        `json:"target,omitempty"`
	Host                string        `json:"host,omitempty"`
	Port                int           `json:"port,omitempty"`
	Scheme              Scheme        `json:"scheme,omitempty"`
	Transport           TLSMode       `json:"transport,omitempty"`
	DN                  string        `json:"dn,omitempty"`
	BoundDN             string        `json:"bound_dn,omitempty"`
	AuthMechanism       AuthMechanism `json:"auth_mechanism,omitempty"`
	CredentialForwarded bool          `json:"credential_forwarded"`
	Followable          bool          `json:"followable"`
	Action              string        `json:"action,omitempty"`
	Reason              string        `json:"reason,omitempty"`
	Error               string        `json:"error,omitempty"`
	Depth               int           `json:"depth,omitempty"`
	Forest              string        `json:"forest,omitempty"`
	CrossForest         bool          `json:"cross_forest"`
}

// AuditSink receives security-relevant referral events.
type AuditSink interface {
	Record(context.Context, AuditEvent)
}

// AuditFunc adapts a function to AuditSink.
type AuditFunc func(context.Context, AuditEvent)

func (f AuditFunc) Record(ctx context.Context, event AuditEvent) { f(ctx, event) }

// MemoryAudit is an in-memory AuditSink suitable for tests and diagnostics.
// Production callers should provide a durable, concurrency-safe sink.
type MemoryAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (m *MemoryAudit) Record(_ context.Context, event AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

// Events returns a snapshot of recorded events.
func (m *MemoryAudit) Events() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}
