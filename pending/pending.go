// Package pending is a deliberately vulnerable implementation kept only as a
// regression fixture for REISSUE-20261113-0070.
//
// It reproduces the audited bug: a finalizer that runs after Revoke puts the
// old object identity back into all requested global slots and makes the old
// resource live again. Call also forgets to revalidate the capability
// generation. Consequently a ticket issued before Revoke can perform a real
// host-resource call after Revoke.
//
// DO NOT USE THIS PACKAGE OUTSIDE SECURITY TESTS.
package pending

import (
	"errors"
	"sync"

	"example.org/netsec/q0070"
)

type (
	// ObjectRef, Ticket, WeakRef, ResourceID, SlotID, and ObjectState deliberately
	// use the safe package's wire types so tests can compare both implementations.
	ObjectRef   = netsec.ObjectRef
	Ticket      = netsec.Ticket
	WeakRef     = netsec.WeakRef
	ResourceID  = netsec.ResourceID
	SlotID      = netsec.SlotID
	ObjectState = netsec.ObjectState
)

const (
	StateLive       = netsec.StateLive
	StateFinalizing = netsec.StateFinalizing
	StateDead       = netsec.StateDead
)

// Handle is the fixture's strong-reference token.
type Handle struct {
	ObjectRef
	token uint64
}

type objectRecord struct {
	ref    ObjectRef
	state  ObjectState
	capGen netsec.CapabilityGeneration

	resourceID     ResourceID
	lastResourceID ResourceID
	strong         int
	revoked        bool
	hostAllowed    bool

	finalizeQueued  bool
	finalizeDone    bool
	finalizeRunning bool

	weakGetAttempts  int
	weakGetSuccesses int
	resRequests      []netsec.ResurrectionRequest
}

type hostResource struct {
	id        ResourceID
	label     string
	live      bool
	calls     int
	clearings int
}

// Runtime is a minimal single-object lifecycle model. Its finalization branch
// is intentionally incorrect.
type Runtime struct {
	mu sync.Mutex

	nextID      netsec.ObjectID
	nextAddress netsec.Address
	nextAlloc   netsec.AllocationGeneration
	nextToken   uint64

	byID      map[netsec.ObjectID]*objectRecord
	byAddress map[netsec.Address]*objectRecord
	freeAddrs []netsec.Address
	tokens    map[uint64]*objectRecord
	current   *objectRecord

	nextResource ResourceID
	resources    map[ResourceID]*hostResource

	queue     bool
	slots     map[SlotID]ObjectRef
	finalizer Finalizer
}

// Finalizer is untrusted callback code.
type Finalizer func(*FinalizeContext)

// FinalizeContext exposes only weak access and slot requests, matching the
// capabilities available to script finalizers.
type FinalizeContext struct {
	rt     *Runtime
	ref    ObjectRef
	closed bool
}

// Object returns the finalizing identity.
func (c *FinalizeContext) Object() ObjectRef { return c.ref }

// WeakGet is rejected because the object is already finalizing.
func (c *FinalizeContext) WeakGet() (*Handle, bool) {
	return c.rt.finalizerWeakGet(c)
}

// RequestResurrect records a request. The fixture later applies it without
// checking the host policy or revocation state.
func (c *FinalizeContext) RequestResurrect(slot SlotID) bool {
	return c.rt.finalizerResurrect(c, slot)
}

var (
	// ErrUnknownResource is returned for an unavailable host resource.
	ErrUnknownResource = errors.New("unknown or cleared host resource")
)

// NewRuntime constructs the vulnerable fixture runtime.
func NewRuntime() *Runtime {
	return &Runtime{
		byID:      make(map[netsec.ObjectID]*objectRecord),
		byAddress: make(map[netsec.Address]*objectRecord),
		tokens:    make(map[uint64]*objectRecord),
		resources: make(map[ResourceID]*hostResource),
		slots:     make(map[SlotID]ObjectRef),
	}
}

// SetFinalizer installs the untrusted callback.
func (r *Runtime) SetFinalizer(fn Finalizer) {
	r.mu.Lock()
	r.finalizer = fn
	r.mu.Unlock()
}

// NewResource registers a host resource.
func (r *Runtime) NewResource(label string) ResourceID {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextResource++
	id := r.nextResource
	r.resources[id] = &hostResource{id: id, label: label, live: true}
	return id
}

// Allocate creates an object with one strong reference.
func (r *Runtime) Allocate(resourceID ResourceID) (*Handle, WeakRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := r.resources[resourceID]
	if res == nil || !res.live {
		return nil, WeakRef{}, ErrUnknownResource
	}

	var address netsec.Address
	if n := len(r.freeAddrs); n > 0 {
		address = r.freeAddrs[n-1]
		r.freeAddrs = r.freeAddrs[:n-1]
	} else {
		address = r.nextAddress
		r.nextAddress++
	}

	r.nextID++
	r.nextAlloc++
	obj := &objectRecord{
		ref: ObjectRef{
			ID:       r.nextID,
			Address:  address,
			AllocGen: r.nextAlloc,
		},
		state:      StateLive,
		resourceID: resourceID,
		strong:     1,
	}
	r.nextToken++
	obj.strong = 1
	r.tokens[r.nextToken] = obj
	r.byID[obj.ref.ID] = obj
	r.byAddress[address] = obj
	r.current = obj

	handle := &Handle{ObjectRef: obj.ref, token: r.nextToken}
	return handle, WeakRef{ObjectRef: obj.ref}, nil
}

// WeakGet follows the safe upgrade rule; it is not the buggy operation.
func (r *Runtime) WeakGet(w WeakRef) (*Handle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.weakGetLocked(w)
}

func (r *Runtime) weakGetLocked(w WeakRef) (*Handle, bool) {
	obj := r.byID[w.ID]
	if obj == nil || obj.ref != w.ObjectRef || obj.state != StateLive || obj.strong <= 0 {
		return nil, false
	}
	r.nextToken++
	r.tokens[r.nextToken] = obj
	obj.strong++
	return &Handle{ObjectRef: obj.ref, token: r.nextToken}, true
}

// WeakOf derives a weak identity from a live handle.
func (r *Runtime) WeakOf(h *Handle) (WeakRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.resolveHandleLocked(h)
	if obj == nil || obj.state != StateLive {
		return WeakRef{}, false
	}
	return WeakRef{ObjectRef: obj.ref}, true
}

// Drop consumes one strong token. Repeated drops do not queue another
// finalizer.
func (r *Runtime) Drop(h *Handle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h == nil {
		return
	}
	obj := r.byID[h.ID]
	if obj == nil || obj.ref != h.ObjectRef {
		return
	}
	if r.tokens[h.token] != obj {
		return
	}
	delete(r.tokens, h.token)
	if obj.strong > 0 {
		obj.strong--
	}
	if obj.strong == 0 && obj.state == StateLive {
		if obj.finalizeDone {
			r.dieLocked(obj)
		} else if !obj.finalizeQueued {
			obj.state = StateFinalizing
			obj.finalizeQueued = true
			r.queue = true
		}
	}
}

// IssueTicket snapshots the old capability generation.
func (r *Runtime) IssueTicket(h *Handle) (Ticket, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.resolveHandleLocked(h)
	if obj == nil || obj.state != StateLive || obj.strong <= 0 || obj.revoked {
		return Ticket{}, false
	}
	if obj.resourceID == 0 || !r.resourceLiveLocked(obj.resourceID) {
		return Ticket{}, false
	}
	return Ticket{Object: obj.ref, CapGen: obj.capGen, ResourceID: obj.resourceID}, true
}

// Call contains the second deliberate flaw: it validates identity and the
// resource identifier but not CapGen or the revoked bit. After the buggy
// finalizer restores the old resource, the pre-revoke ticket therefore reaches
// the host.
func (r *Runtime) Call(t Ticket) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.byID[t.Object.ID]
	if obj == nil || obj.ref != t.Object || obj.state != StateLive || obj.strong <= 0 {
		return false
	}
	if t.ResourceID == 0 || t.ResourceID != obj.resourceID {
		return false
	}
	res := r.resources[t.ResourceID]
	if res == nil || !res.live {
		return false
	}
	res.calls++
	return true
}

// Revoke increments CapGen before clearing the resource, but the finalizer
// below later restores both the old ID and the live bit.
func (r *Runtime) Revoke(ref ObjectRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.byID[ref.ID]
	if obj == nil || obj.ref != ref || obj.state == StateDead || obj.revoked {
		return
	}
	obj.capGen++
	obj.revoked = true
	r.clearResourceLocked(obj)
}

// SetHostResurrectionAllowed is faithfully recorded but deliberately ignored
// by the vulnerable finalization path.
func (r *Runtime) SetHostResurrectionAllowed(h *Handle, allowed bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.resolveHandleLocked(h)
	if obj == nil || obj.state == StateDead {
		return false
	}
	obj.hostAllowed = allowed
	return true
}

// PutGlobalSlot places a live object identity in a slot.
func (r *Runtime) PutGlobalSlot(slot SlotID, h *Handle) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.resolveHandleLocked(h)
	if obj == nil || obj.state != StateLive {
		return false
	}
	r.slots[slot] = obj.ref
	return true
}

// GlobalSlot returns the current slot value.
func (r *Runtime) GlobalSlot(slot SlotID) (ObjectRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.slots[slot]
	return ref, ok
}

// FinalizeNext runs the queued callback and then applies the buggy resurrection
// policy. It is the only place where the fixture intentionally diverges from
// the safe model.
func (r *Runtime) FinalizeNext() (bool, bool) {
	r.mu.Lock()
	if !r.queue {
		r.mu.Unlock()
		return false, false
	}

	var obj *objectRecord
	for _, candidate := range r.byID {
		if candidate.state == StateFinalizing && !candidate.finalizeDone && !candidate.finalizeRunning {
			obj = candidate
			break
		}
	}
	if obj == nil {
		r.queue = false
		r.mu.Unlock()
		return false, false
	}

	obj.finalizeRunning = true
	oldResource := obj.resourceID
	if oldResource == 0 {
		oldResource = obj.lastResourceID
	}
	ctx := &FinalizeContext{rt: r, ref: obj.ref}
	fn := r.finalizer
	r.mu.Unlock()

	if fn != nil {
		fn(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ctx.closed = true
	obj.finalizeRunning = false
	obj.finalizeDone = true
	r.queue = false

	if len(obj.resRequests) > 0 {
		// VULNERABILITY: host policy and obj.revoked are not consulted.
		if res := r.resources[oldResource]; res != nil {
			res.live = true
			obj.resourceID = oldResource
		}
		obj.state = StateLive
		obj.strong = 1
		for i := range obj.resRequests {
			obj.resRequests[i].Accepted = true
			obj.resRequests[i].Reason = "VULNERABLE_ACCEPTED"
			r.slots[obj.resRequests[i].Slot] = obj.ref
		}
		return true, true
	}

	r.dieLocked(obj)
	return true, false
}

// RunFinalizers drains the single-object queue.
func (r *Runtime) RunFinalizers() int {
	count := 0
	for {
		ran, _ := r.FinalizeNext()
		if !ran {
			return count
		}
		count++
	}
}

func (r *Runtime) finalizerWeakGet(c *FinalizeContext) (*Handle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == nil || c.closed {
		return nil, false
	}
	obj := r.byID[c.ref.ID]
	if obj == nil || obj.ref != c.ref || !obj.finalizeRunning || obj.state != StateFinalizing {
		return nil, false
	}
	obj.weakGetAttempts++
	handle, ok := r.weakGetLocked(WeakRef{ObjectRef: c.ref})
	if ok {
		obj.weakGetSuccesses++
	}
	return handle, ok
}

func (r *Runtime) finalizerResurrect(c *FinalizeContext, slot SlotID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == nil || c.closed {
		return false
	}
	obj := r.byID[c.ref.ID]
	if obj == nil || obj.ref != c.ref || !obj.finalizeRunning || obj.state != StateFinalizing {
		return false
	}
	obj.resRequests = append(obj.resRequests, netsec.ResurrectionRequest{
		Ordinal: len(obj.resRequests),
		Slot:    slot,
	})
	return true
}

func (r *Runtime) resolveHandleLocked(h *Handle) *objectRecord {
	if h == nil {
		return nil
	}
	obj := r.tokens[h.token]
	if obj == nil || obj.ref != h.ObjectRef {
		return nil
	}
	return obj
}

func (r *Runtime) clearResourceLocked(obj *objectRecord) bool {
	cleared := false
	if obj.resourceID != 0 {
		if res := r.resources[obj.resourceID]; res != nil && res.live {
			res.live = false
			res.clearings++
			cleared = true
		}
		obj.lastResourceID = obj.resourceID
		obj.resourceID = 0
	}
	return cleared
}

func (r *Runtime) resourceLiveLocked(id ResourceID) bool {
	res := r.resources[id]
	return res != nil && res.live
}

func (r *Runtime) dieLocked(obj *objectRecord) {
	if obj.state == StateDead {
		return
	}
	r.clearResourceLocked(obj)
	obj.state = StateDead
	obj.strong = 0
	if current := r.byAddress[obj.ref.Address]; current == obj {
		delete(r.byAddress, obj.ref.Address)
		r.freeAddrs = append(r.freeAddrs, obj.ref.Address)
	}
	if r.current == obj {
		r.current = nil
	}
}

// Inspect returns the current object snapshot.
func (r *Runtime) Inspect() (ObjectInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.current
	if obj == nil {
		for _, candidate := range r.byID {
			if obj == nil || candidate.ref.ID > obj.ref.ID {
				obj = candidate
			}
		}
	}
	if obj == nil {
		return ObjectInfo{}, false
	}
	return snapshotObject(obj), true
}

// InspectRef returns a specific generation-qualified object snapshot.
func (r *Runtime) InspectRef(ref ObjectRef) (ObjectInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj := r.byID[ref.ID]
	if obj == nil || obj.ref != ref {
		return ObjectInfo{}, false
	}
	return snapshotObject(obj), true
}

func snapshotObject(obj *objectRecord) ObjectInfo {
	return ObjectInfo{
		Ref:                  obj.ref,
		State:                obj.state,
		CapGen:               obj.capGen,
		ResourceID:           obj.resourceID,
		LastResourceID:       obj.lastResourceID,
		Strong:               obj.strong,
		Revoked:              obj.revoked,
		HostAllowed:          obj.hostAllowed,
		FinalizeQueued:       obj.finalizeQueued,
		FinalizeDone:         obj.finalizeDone,
		WeakGetAttempts:      obj.weakGetAttempts,
		WeakGetSuccesses:     obj.weakGetSuccesses,
		ResurrectionRequests: append([]netsec.ResurrectionRequest(nil), obj.resRequests...),
	}
}

// ObjectInfo is a diagnostic snapshot from the vulnerable fixture.
type ObjectInfo struct {
	Ref                  ObjectRef
	State                ObjectState
	CapGen               netsec.CapabilityGeneration
	ResourceID           ResourceID
	LastResourceID       ResourceID
	Strong               int
	Revoked              bool
	HostAllowed          bool
	FinalizeQueued       bool
	FinalizeDone         bool
	WeakGetAttempts      int
	WeakGetSuccesses     int
	ResurrectionRequests []netsec.ResurrectionRequest
}

// ResourceInfo returns host-resource counters.
func (r *Runtime) ResourceInfo(id ResourceID) (live bool, calls, clearings int, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.resources[id]
	if res == nil {
		return false, 0, 0, false
	}
	return res.live, res.calls, res.clearings, true
}
