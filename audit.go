package netsec

import (
	"context"
	"sync"
	"time"
)

// AuditEventKind classifies resolution audit records.
type AuditEventKind string

const (
	AuditSourceSelected   AuditEventKind = "source_selected"
	AuditCacheHit         AuditEventKind = "cache_hit"
	AuditCacheMiss        AuditEventKind = "cache_miss"
	AuditRepositoryFetch  AuditEventKind = "repository_fetch"
	AuditRepositoryFailed AuditEventKind = "repository_failed"
	AuditFallback         AuditEventKind = "fallback"
	AuditFallbackBlocked  AuditEventKind = "fallback_blocked"
	AuditDegraded         AuditEventKind = "degraded"
	AuditMetadataIgnored  AuditEventKind = "metadata_override_ignored"
	AuditConflict         AuditEventKind = "source_conflict"
	AuditRejected         AuditEventKind = "rejected"
	AuditVerified         AuditEventKind = "verified"
	AuditCacheWriteFailed AuditEventKind = "cache_write_failed"
)

// AuditEvent is one structured resolution record. The result also contains
// the same events, so callers can persist them transactionally.
type AuditEvent struct {
	At         time.Time      `json:"at"`
	Kind       AuditEventKind `json:"kind"`
	ModulePath string         `json:"module_path,omitempty"`
	Version    string         `json:"version,omitempty"`
	SourceID   string         `json:"source_id,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Detail     string         `json:"detail,omitempty"`
}

// Auditor receives audit events as they occur, including failed resolutions.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent)
}

// AuditFunc adapts a function to Auditor.
type AuditFunc func(ctx context.Context, event AuditEvent)

// Record implements Auditor.
func (f AuditFunc) Record(ctx context.Context, event AuditEvent) {
	f(ctx, event)
}

// AuditLog is a concurrency-safe in-memory Auditor/event collector.
type AuditLog struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Record implements Auditor.
func (l *AuditLog) Record(_ context.Context, event AuditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

// Snapshot returns a copy of all recorded events.
func (l *AuditLog) Snapshot() []AuditEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEvent, len(l.events))
	copy(out, l.events)
	return out
}
