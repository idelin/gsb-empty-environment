package netsec

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// MaxChainDepth bounds the number of links in a proxied call chain.
const MaxChainDepth = 32

// Link is one hop of a proxied call chain: the instance on that hop and
// the operation cap it contributes. The effective operations of a call are
// the intersection of the handle's operations and every link's cap, so no
// hop can amplify privileges, however privileged its own handles are.
type Link struct {
	Instance InstanceID
	Ops      Op
}

// Denial is one audit record of a refused operation.
type Denial struct {
	Reason    DenyReason
	Requester InstanceID
	When      time.Time
}

// Host mints capabilities, routes proxied calls and owns the audit log.
// It is the only entity able to create valid handles.
type Host struct {
	mu        sync.Mutex
	instances map[InstanceID]*instanceState
	handles   map[handleID]*capRecord
	resources map[resourceID]*Resource
	denials   []Denial
}

func NewHost() *Host {
	return &Host{
		instances: make(map[InstanceID]*instanceState),
		handles:   make(map[handleID]*capRecord),
		resources: make(map[resourceID]*Resource),
	}
}

type instanceState struct {
	generation uint64
	active     bool
	retired    bool
	resources  map[resourceID]struct{}
}

type capRecord struct {
	id         handleID
	resource   resourceID
	rtype      ResourceType
	ops        Op
	instance   InstanceID
	generation uint64
}

// RegisterInstance loads a new plugin instance and returns its ID.
func (h *Host) RegisterInstance() InstanceID {
	id := InstanceID(randomID())
	h.mu.Lock()
	h.instances[id] = &instanceState{
		generation: 1,
		active:     true,
		resources:  make(map[resourceID]struct{}),
	}
	h.mu.Unlock()
	return id
}

// UnloadInstance unloads a plugin instance: its generation advances (so
// every handle minted under the old generation goes stale) and all of its
// resources are closed. The generation never wraps; an instance whose
// generation space is exhausted is retired permanently.
func (h *Host) UnloadInstance(id InstanceID) error {
	h.mu.Lock()
	inst, ok := h.instances[id]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("netsec: unknown instance")
	}
	if !inst.active {
		h.mu.Unlock()
		return nil
	}
	inst.active = false
	if inst.generation == math.MaxUint64 {
		inst.retired = true
	} else {
		inst.generation++
	}
	resources := make([]*Resource, 0, len(inst.resources))
	for rid := range inst.resources {
		if r := h.resources[rid]; r != nil {
			resources = append(resources, r)
		}
	}
	h.mu.Unlock()
	for _, r := range resources {
		r.Close()
	}
	return nil
}

// ReloadInstance reactivates an unloaded instance. Handles minted before
// the unload remain stale because the generation moved on.
func (h *Host) ReloadInstance(id InstanceID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	inst, ok := h.instances[id]
	if !ok {
		return fmt.Errorf("netsec: unknown instance")
	}
	if inst.retired {
		return fmt.Errorf("netsec: instance generation space exhausted, cannot reload")
	}
	inst.active = true
	return nil
}

// OpenResource registers a host resource for an instance and mints the
// root capability handle for it. The name is host-side bookkeeping and is
// never exposed through errors or the audit log.
func (h *Host) OpenResource(inst InstanceID, rtype ResourceType, ops Op, name string) (*Resource, Handle, error) {
	var zero Handle
	if !rtype.valid() {
		return nil, zero, fmt.Errorf("netsec: invalid resource type")
	}
	if ops == 0 || ops&^knownOps != 0 {
		return nil, zero, fmt.Errorf("netsec: invalid operation set")
	}
	h.mu.Lock()
	state, ok := h.instances[inst]
	if !ok || state.retired || !state.active {
		h.mu.Unlock()
		return nil, zero, fmt.Errorf("netsec: instance not active")
	}
	rid := resourceID(randomID())
	res := newResource(h, rid, rtype, name)
	h.resources[rid] = res
	state.resources[rid] = struct{}{}
	hid := handleID(randomID())
	h.handles[hid] = &capRecord{
		id:         hid,
		resource:   rid,
		rtype:      rtype,
		ops:        ops,
		instance:   inst,
		generation: state.generation,
	}
	handle := encodeHandle(hid, state.generation, rtype, ops, inst)
	h.mu.Unlock()
	return res, handle, nil
}

// Denials returns a copy of the audit log of refused operations.
func (h *Host) Denials() []Denial {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Denial, len(h.denials))
	copy(out, h.denials)
	return out
}

func (h *Host) deny(reason DenyReason, requester InstanceID) error {
	h.mu.Lock()
	h.denials = append(h.denials, Denial{Reason: reason, Requester: requester, When: time.Now()})
	h.mu.Unlock()
	return &Error{Reason: reason, Requester: requester}
}

type authResult struct {
	rec       *capRecord
	res       *Resource
	effective Op
	requester InstanceID
}

// authorize validates the handle bytes against the registry and folds the
// call chain into the effective operation set.
func (h *Host) authorize(handle Handle, chain []Link) (authResult, error) {
	parsed, ok := parseHandle(handle)
	requester := parsed.instance
	if n := len(chain); n > 0 {
		requester = chain[n-1].Instance
	}
	if !ok {
		return authResult{}, h.deny(DenyMalformedHandle, requester)
	}

	h.mu.Lock()
	rec := h.handles[parsed.id]
	var res *Resource
	var effective Op
	var reason DenyReason
	switch {
	case rec == nil:
		reason = DenyUnknownHandle
	case rec.rtype != parsed.rtype || rec.ops != parsed.ops ||
		rec.instance != parsed.instance || rec.generation != parsed.generation:
		// The bytes were mutated after minting (e.g. resource type or
		// operation bits flipped to reinterpret the capability).
		reason = DenyTamperedHandle
	default:
		inst := h.instances[rec.instance]
		switch {
		case inst == nil || inst.retired:
			reason = DenyInstanceRetired
		case !inst.active:
			reason = DenyInstanceUnloaded
		case rec.generation != inst.generation:
			reason = DenyStaleGeneration
		case len(chain) > MaxChainDepth:
			reason = DenyDepthExceeded
		case len(chain) > 0 && chain[0].Instance != rec.instance:
			reason = DenyForeignHandle
		default:
			seen := make(map[InstanceID]struct{}, len(chain))
			effective = rec.ops
			for _, link := range chain {
				if _, dup := seen[link.Instance]; dup {
					reason = DenyCycleDetected
					break
				}
				seen[link.Instance] = struct{}{}
				effective &= link.Ops
			}
			res = h.resources[rec.resource]
		}
	}
	h.mu.Unlock()

	if reason != 0 {
		return authResult{}, h.deny(reason, requester)
	}
	return authResult{rec: rec, res: res, effective: effective, requester: requester}, nil
}

// Enter routes a call into a resource callback. The caller must present a
// handle and the full current call chain (empty for a direct call by the
// handle owner). The callback runs under an operation lease: a lease
// acquired before Close may run to completion, but once Close has
// returned no new callback can enter the resource.
func (h *Host) Enter(handle Handle, op Op, chain []Link, fn func() error) error {
	auth, err := h.authorize(handle, chain)
	if err != nil {
		return err
	}
	if op == 0 || op&^knownOps != 0 || op&auth.effective != op {
		return h.deny(DenyOpNotAllowed, auth.requester)
	}
	if auth.res == nil || !auth.res.acquire() {
		return h.deny(DenyResourceClosed, auth.requester)
	}
	defer auth.res.release()
	return fn()
}

// Delegate mints a new handle on the same resource for another instance.
// Delegation can only narrow: the requested operations must be a subset of
// the effective operations along the current call chain. The delegatee
// must not already appear in the chain, and the extended chain must stay
// within MaxChainDepth.
func (h *Host) Delegate(handle Handle, chain []Link, to InstanceID, ops Op) (Handle, error) {
	var zero Handle
	auth, err := h.authorize(handle, chain)
	if err != nil {
		return zero, err
	}
	if ops == 0 || ops&^knownOps != 0 || ops&^auth.effective != 0 {
		return zero, h.deny(DenyDelegationWidens, auth.requester)
	}
	if len(chain) == 0 && to == auth.rec.instance {
		return zero, h.deny(DenyCycleDetected, auth.requester)
	}
	for _, link := range chain {
		if link.Instance == to {
			return zero, h.deny(DenyCycleDetected, auth.requester)
		}
	}
	if len(chain)+1 > MaxChainDepth {
		return zero, h.deny(DenyDepthExceeded, auth.requester)
	}

	h.mu.Lock()
	target, ok := h.instances[to]
	if !ok || target.retired || !target.active {
		h.mu.Unlock()
		return zero, fmt.Errorf("netsec: delegate target is not an active instance")
	}
	hid := handleID(randomID())
	h.handles[hid] = &capRecord{
		id:         hid,
		resource:   auth.rec.resource,
		rtype:      auth.rec.rtype,
		ops:        ops,
		instance:   to,
		generation: target.generation,
	}
	delegated := encodeHandle(hid, target.generation, auth.rec.rtype, ops, to)
	h.mu.Unlock()
	return delegated, nil
}
