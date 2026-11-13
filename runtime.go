package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrUnknownResource means the host did not provide a live resource.
	ErrUnknownResource = errors.New("unknown or cleared host resource")
)

// Call failure reasons. A failed call never reaches the host resource.
const (
	CallReasonOK                  = "ok"
	CallReasonNoObject            = "no_such_object"
	CallReasonStaleIdentity       = "stale_identity"
	CallReasonNotLive             = "object_not_live"
	CallReasonNoStrongReference   = "no_strong_reference"
	CallReasonCapabilityStale     = "stale_capability"
	CallReasonRevoked             = "capability_revoked"
	CallReasonResourceMismatch    = "resource_mismatch"
	CallReasonResourceUnavailable = "resource_unavailable"
)

type objectRecord struct {
	ref ObjectRef

	state      ObjectState
	capGen     CapabilityGeneration
	resourceID ResourceID
	lastResID  ResourceID
	strong     int

	revoked         bool
	hostAllowed     bool
	finalizeQueued  bool
	finalizeDone    bool
	finalizeRunning bool

	weakGetAttempts  int
	weakGetSuccesses int
	resRequests      []ResurrectionRequest
}

type strongToken struct {
	id     uint64
	object *objectRecord
	active bool
}

type hostResource struct {
	id        ResourceID
	label     string
	live      bool
	calls     int
	clearings int
	clearedBy ObjectID
}

// Runtime implements the host-facing object lifecycle. All transitions are
// serialized by mu; untrusted finalizer code always runs without the lock.
type Runtime struct {
	mu sync.Mutex

	nextID      ObjectID
	nextAddress Address
	nextAlloc   AllocationGeneration
	nextToken   uint64

	byID      map[ObjectID]*objectRecord
	byAddress map[Address]*objectRecord
	freeAddrs []Address

	tokens map[uint64]*strongToken
	slots  map[SlotID]*strongToken

	nextResource ResourceID
	resources    map[ResourceID]*hostResource

	finalizer Finalizer
	queue     []ObjectID
}

// NewRuntime constructs an empty lifecycle runtime.
func NewRuntime() *Runtime {
	return &Runtime{
		byID:      make(map[ObjectID]*objectRecord),
		byAddress: make(map[Address]*objectRecord),
		tokens:    make(map[uint64]*strongToken),
		slots:     make(map[SlotID]*strongToken),
		resources: make(map[ResourceID]*hostResource),
	}
}

// SetFinalizer installs the untrusted callback invoked for each object at most
// once. It should be called before dropping references.
func (r *Runtime) SetFinalizer(fn Finalizer) {
	r.mu.Lock()
	r.finalizer = fn
	r.mu.Unlock()
}

// NewResource registers a host resource and returns its identifier.
func (r *Runtime) NewResource(label string) ResourceID {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextResource++
	id := r.nextResource
	r.resources[id] = &hostResource{id: id, label: label, live: true}
	return id
}

// Allocate creates a live object with one strong reference bound to a live host
// resource. The returned weak reference is only an identity; it grants no
// capability.
func (r *Runtime) Allocate(resourceID ResourceID) (*StrongRef, WeakRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := r.resources[resourceID]
	if res == nil || !res.live {
		return nil, WeakRef{}, ErrUnknownResource
	}

	var address Address
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
	}

	r.byID[obj.ref.ID] = obj
	r.byAddress[address] = obj

	strong := r.makeStrongLocked(obj)
	return strong, WeakRef{ObjectRef: obj.ref}, nil
}

// StrongAdd retains another strong reference. Like WeakGet, it requires a live
// object with a nonzero strong count.
func (r *Runtime) StrongAdd(w WeakRef) (*StrongRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.weakUpgradeLocked(w)
}

// WeakGet atomically upgrades a weak reference only when the object is live and
// still has at least one strong owner.
func (r *Runtime) WeakGet(w WeakRef) (*StrongRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.weakUpgradeLocked(w)
}

func (r *Runtime) weakUpgradeLocked(w WeakRef) (*StrongRef, bool) {
	obj := r.resolveLocked(w.ObjectRef)
	if obj == nil || obj.state != StateLive || obj.strong <= 0 {
		return nil, false
	}
	return r.makeStrongLocked(obj), true
}

// WeakOf derives a weak identity from a live strong handle.
func (r *Runtime) WeakOf(s *StrongRef) (WeakRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveStrongLocked(s)
	if obj == nil || obj.state != StateLive {
		return WeakRef{}, false
	}
	return WeakRef{ObjectRef: obj.ref}, true
}

// StrongDrop consumes one strong token. Repeated drops of the same handle are
// ignored. Exactly one finalizer is queued over the object's lifetime.
func (r *Runtime) StrongDrop(s *StrongRef) DropResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s == nil {
		return DropResult{}
	}
	tok := r.tokens[s.token]
	if tok == nil || tok.object.ref != s.ObjectRef {
		return DropResult{}
	}
	obj := tok.object
	if !tok.active {
		return DropResult{
			Matched:    true,
			StrongRefs: obj.strong,
			State:      obj.state,
		}
	}

	tok.active = false
	if obj.strong > 0 {
		obj.strong--
	}
	queued := false
	if obj.strong == 0 {
		queued = r.onZeroLocked(obj)
	}
	return DropResult{
		Matched:        true,
		Changed:        true,
		StrongRefs:     obj.strong,
		State:          obj.state,
		FinalizeQueued: queued,
	}
}

// IssueTicket snapshots the object's current capability generation. The ticket
// does not extend any lifetime.
func (r *Runtime) IssueTicket(s *StrongRef) (Ticket, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveStrongLocked(s)
	if obj == nil || obj.state != StateLive || obj.strong <= 0 || obj.revoked {
		return Ticket{}, false
	}
	if obj.resourceID == 0 || !r.resourceLiveLocked(obj.resourceID) {
		return Ticket{}, false
	}
	return Ticket{
		Object:     obj.ref,
		CapGen:     obj.capGen,
		ResourceID: obj.resourceID,
	}, true
}

// Call performs a real host-resource invocation only if the ticket's object ID,
// allocation generation, capability generation, resource ID, and current
// lifecycle state all still match.
func (r *Runtime) Call(t Ticket) CallResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.byID[t.Object.ID]
	if obj == nil {
		return CallResult{Reason: CallReasonNoObject, ResourceID: t.ResourceID}
	}
	if obj.ref != t.Object {
		return CallResult{Reason: CallReasonStaleIdentity, ResourceID: t.ResourceID}
	}
	if obj.state != StateLive {
		return CallResult{Reason: CallReasonNotLive, ResourceID: t.ResourceID, Calls: r.resourceCallsLocked(t.ResourceID)}
	}
	if obj.strong <= 0 {
		return CallResult{Reason: CallReasonNoStrongReference, ResourceID: t.ResourceID}
	}
	if t.CapGen != obj.capGen {
		return CallResult{Reason: CallReasonCapabilityStale, ResourceID: t.ResourceID}
	}
	if obj.revoked {
		return CallResult{Reason: CallReasonRevoked, ResourceID: t.ResourceID}
	}
	if t.ResourceID == 0 || t.ResourceID != obj.resourceID {
		return CallResult{Reason: CallReasonResourceMismatch, ResourceID: t.ResourceID}
	}
	res := r.resources[obj.resourceID]
	if res == nil || !res.live {
		return CallResult{Reason: CallReasonResourceUnavailable, ResourceID: t.ResourceID}
	}

	res.calls++
	return CallResult{
		Success:    true,
		Reason:     CallReasonOK,
		ResourceID: res.id,
		Calls:      res.calls,
	}
}

// Revoke increments the capability generation before clearing the host
// resource. Every previously issued ticket becomes stale, including tickets
// retained by an in-flight finalizer.
func (r *Runtime) Revoke(ref ObjectRef) RevokeResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveLocked(ref)
	if obj == nil {
		return RevokeResult{}
	}
	if obj.state == StateDead {
		return RevokeResult{
			Matched:     true,
			PriorCapGen: obj.capGen,
			CapGen:      obj.capGen,
			State:       obj.state,
		}
	}
	if obj.revoked {
		return RevokeResult{
			Matched:     true,
			PriorCapGen: obj.capGen,
			CapGen:      obj.capGen,
			State:       obj.state,
		}
	}

	prior := obj.capGen
	obj.capGen++
	obj.revoked = true
	cleared := r.clearResourceLocked(obj)
	return RevokeResult{
		Matched:         true,
		Changed:         true,
		PriorCapGen:     prior,
		CapGen:          obj.capGen,
		ResourceCleared: cleared,
		State:           obj.state,
	}
}

// SetHostResurrectionPolicy is a trusted host API. The untrusted finalizer can
// request a slot but can never set this flag. The policy is checked again at
// finalization completion, after any concurrent Revoke.
func (r *Runtime) SetHostResurrectionPolicy(ref ObjectRef, allowed bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveLocked(ref)
	if obj == nil || obj.state == StateDead {
		return false
	}
	obj.hostAllowed = allowed
	return true
}

// SetHostResurrectionAllowed is the strong-handle convenience form of
// SetHostResurrectionPolicy.
func (r *Runtime) SetHostResurrectionAllowed(s *StrongRef, allowed bool) bool {
	if s == nil {
		return false
	}
	return r.SetHostResurrectionPolicy(s.ObjectRef, allowed)
}

// SetGlobalSlot installs a strong reference owned by the global table. Replacing
// a slot drops the previous owner and may queue that object's finalizer.
func (r *Runtime) SetGlobalSlot(slot SlotID, s *StrongRef) (SlotSetResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveStrongLocked(s)
	if obj == nil || obj.state != StateLive || obj.strong <= 0 {
		return SlotSetResult{}, false
	}

	var previous *SlotInfo
	queued := false
	old := r.slots[slot]
	if old != nil {
		if info, ok := r.slotInfoLocked(slot, old); ok {
			copy := info
			previous = &copy
		}
	}

	r.slots[slot] = r.makeStrongTokenLocked(obj)
	if old != nil {
		result := r.dropTokenLocked(old)
		queued = result.FinalizeQueued
	}

	return SlotSetResult{
		Changed:        true,
		Previous:       previous,
		FinalizeQueued: queued,
	}, true
}

// ClearGlobalSlot removes a global slot and drops the strong reference it owned.
func (r *Runtime) ClearGlobalSlot(slot SlotID) DropResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.slots[slot]
	if old == nil {
		return DropResult{}
	}
	delete(r.slots, slot)
	return r.dropTokenLocked(old)
}

// GlobalSlot returns a snapshot of the current slot owner.
func (r *Runtime) GlobalSlot(slot SlotID) (SlotInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tok := r.slots[slot]
	if tok == nil {
		return SlotInfo{}, false
	}
	return r.slotInfoLocked(slot, tok)
}

// Inspect returns a snapshot of an object, including dead history.
func (r *Runtime) Inspect(ref ObjectRef) (ObjectInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.resolveLocked(ref)
	if obj == nil {
		return ObjectInfo{}, false
	}
	return r.snapshotLocked(obj), true
}

// InspectID returns an object snapshot by unique logical ID.
func (r *Runtime) InspectID(id ObjectID) (ObjectInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	obj := r.byID[id]
	if obj == nil {
		return ObjectInfo{}, false
	}
	return r.snapshotLocked(obj), true
}

// ResourceInfo returns a snapshot of a host resource.
func (r *Runtime) ResourceInfo(id ResourceID) (ResourceInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := r.resources[id]
	if res == nil {
		return ResourceInfo{}, false
	}
	return ResourceInfo{
		ID:        res.id,
		Label:     res.label,
		Live:      res.live,
		Calls:     res.calls,
		Clearings: res.clearings,
		ClearedBy: res.clearedBy,
	}, true
}

// PendingFinalizers reports how many finalizers are currently queued.
func (r *Runtime) PendingFinalizers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// FinalizeNext runs one queued finalizer. The callback executes without the
// runtime lock, so Revoke may run concurrently with it.
func (r *Runtime) FinalizeNext() (FinalizeReport, bool) {
	r.mu.Lock()

	var obj *objectRecord
	for len(r.queue) > 0 {
		id := r.queue[0]
		r.queue = r.queue[1:]
		candidate := r.byID[id]
		if candidate == nil {
			continue
		}
		if candidate.state != StateFinalizing || candidate.finalizeDone || candidate.finalizeRunning {
			continue
		}
		obj = candidate
		break
	}
	if obj == nil {
		r.mu.Unlock()
		return FinalizeReport{}, false
	}

	obj.finalizeRunning = true
	ctx := &FinalizeContext{rt: r, ref: obj.ref}
	report := FinalizeReport{
		ObjectRef:   obj.ref,
		PriorCapGen: obj.capGen,
		CapGen:      obj.capGen,
	}
	fn := r.finalizer
	r.mu.Unlock()

	var callbackPanic any
	if fn != nil {
		func() {
			defer func() {
				callbackPanic = recover()
			}()
			fn(ctx)
		}()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ctx.closed = true
	obj.finalizeRunning = false
	obj.finalizeDone = true
	report.CapGen = obj.capGen
	report.WeakGetAttempts = obj.weakGetAttempts
	report.WeakGetSuccesses = obj.weakGetSuccesses
	if callbackPanic != nil {
		report.Panic = fmt.Sprint(callbackPanic)
	}

	requests := append([]ResurrectionRequest(nil), obj.resRequests...)
	if len(requests) > 0 {
		reason := ""
		switch {
		case !obj.hostAllowed:
			reason = "host_denied"
		case obj.revoked:
			reason = "capability_revoked"
		case obj.resourceID == 0 || !r.resourceLiveLocked(obj.resourceID):
			reason = "resource_unavailable"
		}

		if reason == "" {
			first := requests[0]
			first.Accepted = true
			first.Reason = "accepted"
			requests[0] = first
			for i := 1; i < len(requests); i++ {
				requests[i].Reason = "duplicate_request"
			}

			obj.state = StateLive
			previous, queued := r.installSlotLocked(first.Slot, obj)
			report.Slot = first.Slot
			report.PreviousSlotOwner = previous
			report.SlotFinalizeQueued = queued
		} else {
			for i := range requests {
				requests[i].Reason = reason
			}
			report.ResourceCleared = r.dieLocked(obj)
		}
	} else {
		report.ResourceCleared = r.dieLocked(obj)
	}

	obj.resRequests = requests
	report.Requests = append([]ResurrectionRequest(nil), requests...)
	report.FinalState = obj.state
	report.CapGen = obj.capGen
	return report, true
}

// RunFinalizers drains up to limit queued finalizers. A non-positive limit
// drains until the queue is empty.
func (r *Runtime) RunFinalizers(limit int) []FinalizeReport {
	var reports []FinalizeReport
	for limit <= 0 || len(reports) < limit {
		report, ok := r.FinalizeNext()
		if !ok {
			break
		}
		reports = append(reports, report)
	}
	return reports
}

func (r *Runtime) finalizerWeakGet(c *FinalizeContext, w WeakRef) (*StrongRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c == nil || c.closed {
		return nil, false
	}
	obj := r.resolveLocked(c.ref)
	if obj == nil || !obj.finalizeRunning || obj.state != StateFinalizing {
		return nil, false
	}
	obj.weakGetAttempts++
	strong, ok := r.weakUpgradeLocked(w)
	if ok {
		obj.weakGetSuccesses++
	}
	return strong, ok
}

func (r *Runtime) finalizerResurrect(c *FinalizeContext, slot SlotID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c == nil || c.closed {
		return false
	}
	obj := r.resolveLocked(c.ref)
	if obj == nil || !obj.finalizeRunning || obj.state != StateFinalizing {
		return false
	}
	obj.resRequests = append(obj.resRequests, ResurrectionRequest{
		Ordinal: len(obj.resRequests),
		Slot:    slot,
	})
	return true
}

func (r *Runtime) resolveLocked(ref ObjectRef) *objectRecord {
	obj := r.byID[ref.ID]
	if obj == nil || obj.ref != ref {
		return nil
	}
	return obj
}

func (r *Runtime) resolveStrongLocked(s *StrongRef) *objectRecord {
	if s == nil {
		return nil
	}
	tok := r.tokens[s.token]
	if tok == nil || !tok.active || tok.object.ref != s.ObjectRef {
		return nil
	}
	return tok.object
}

func (r *Runtime) makeStrongLocked(obj *objectRecord) *StrongRef {
	tok := r.makeStrongTokenLocked(obj)
	return &StrongRef{ObjectRef: obj.ref, token: tok.id}
}

func (r *Runtime) makeStrongTokenLocked(obj *objectRecord) *strongToken {
	r.nextToken++
	tok := &strongToken{
		id:     r.nextToken,
		object: obj,
		active: true,
	}
	r.tokens[tok.id] = tok
	obj.strong++
	return tok
}

func (r *Runtime) dropTokenLocked(tok *strongToken) DropResult {
	if tok == nil || !tok.active {
		return DropResult{}
	}
	obj := tok.object
	tok.active = false
	if obj.strong > 0 {
		obj.strong--
	}
	queued := false
	if obj.strong == 0 {
		queued = r.onZeroLocked(obj)
	}
	return DropResult{
		Matched:        true,
		Changed:        true,
		StrongRefs:     obj.strong,
		State:          obj.state,
		FinalizeQueued: queued,
	}
}

func (r *Runtime) onZeroLocked(obj *objectRecord) bool {
	if obj.state != StateLive {
		return false
	}
	if obj.finalizeDone {
		r.dieLocked(obj)
		return false
	}
	if obj.finalizeQueued {
		return false
	}

	obj.state = StateFinalizing
	obj.finalizeQueued = true
	r.queue = append(r.queue, obj.ref.ID)
	return true
}

func (r *Runtime) dieLocked(obj *objectRecord) bool {
	if obj.state == StateDead {
		return false
	}
	cleared := r.clearResourceLocked(obj)
	obj.state = StateDead
	obj.strong = 0
	if current := r.byAddress[obj.ref.Address]; current == obj {
		delete(r.byAddress, obj.ref.Address)
		r.freeAddrs = append(r.freeAddrs, obj.ref.Address)
	}
	return cleared
}

func (r *Runtime) clearResourceLocked(obj *objectRecord) bool {
	cleared := false
	if obj.resourceID != 0 {
		if res := r.resources[obj.resourceID]; res != nil && res.live {
			res.live = false
			res.clearings++
			res.clearedBy = obj.ref.ID
			cleared = true
		}
		obj.lastResID = obj.resourceID
		obj.resourceID = 0
	}
	return cleared
}

func (r *Runtime) resourceLiveLocked(id ResourceID) bool {
	res := r.resources[id]
	return res != nil && res.live
}

func (r *Runtime) resourceCallsLocked(id ResourceID) int {
	if res := r.resources[id]; res != nil {
		return res.calls
	}
	return 0
}

func (r *Runtime) installSlotLocked(slot SlotID, obj *objectRecord) (*SlotInfo, bool) {
	old := r.slots[slot]
	var previous *SlotInfo
	if old != nil {
		if info, ok := r.slotInfoLocked(slot, old); ok {
			copy := info
			previous = &copy
		}
	}

	r.slots[slot] = r.makeStrongTokenLocked(obj)
	queued := false
	if old != nil {
		result := r.dropTokenLocked(old)
		queued = result.FinalizeQueued
	}
	return previous, queued
}

func (r *Runtime) slotInfoLocked(slot SlotID, tok *strongToken) (SlotInfo, bool) {
	if tok == nil || !tok.active {
		return SlotInfo{}, false
	}
	obj := tok.object
	return SlotInfo{
		Slot:    slot,
		Object:  obj.ref,
		State:   obj.state,
		CapGen:  obj.capGen,
		Revoked: obj.revoked,
	}, true
}

func (r *Runtime) snapshotLocked(obj *objectRecord) ObjectInfo {
	return ObjectInfo{
		ObjectRef:              obj.ref,
		State:                  obj.state,
		CapGen:                 obj.capGen,
		ResourceID:             obj.resourceID,
		LastResourceID:         obj.lastResID,
		StrongRefs:             obj.strong,
		Revoked:                obj.revoked,
		HostAllowsResurrection: obj.hostAllowed,
		FinalizeQueued:         obj.finalizeQueued,
		FinalizeDone:           obj.finalizeDone,
		ResurrectionRequests:   append([]ResurrectionRequest(nil), obj.resRequests...),
	}
}
