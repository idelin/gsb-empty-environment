package netsec

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// TenantID identifies a restricted process whose signals are billed together.
type TenantID string

// TargetID identifies a host-managed signal recipient.
type TargetID string

// Signal mirrors the POSIX split between standard and realtime signals.
// Signals 1..31 are standard and coalesce; signals 32..64 are realtime and
// are queued individually with their payload.
type Signal int

const (
	minStandard Signal = 1
	maxStandard Signal = 31
	minRealtime Signal = 32
	maxRealtime Signal = 64
)

// IsStandard reports whether the signal coalesces per target and generation.
func (s Signal) IsStandard() bool {
	return s >= minStandard && s <= maxStandard
}

// IsRealtime reports whether the signal queues individually with a payload.
func (s Signal) IsRealtime() bool {
	return s >= minRealtime && s <= maxRealtime
}

func (s Signal) valid() bool {
	return s >= minStandard && s <= maxRealtime
}

// Envelope is one queued signal request. The tenant identity and the target
// generation captured at enqueue time are immutable; delivery re-checks the
// policy against exactly these values.
type Envelope struct {
	Tenant  TenantID
	Target  TargetID
	Signal  Signal
	Payload []byte
	Gen     uint64

	// Coalesced counts extra standard-signal sends folded into this envelope.
	Coalesced int
}

// QueuedEnvelope is the handle returned by Send, wrapping the delivered
// Envelope with host-internal bookkeeping. It is never copied across the
// receiver channel; only the embedded Envelope value leaves the host.
type QueuedEnvelope struct {
	Envelope

	elem       *list.Element
	tenant     *tenantState
	target     *targetState
	released   bool
	canceled   bool
	coalescedN atomic.Int64

	releaseMu sync.Mutex
}

// addCoalesced records one more same-kind standard send folded into this
// envelope. It is safe while the pump is blocked delivering the envelope.
func (q *QueuedEnvelope) addCoalesced() {
	q.coalescedN.Add(1)
}

// snapshot returns an immutable Envelope value safe to copy across the
// receiver channel concurrently with further coalescing.
func (q *QueuedEnvelope) snapshot() Envelope {
	delivered := q.Envelope
	delivered.Coalesced = int(q.coalescedN.Load())
	return delivered
}

// coalescedCount returns the number of same-kind sends merged so far.
func (q *QueuedEnvelope) coalescedCount() int {
	return int(q.coalescedN.Load())
}

// release returns the tenant's slot at most once, whether delivery or
// cancellation wins the race.
func (q *QueuedEnvelope) release(h *Host) {
	q.releaseMu.Lock()
	if q.released {
		q.releaseMu.Unlock()
		return
	}
	q.released = true
	q.releaseMu.Unlock()

	h.mu.Lock()
	q.tenant.used--
	if q.tenant.used < 0 {
		panic("netsec: negative tenant queue usage")
	}
	if h.used > 0 {
		h.used--
	}
	h.cond.Broadcast()
	h.mu.Unlock()
}

// isReleased reports whether the slot was already released.
func (q *QueuedEnvelope) isReleased() bool {
	q.releaseMu.Lock()
	defer q.releaseMu.Unlock()
	return q.released
}

// Authorizer decides whether a tenant may queue or receive a signal for a
// specific target identity generation. Implementations must be safe for
// concurrent use.
type Authorizer interface {
	Authorize(tenant TenantID, target TargetID, generation uint64, sig Signal) bool
}

// AuthRule is one authorization entry used by RuleAuthorizer.
type AuthRule struct {
	Tenant     TenantID
	Target     TargetID
	Generation uint64
	Signal     Signal
}

// RuleAuthorizer is a goroutine-safe set-based Authorizer intended for tests
// and simple deployments. Rules are keyed by the full identity generation, so
// revoking a rule or requiring a fresh generation re-checks at delivery time.
type RuleAuthorizer struct {
	mu    sync.RWMutex
	rules map[AuthRule]struct{}
}

// NewRuleAuthorizer builds an authorizer containing the given rules.
func NewRuleAuthorizer(rules ...AuthRule) *RuleAuthorizer {
	a := &RuleAuthorizer{rules: make(map[AuthRule]struct{}, len(rules))}
	for _, rule := range rules {
		a.rules[rule] = struct{}{}
	}
	return a
}

// Authorize implements Authorizer.
func (a *RuleAuthorizer) Authorize(tenant TenantID, target TargetID, generation uint64, sig Signal) bool {
	a.mu.RLock()
	_, ok := a.rules[AuthRule{Tenant: tenant, Target: target, Generation: generation, Signal: sig}]
	a.mu.RUnlock()
	return ok
}

// Grant adds an authorization rule.
func (a *RuleAuthorizer) Grant(rule AuthRule) {
	a.mu.Lock()
	a.rules[rule] = struct{}{}
	a.mu.Unlock()
}

// Revoke removes an authorization rule.
func (a *RuleAuthorizer) Revoke(rule AuthRule) {
	a.mu.Lock()
	delete(a.rules, rule)
	a.mu.Unlock()
}

// tenantState holds the per-tenant billing counters.
type tenantState struct {
	quota int
	used  int
}

// Stats is a point-in-time snapshot of host accounting.
type Stats struct {
	// Used is the number of global queue slots currently occupied.
	Used int
	// Capacity is the total number of queue slots.
	Capacity int
	// Tenants maps tenant ID to currently occupied slots.
	Tenants map[TenantID]int
}
