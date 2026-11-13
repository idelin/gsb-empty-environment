package netsec

import "fmt"

// ObjectID is a logical object identity. Unlike Address, an ObjectID is never
// reused.
type ObjectID uint64

// Address is the reusable storage address of an object.
type Address uint64

// AllocationGeneration changes every time an address is allocated. It also
// changes when a dead address is reused, invalidating every old handle.
type AllocationGeneration uint64

// CapabilityGeneration identifies one grant of host resource access. Revocation
// increments it before clearing the resource.
type CapabilityGeneration uint64

// ResourceID identifies a host resource. Zero means "no resource".
type ResourceID uint64

// SlotID names a host-visible global slot.
type SlotID string

// ObjectState is the lifecycle state of an object.
type ObjectState uint8

const (
	// StateLive means the object is alive and its strong count can be increased.
	StateLive ObjectState = iota + 1
	// StateFinalizing means the strong count reached zero and one finalizer is
	// queued or running.
	StateFinalizing
	// StateDead means the object can no longer be observed or called. Its address
	// may be reused with a new allocation generation.
	StateDead
)

// Short aliases for callers that prefer Live/Finalizing/Dead.
const (
	Live       = StateLive
	Finalizing = StateFinalizing
	Dead       = StateDead
)

func (s ObjectState) String() string {
	switch s {
	case StateLive:
		return "live"
	case StateFinalizing:
		return "finalizing"
	case StateDead:
		return "dead"
	default:
		return fmt.Sprintf("ObjectState(%d)", uint8(s))
	}
}

// ObjectRef is a generation-qualified object identity. It is safe to retain
// after the object dies because every operation revalidates all three fields.
type ObjectRef struct {
	ID       ObjectID             `json:"id"`
	Address  Address              `json:"address"`
	AllocGen AllocationGeneration `json:"allocation_generation"`
}

// WeakRef can be upgraded only while the object is live and still has at least
// one strong reference.
type WeakRef struct {
	ObjectRef
}

// StrongRef owns one strong-reference token. The token is intentionally
// unexported so that a copied handle cannot manufacture additional ownership.
// Calling StrongDrop twice on copies of the same handle is harmless: only the
// first drop consumes the token.
type StrongRef struct {
	ObjectRef
	token uint64
}

// Identity returns the generation-qualified identity carried by the strong
// reference.
func (s *StrongRef) Identity() ObjectRef {
	if s == nil {
		return ObjectRef{}
	}
	return s.ObjectRef
}

// Ticket authorizes a method invocation. Every field is rechecked by Call;
// retaining a ticket does not retain the object or its capability.
type Ticket struct {
	Object     ObjectRef            `json:"object"`
	CapGen     CapabilityGeneration `json:"capability_generation"`
	ResourceID ResourceID           `json:"resource_id"`
}

// ResurrectionRequest records one untrusted request made from a finalizer.
// Requests are recorded even when the host denies them.
type ResurrectionRequest struct {
	Ordinal  int    `json:"ordinal"`
	Slot     SlotID `json:"slot"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// ObjectInfo is an immutable snapshot used by tests and host diagnostics.
type ObjectInfo struct {
	ObjectRef
	State                  ObjectState           `json:"state"`
	CapGen                 CapabilityGeneration  `json:"capability_generation"`
	ResourceID             ResourceID            `json:"resource_id"`
	LastResourceID         ResourceID            `json:"last_resource_id"`
	StrongRefs             int                   `json:"strong_refs"`
	Revoked                bool                  `json:"revoked"`
	HostAllowsResurrection bool                  `json:"host_allows_resurrection"`
	FinalizeQueued         bool                  `json:"finalize_queued"`
	FinalizeDone           bool                  `json:"finalize_done"`
	ResurrectionRequests   []ResurrectionRequest `json:"resurrection_requests,omitempty"`
}

// ResourceInfo is a host-side view of a resource.
type ResourceInfo struct {
	ID        ResourceID `json:"id"`
	Label     string     `json:"label"`
	Live      bool       `json:"live"`
	Calls     int        `json:"calls"`
	Clearings int        `json:"clearings"`
	ClearedBy ObjectID   `json:"cleared_by,omitempty"`
}

// DropResult describes the effect of consuming one strong token.
type DropResult struct {
	Matched        bool        `json:"matched"`
	Changed        bool        `json:"changed"`
	StrongRefs     int         `json:"strong_refs"`
	State          ObjectState `json:"state"`
	FinalizeQueued bool        `json:"finalize_queued"`
}

// RevokeResult describes capability revocation.
type RevokeResult struct {
	Matched         bool                 `json:"matched"`
	Changed         bool                 `json:"changed"`
	PriorCapGen     CapabilityGeneration `json:"prior_capability_generation"`
	CapGen          CapabilityGeneration `json:"capability_generation"`
	ResourceCleared bool                 `json:"resource_cleared"`
	State           ObjectState          `json:"state"`
}

// CallResult describes a method invocation. Calls is the cumulative number of
// real host-resource calls, so a failed call never increments it.
type CallResult struct {
	Success    bool       `json:"success"`
	Reason     string     `json:"reason"`
	ResourceID ResourceID `json:"resource_id"`
	Calls      int        `json:"calls"`
}

// SlotInfo describes the object currently installed in a global slot.
type SlotInfo struct {
	Slot    SlotID               `json:"slot"`
	Object  ObjectRef            `json:"object"`
	State   ObjectState          `json:"state"`
	CapGen  CapabilityGeneration `json:"capability_generation"`
	Revoked bool                 `json:"revoked"`
}

// SlotSetResult describes replacement of a global slot. The previous slot
// owner loses the slot's strong reference and may therefore have a finalizer
// queued.
type SlotSetResult struct {
	Changed        bool      `json:"changed"`
	Previous       *SlotInfo `json:"previous,omitempty"`
	FinalizeQueued bool      `json:"finalize_queued"`
}

// FinalizeReport is returned after one finalizer completes.
type FinalizeReport struct {
	ObjectRef
	PriorCapGen        CapabilityGeneration  `json:"prior_capability_generation"`
	CapGen             CapabilityGeneration  `json:"capability_generation"`
	FinalState         ObjectState           `json:"final_state"`
	Requests           []ResurrectionRequest `json:"requests,omitempty"`
	Slot               SlotID                `json:"slot,omitempty"`
	PreviousSlotOwner  *SlotInfo             `json:"previous_slot_owner,omitempty"`
	SlotFinalizeQueued bool                  `json:"slot_finalize_queued"`
	WeakGetAttempts    int                   `json:"weak_get_attempts"`
	WeakGetSuccesses   int                   `json:"weak_get_successes"`
	ResourceCleared    bool                  `json:"resource_cleared"`
	Panic              string                `json:"panic,omitempty"`
}

// Finalizer is untrusted script code. It may inspect the weak identity and
// request slots, but it cannot grant itself strong ownership.
type Finalizer func(*FinalizeContext)

// FinalizeContext is the only script-facing API available from a finalizer.
type FinalizeContext struct {
	rt  *Runtime
	ref ObjectRef

	closed bool
}

// Object returns the identity of the object being finalized.
func (c *FinalizeContext) Object() ObjectRef {
	return c.ref
}

// WeakGet is an attempted weak-to-strong upgrade from inside the callback The
// object is finalizing, so the safe runtime always rejects it.
func (c *FinalizeContext) WeakGet() (*StrongRef, bool) {
	return c.rt.finalizerWeakGet(c, WeakRef{ObjectRef: c.ref})
}

// RequestResurrect records an untrusted request. Nothing is written to the slot
// until the host-approved finalization decision is made.
func (c *FinalizeContext) RequestResurrect(slot SlotID) bool {
	return c.rt.finalizerResurrect(c, slot)
}
