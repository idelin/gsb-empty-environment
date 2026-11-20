package caproute

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
)

// resourceGate serialises access to one backing resource against its close.
// An operation must acquire a lease before entering the resource callback;
// close marks the gate closed and waits for every leased operation to drain.
type resourceGate struct {
	resource Resource
	rtype    ResourceType
	label    string

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// acquire grants a lease. It returns false once the gate is closing/closed.
func (g *resourceGate) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// release ends a lease.
func (g *resourceGate) release() {
	g.wg.Done()
}

// close blocks new entries and waits for in-flight leases. It is safe to
// call concurrently with acquire and with itself.
func (g *resourceGate) close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		g.wg.Wait()
		return
	}
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}

// capability is the host-side record behind an opaque handle.
type capability struct {
	id         []byte
	owner      InstanceID
	gate       *resourceGate
	rtype      ResourceType
	allowed    opSet
	generation Generation
	revoked    bool
}

// instance tracks one plug-in instance and its non-rewindable generation.
type instance struct {
	generation Generation
}

// Host is the capability router. A Host is safe for concurrent use.
type Host struct {
	mu      sync.Mutex
	handles map[string]*capability
	insts   map[InstanceID]*instance
	gates   map[*resourceGate]struct{}
	audit   AuditSink
	rand    io.Reader
}

// Option customises a Host at construction.
type Option func(*Host)

// WithAudit installs an audit sink. When omitted a *MemoryAudit is used.
func WithAudit(sink AuditSink) Option {
	return func(h *Host) { h.audit = sink }
}

// withRand overrides the random source used for handles. It is unexported
// because production callers must keep the crypto/rand default; tests use it
// to assert forge resistance deterministically.
func withRand(r io.Reader) Option {
	return func(h *Host) { h.rand = r }
}

// NewHost creates an empty router.
func NewHost(opts ...Option) *Host {
	h := &Host{
		handles: make(map[string]*capability),
		insts:   make(map[InstanceID]*instance),
		gates:   make(map[*resourceGate]struct{}),
		audit:   &MemoryAudit{},
		rand:    rand.Reader,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Audit returns the audit sink installed on the host.
func (h *Host) Audit() AuditSink { return h.audit }

// RegisterInstance introduces a plug-in instance at generation 1. Re-registering
// an existing instance is an error; use Reload to advance its generation.
func (h *Host) RegisterInstance(id InstanceID) error {
	if id == "" {
		return errors.New("caproute: instance id must not be empty")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.insts[id]; ok {
		return errors.New("caproute: instance already registered: " + string(id))
	}
	h.insts[id] = &instance{generation: 1}
	return nil
}

// Generation returns the current generation of an instance. It returns 0 for
// unknown instances.
func (h *Host) Generation(id InstanceID) Generation {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ins, ok := h.insts[id]; ok {
		return ins.generation
	}
	return 0
}

// Reload advances the instance generation and invalidates every handle the
// instance currently holds. Generations only move forward. The new
// generation is returned.
func (h *Host) Reload(id InstanceID) (Generation, error) {
	h.mu.Lock()
	ins, ok := h.insts[id]
	if !ok {
		h.mu.Unlock()
		return 0, denied(ReasonUnknownInstance, "cannot reload unknown instance")
	}
	ins.generation++
	gen := ins.generation
	for _, capb := range h.handles {
		if capb.owner == id {
			capb.revoked = true
		}
	}
	h.mu.Unlock()
	return gen, nil
}

// UnloadInstance removes an instance, closes every resource gate exclusively
// associated with it and revokes its handles. A later RegisterInstance starts
// a fresh lineage at generation 1; old handles stay invalid.
func (h *Host) UnloadInstance(id InstanceID) error {
	h.mu.Lock()
	if _, ok := h.insts[id]; !ok {
		h.mu.Unlock()
		return denied(ReasonUnknownInstance, "cannot unload unknown instance")
	}
	delete(h.insts, id)

	var gates []*resourceGate
	ownedGates := make(map[*resourceGate]struct{})
	for _, capb := range h.handles {
		if capb.owner == id {
			ownedGates[capb.gate] = struct{}{}
			capb.revoked = true
		}
	}
	// Count live users of every gate so shared resources survive an unload.
	liveUsers := make(map[*resourceGate]map[InstanceID]struct{})
	for _, capb := range h.handles {
		if capb.revoked || capb.owner == id {
			continue
		}
		users, ok := liveUsers[capb.gate]
		if !ok {
			users = make(map[InstanceID]struct{})
			liveUsers[capb.gate] = users
		}
		users[capb.owner] = struct{}{}
	}
	for gate := range ownedGates {
		if len(liveUsers[gate]) == 0 {
			gates = append(gates, gate)
		}
	}
	for _, gate := range gates {
		delete(h.gates, gate)
	}
	h.mu.Unlock()

	// Drain outside the host lock so in-flight callbacks can finish.
	for _, gate := range gates {
		gate.close()
	}
	return nil
}

// RegisterResource attaches a backing resource to the host and returns its
// gate. label is host-side metadata used in audit records only.
func (h *Host) RegisterResource(rtype ResourceType, r Resource, label string) error {
	if r == nil {
		return errors.New("caproute: resource must not be nil")
	}
	if rtype != TypeFile && rtype != TypeNetwork {
		return errors.New("caproute: unknown resource type: " + string(rtype))
	}
	gate := &resourceGate{resource: r, rtype: rtype, label: label}
	h.mu.Lock()
	h.gates[gate] = struct{}{}
	h.mu.Unlock()
	return nil
}

// MintCapability is the sole way to create handles and is host-only. The
// capability is bound to owner, the resource identified by label/type and
// the exact allowed operations, and is stamped with the owner's current
// generation.
func (h *Host) MintCapability(owner InstanceID, rtype ResourceType, label string, ops []Operation) (Handle, error) {
	h.mu.Lock()
	ins, ok := h.insts[owner]
	if !ok {
		h.mu.Unlock()
		return nil, denied(ReasonUnknownInstance, "owner instance is not registered")
	}

	allowed := makeOpSet(ops)
	if len(allowed) == 0 {
		h.mu.Unlock()
		return nil, denied(ReasonOperationDenied, "capability must permit at least one operation")
	}
	for op := range allowed {
		want, known := opType[op]
		if !known {
			h.mu.Unlock()
			return nil, denied(ReasonUnknownOperation, string(op))
		}
		if want != rtype {
			h.mu.Unlock()
			return nil, denied(ReasonTypeMismatch, string(op)+" is not valid for type "+string(rtype))
		}
	}

	var target *resourceGate
	for gate := range h.gates {
		if gate.label == label && gate.rtype == rtype {
			target = gate
			break
		}
	}
	if target == nil {
		h.mu.Unlock()
		return nil, errors.New("caproute: no registered resource of type " + string(rtype) + " with label " + label)
	}

	raw := make([]byte, 32)
	if _, err := io.ReadFull(h.rand, raw); err != nil {
		h.mu.Unlock()
		return nil, err
	}
	id := make([]byte, len(raw))
	copy(id, raw)
	key := hex.EncodeToString(id)
	if _, collision := h.handles[key]; collision {
		h.mu.Unlock()
		return nil, errors.New("caproute: handle collision; retry")
	}

	capb := &capability{
		id:         id,
		owner:      owner,
		gate:       target,
		rtype:      rtype,
		allowed:    allowed,
		generation: ins.generation,
	}
	h.handles[key] = capb
	h.mu.Unlock()

	out := make(Handle, len(id))
	copy(out, id)
	return out, nil
}

// CloseResource closes the resource of the given type/label, waits for all
// in-flight operations on it to finish and revokes every handle pointing at
// it. After it returns, no new resource callback can be entered.
func (h *Host) CloseResource(rtype ResourceType, label string) error {
	h.mu.Lock()
	var target *resourceGate
	for gate := range h.gates {
		if gate.label == label && gate.rtype == rtype {
			target = gate
			break
		}
	}
	if target == nil {
		h.mu.Unlock()
		return errors.New("caproute: no such resource")
	}
	delete(h.gates, target)
	for _, capb := range h.handles {
		if capb.gate == target {
			capb.revoked = true
		}
	}
	h.mu.Unlock()

	target.close()
	return nil
}
