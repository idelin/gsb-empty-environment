package netsec

import (
	"container/list"
	"fmt"
	"sync"
)

// Config configures a Host.
type Config struct {
	// Capacity is the total number of queued envelopes across all tenants.
	Capacity int
	// ReservedSlots are kept available for tenants that have not used their
	// guaranteed share. A single tenant can never occupy more than
	// Capacity-ReservedSlots slots while reserved slots remain protected.
	ReservedSlots int
	// TenantQuota caps envelopes billed to one tenant. Zero defaults to
	// Capacity, leaving admission governed by the shared limits.
	TenantQuota map[TenantID]int
	// MaxPayloadBytes bounds realtime signal payloads. Zero means no limit.
	MaxPayloadBytes int
}

type coalesceKey struct {
	target TargetID
	gen    uint64
	sig    Signal
}

type targetState struct {
	id      TargetID
	gen     uint64
	ch      chan Envelope
	recheck chan struct{}
	stop    chan struct{}
	stopped bool
}

// Host is a signal delivery host for restricted senders.
type Host struct {
	auth Authorizer
	cfg  Config

	mu      sync.Mutex
	cond    *sync.Cond
	queue   *list.List
	pending map[coalesceKey]*QueuedEnvelope
	targets map[TargetID]*targetState
	tenants map[TenantID]*tenantState
	used    int
	closed  bool

	pumps sync.WaitGroup
}

// New constructs a Host with the given policy and limits.
func New(auth Authorizer, cfg Config) (*Host, error) {
	if auth == nil {
		return nil, fmt.Errorf("netsec: nil authorizer")
	}
	if cfg.Capacity <= 0 {
		return nil, fmt.Errorf("netsec: capacity must be positive")
	}
	if cfg.ReservedSlots < 0 || cfg.ReservedSlots >= cfg.Capacity {
		return nil, fmt.Errorf("netsec: reserved slots must be in [0, capacity)")
	}
	tenants := make(map[TenantID]*tenantState, len(cfg.TenantQuota))
	for tenant, quota := range cfg.TenantQuota {
		if quota <= 0 {
			return nil, fmt.Errorf("netsec: tenant %q quota must be positive", tenant)
		}
		if quota > cfg.Capacity {
			quota = cfg.Capacity
		}
		tenants[tenant] = &tenantState{quota: quota}
	}
	h := &Host{
		auth:    auth,
		cfg:     cfg,
		queue:   list.New(),
		pending: make(map[coalesceKey]*QueuedEnvelope),
		targets: make(map[TargetID]*targetState),
		tenants: tenants,
	}
	h.cond = sync.NewCond(&h.mu)
	return h, nil
}

// Register creates a target with an unbuffered blocking receiver. The
// returned channel delivers envelopes one at a time; a slow receiver applies
// back pressure without blocking other targets.
func (h *Host) Register(id TargetID) (chan Envelope, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrAlreadyClosed
	}
	if _, exists := h.targets[id]; exists {
		return nil, fmt.Errorf("netsec: target %q already registered", id)
	}
	target := &targetState{
		id:      id,
		gen:     1,
		ch:      make(chan Envelope),
		recheck: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	h.targets[id] = target
	h.pumps.Add(1)
	go h.pump(target)
	return target.ch, nil
}

// Restart models a target process restart: it gets a fresh identity
// generation. Envelopes pinned to previous generations are invalidated and
// release their slots without being delivered. The delivery channel is
// replaced, so receivers must re-fetch it.
func (h *Host) Restart(id TargetID) (chan Envelope, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrAlreadyClosed
	}
	target, ok := h.targets[id]
	if !ok {
		h.mu.Unlock()
		return nil, ErrUnknownTarget
	}

	close(target.stop)
	target.stopped = true
	invalidated := h.invalidateLocked(target)

	fresh := &targetState{
		id:      id,
		gen:     target.gen + 1,
		ch:      make(chan Envelope),
		recheck: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	h.targets[id] = fresh
	h.mu.Unlock()

	for _, envelope := range invalidated {
		envelope.release(h)
	}

	h.pumps.Add(1)
	go h.pump(fresh)
	return fresh.ch, nil
}

// Unregister stops a target and releases every envelope still queued for it.
func (h *Host) Unregister(id TargetID) error {
	h.mu.Lock()
	target, ok := h.targets[id]
	if !ok {
		h.mu.Unlock()
		return ErrUnknownTarget
	}
	delete(h.targets, id)
	close(target.stop)
	target.stopped = true
	invalidated := h.invalidateLocked(target)
	h.mu.Unlock()

	for _, envelope := range invalidated {
		envelope.release(h)
	}
	return nil
}

// invalidateLocked removes all queued envelopes pinned to the target's current
// generation and returns them for slot release. Standard-signal coalescing
// entries are removed as well.
func (h *Host) invalidateLocked(target *targetState) []*QueuedEnvelope {
	var dropped []*QueuedEnvelope
	for element := h.queue.Front(); element != nil; {
		next := element.Next()
		envelope := element.Value.(*QueuedEnvelope)
		if envelope.target == target {
			h.queue.Remove(element)
			envelope.elem = nil
			delete(h.pending, coalesceKey{target: target.id, gen: target.gen, sig: envelope.Signal})
			dropped = append(dropped, envelope)
		}
		element = next
	}
	return dropped
}

// Send queues a signal from a tenant. Authorization and payload limits are
// enforced here, and the target generation is fixed on the returned
// envelope. Standard signals merge into a pending same-kind envelope; realtime
// signals always occupy their own slot.
func (h *Host) Send(tenant TenantID, targetID TargetID, sig Signal, payload []byte) (*QueuedEnvelope, error) {
	if !sig.valid() {
		return nil, ErrInvalidSignal
	}
	if sig.IsStandard() && len(payload) > 0 {
		return nil, ErrStandardPayload
	}
	if h.cfg.MaxPayloadBytes > 0 && len(payload) > h.cfg.MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrAlreadyClosed
	}
	tenantState, ok := h.tenants[tenant]
	if !ok {
		h.mu.Unlock()
		return nil, ErrTenantUnknown
	}
	target, ok := h.targets[targetID]
	if !ok {
		h.mu.Unlock()
		return nil, ErrUnknownTarget
	}

	// Identity generation is fixed at enqueue; the same value is re-checked
	// at delivery.
	generation := target.gen
	if !h.auth.Authorize(tenant, targetID, generation, sig) {
		h.mu.Unlock()
		return nil, ErrNotAuthorized
	}

	key := coalesceKey{target: targetID, gen: generation, sig: sig}
	if sig.IsStandard() {
		if existing, ok := h.pending[key]; ok {
			existing.addCoalesced()
			h.mu.Unlock()
			return existing, nil
		}
	}

	if err := h.admitLocked(tenantState); err != nil {
		h.mu.Unlock()
		return nil, err
	}

	var storedPayload []byte
	if len(payload) > 0 {
		storedPayload = append([]byte(nil), payload...)
	}
	envelope := &QueuedEnvelope{Envelope: Envelope{
		Tenant:  tenant,
		Target:  targetID,
		Signal:  sig,
		Payload: storedPayload,
		Gen:     generation,
	},
		tenant: tenantState,
		target: target,
	}
	tenantState.used++
	h.used++
	envelope.elem = h.queue.PushBack(envelope)
	if sig.IsStandard() {
		h.pending[key] = envelope
	}
	h.cond.Broadcast()
	h.mu.Unlock()
	return envelope, nil
}

// admitLocked enforces per-tenant quota, global capacity, and the reserved
// slot guarantee. The reservation defines a per-tenant cap of
// Capacity-ReservedSlots: no single tenant can ever consume the protected
// slots, while any tenant within its cap can use shared capacity.
func (h *Host) admitLocked(tenantState *tenantState) error {
	if tenantState.used >= tenantState.quota {
		return ErrTenantQuota
	}
	if h.used >= h.cfg.Capacity {
		return ErrCapacity
	}
	reserved := h.cfg.ReservedSlots
	if reserved > 0 {
		perTenantCap := h.cfg.Capacity - reserved
		if tenantState.used >= perTenantCap {
			return ErrReservedSlots
		}
	}
	return nil
}

// Cancel removes a queued envelope and releases its tenant slot at most once.
// Coalesced standard signals are canceled together with their single envelope.
func (h *Host) Cancel(envelope *QueuedEnvelope) error {
	h.mu.Lock()
	if h.closed || envelope.isReleased() || envelope.canceled {
		h.mu.Unlock()
		return ErrCanceled
	}
	target := envelope.target
	inFlight := envelope.elem == nil
	if envelope.elem != nil {
		// Still queued: remove it immediately and free the slot.
		h.queue.Remove(envelope.elem)
		envelope.elem = nil
		delete(h.pending, coalesceKey{target: envelope.Target, gen: envelope.Gen, sig: envelope.Signal})
	}
	if inFlight {
		envelope.canceled = true
		h.clearCoalescingLocked(envelope)
	}
	h.mu.Unlock()

	envelope.release(h)
	if inFlight {
		// The pump had already popped the envelope; wake it so the blocked
		// authorization call or receiver send observes the canceled flag. A
		// pending wakeup is sufficient because the pump rechecks the flag.
		select {
		case target.recheck <- struct{}{}:
		default:
		}
	}
	return nil
}

// RefreshAuth wakes every target pump so queued envelopes re-run delivery-time
// authorization against the current policy. Envelopes that fail remain queued
// (still billed to their tenant and in order) until the policy passes again or
// the target is restarted/unregistered.
func (h *Host) RefreshAuth() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	targets := make([]*targetState, 0, len(h.targets))
	for _, target := range h.targets {
		targets = append(targets, target)
	}
	h.mu.Unlock()

	for _, target := range targets {
		select {
		case target.recheck <- struct{}{}:
		default:
		}
	}
}

// Stats returns a snapshot of queue accounting.
func (h *Host) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	tenants := make(map[TenantID]int, len(h.tenants))
	for tenant, state := range h.tenants {
		tenants[tenant] = state.used
	}
	return Stats{Used: h.used, Capacity: h.cfg.Capacity, Tenants: tenants}
}

// Close stops all target pumps and releases every queued envelope.
func (h *Host) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	targets := make([]*targetState, 0, len(h.targets))
	var queued []*QueuedEnvelope
	for _, target := range h.targets {
		targets = append(targets, target)
		target.stopped = true
		queued = append(queued, h.invalidateLocked(target)...)
	}
	h.targets = nil
	h.cond.Broadcast()
	h.mu.Unlock()

	for _, target := range targets {
		close(target.stop)
	}
	for _, envelope := range queued {
		envelope.release(h)
	}
	h.pumps.Wait()
}

// requeueLocked puts an envelope that failed the delivery-time check back at
// the front so ordering is preserved, and restores its coalescing entry.
func (h *Host) requeueLocked(envelope *QueuedEnvelope) {
	envelope.elem = h.queue.PushFront(envelope)
	if envelope.Signal.IsStandard() {
		h.pending[coalesceKey{target: envelope.Target, gen: envelope.Gen, sig: envelope.Signal}] = envelope
	}
}

// pump is the per-target delivery goroutine. Each target owns its own pump so
// a blocked receiver only stalls that target, never the whole host.
func (h *Host) pump(target *targetState) {
	defer h.pumps.Done()

	for {
		select {
		case <-target.stop:
			return
		default:
		}

		h.mu.Lock()
		for !h.closed && !h.hasAnyLocked(target) {
			h.cond.Wait()
		}
		if h.closed {
			h.mu.Unlock()
			return
		}
		envelope := h.frontForLocked(target)
		// Remove only this target's head envelope; envelopes ahead of it
		// belong to other pumps and must stay in FIFO order.
		h.queue.Remove(envelope.elem)
		envelope.elem = nil
		// Keep the standard-signal coalescing key in place while the envelope
		// is in flight so same-kind sends still merge against a blocked
		// receiver; it is cleared on completion, cancel, or restart.
		h.mu.Unlock()

		// Re-check authorization at delivery, outside the host lock, against
		// the generation fixed at enqueue. A failure parks the envelope; a
		// concurrent cancel or target stop abandons this in-flight envelope.
		authorized, canceled := h.authorizeOrCancel(envelope, target)
		h.mu.Lock()
		stopped := target.stopped
		closed := h.closed
		if closed || envelope.isReleased() || stopped || canceled {
			h.clearCoalescingLocked(envelope)
			h.mu.Unlock()
			envelope.release(h)
			if stopped || closed {
				return
			}
			continue
		}
		if !authorized {
			h.requeueLocked(envelope)
			h.mu.Unlock()
			h.waitRecheckOrStop(target)
			continue
		}
		h.mu.Unlock()

		// Blocking send: the slot stays billed until the receiver actually
		// takes the envelope, and stop/recheck unblock restart or cancel. The
		// mutable coalescing counter is read atomically inside snapshot().
		select {
		case target.ch <- envelope.snapshot():
			h.mu.Lock()
			h.clearCoalescingLocked(envelope)
			h.mu.Unlock()
			envelope.release(h)
		case <-target.recheck:
			// A cancel raced the blocked send; an auth refresh retries.
			h.mu.Lock()
			canceled = envelope.canceled
			if canceled {
				h.clearCoalescingLocked(envelope)
			}
			h.mu.Unlock()
			if !canceled {
				continue
			}
			envelope.release(h)
		case <-target.stop:
			h.mu.Lock()
			h.clearCoalescingLocked(envelope)
			h.mu.Unlock()
			envelope.release(h)
			return
		}
	}
}

// clearCoalescingLocked drops the standard-signal merge key once an in-flight
// envelope reaches a terminal delivery, cancel, or stop state.
func (h *Host) clearCoalescingLocked(envelope *QueuedEnvelope) {
	if envelope.Signal.IsStandard() {
		key := coalesceKey{target: envelope.Target, gen: envelope.Gen, sig: envelope.Signal}
		if h.pending[key] == envelope {
			delete(h.pending, key)
		}
	}
}

// authorizeOrCancel runs the delivery-time policy check while honoring target
// stops and in-flight cancels, so Close, Restart, or Cancel cannot be blocked
// by an authorizer waiting on external state.
func (h *Host) authorizeOrCancel(envelope *QueuedEnvelope, target *targetState) (allowed bool, canceled bool) {
	type result struct{ allowed bool }
	done := make(chan result, 1)
	go func() {
		done <- result{allowed: h.auth.Authorize(envelope.Tenant, envelope.Target, envelope.Gen, envelope.Signal)}
	}()
	select {
	case outcome := <-done:
		h.mu.Lock()
		canceled = envelope.canceled
		h.mu.Unlock()
		return outcome.allowed, canceled
	case <-target.recheck:
		h.mu.Lock()
		canceled = envelope.canceled
		h.mu.Unlock()
		if canceled {
			return false, true
		}
		return h.authorizeOrCancel(envelope, target)
	case <-target.stop:
		return false, false
	}
}

// frontForLocked returns the first queued envelope destined for target, or nil.
// Envelopes ahead of it belong to other pumps and stay in FIFO order.
func (h *Host) frontForLocked(target *targetState) *QueuedEnvelope {
	for element := h.queue.Front(); element != nil; element = element.Next() {
		envelope := element.Value.(*QueuedEnvelope)
		if envelope.target == target {
			return envelope
		}
	}
	return nil
}

// hasAnyLocked reports whether any envelope for target is still queued.
func (h *Host) hasAnyLocked(target *targetState) bool {
	return h.frontForLocked(target) != nil
}

// waitRecheckOrStop blocks until an authorization refresh or target stop.
func (h *Host) waitRecheckOrStop(target *targetState) {
	select {
	case <-target.recheck:
	case <-target.stop:
	}
}
