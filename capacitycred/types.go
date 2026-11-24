package capacitycred

import "context"

// TenantID scopes every credential, event, receipt and task. Credentials can
// never cross tenant boundaries.
type TenantID string

// NodeID identifies a relay node in the cross-node handoff topology.
type NodeID string

// CredID is the unique identity of a capacity credential.
type CredID string

// TaskID is the unique identity of a KDF batch task. The same task id always
// maps to at most one outstanding reservation, even across crash re-entry.
type TaskID string

// EventID is a host-computed hash that uniquely binds an event body.
type EventID string

// Signature is an opaque signature verified by the host-injected verifier.
type Signature []byte

// HashFunc is provided by the host. The component never instantiates a hash
// implementation itself. The same callback both produces digests and runs the
// password KDF batch workload.
type HashFunc func(ctx context.Context, data []byte) ([]byte, error)

// VerifyFunc is provided by the host and validates a tenant-scoped signature.
// It must return nil only when sig is a valid signature of body for tenant.
type VerifyFunc func(tenant TenantID, body, sig []byte) error

// Callbacks are the only external primitives the component is allowed to
// schedule. Both are mandatory.
type Callbacks struct {
	Hash   HashFunc
	Verify VerifyFunc
}

func (c Callbacks) valid() error {
	if c.Hash == nil || c.Verify == nil {
		return ErrMissingCallback
	}
	return nil
}

// Interval is a half-open, root-global byte range [Low, High). Splits inside
// one credential must form a partition of [0, Total) so quota can never be
// duplicated.
type Interval struct {
	Low  uint64
	High uint64
}

func (i Interval) valid() bool { return i.Low < i.High }

func (i Interval) overlaps(o Interval) bool {
	return i.Low < o.High && o.Low < i.High
}

// Params are the KDF inputs whose commitment is carried by a credential chain.
// SCost is the memory cost parameter; each unit is exactly 32 bytes.
type Params struct {
	SCost      uint64
	Iterations uint64
	Parallel   uint32
	Salt       []byte
	Password   []byte
	Label      string
}

// MemoryBytes returns s_cost*32 with 64-bit overflow detection.
func MemoryBytes(sCost uint64) (uint64, error) {
	const blockBytes = uint64(32)
	if sCost != 0 && sCost > (^uint64(0))/blockBytes {
		return 0, ErrOverflow
	}
	return sCost * blockBytes, nil
}

// EventKind enumerates the signed causal events that mutate credential state.
type EventKind string

const (
	KindMint    EventKind = "mint"
	KindSplit   EventKind = "split"
	KindHandoff EventKind = "handoff"
	KindRevoke  EventKind = "revoke"
)

// ChildSpec is one interval produced by a split.
type ChildSpec struct {
	ID    CredID
	Range Interval
}

// Event is a signed causal mutation. Fields are interpreted by Kind:
//
//	mint:    CredID, Tenant, ParentID="", Range=[0,Total), From, ParamsCommitment
//	split:   CredID=parent, Tenant, ParentID=parent root, Children, Prev
//	handoff: CredID, Tenant, ParentID=root, From->To, Prev
//	revoke:  CredID, Tenant, ParentID=root, Prev
//
// Prev references the immediate predecessor event on the credential's causal
// chain and may be empty for an independent mint. Children may be observed
// before their parent mint because events arrive out of order; they stay in
// the unresolved queue until the chain becomes complete.
type Event struct {
	Kind             EventKind
	EventID          EventID
	Tenant           TenantID
	CredID           CredID
	ParentID         CredID
	From, To         NodeID
	Range            Interval
	Total            uint64
	ParamsCommitment []byte
	Children         []ChildSpec
	Prev             EventID
	Nonce            uint64
	Signature        Signature
}

// Receipt is the caller's finality acknowledgement. It binds a credential root
// and the exact prefix of the causal event chain that has been finalized.
type Receipt struct {
	RootCredID CredID
	Tenant     TenantID
	PrefixTip  EventID
	Signature  Signature
}

// Envelope carries an out-of-order inbound event with the host callbacks used
// to authenticate it. Nil callbacks are rejected at this entry as well as at
// engine construction.
type Envelope struct {
	Event     Event
	Callbacks Callbacks
}

// CredView is an immutable snapshot row for a credential.
type CredView struct {
	ID               CredID
	Root             CredID
	Tenant           TenantID
	Total            uint64
	Range            Interval
	ParamsCommitment []byte
	Owner            NodeID
	Parent           CredID
	Children         []CredID
	Quarantined      bool
	Revoked          bool
	HeldBytes        uint64
	FreeBytes        uint64
	Runnable         bool
	LocalOwner       bool
}

// Snapshot is the deterministic state produced by both incremental merging and
// full recomputation. Equal immutable event sets must yield equal snapshots.
type Snapshot struct {
	Credentials    []CredView
	FinalizedRoots map[CredID]Receipt
	Sequence       []EventID
	Conflicts      map[CredID][]EventID
}

// Slot is one delivered memory buffer managed through the host allocator.
type Slot interface {
	// Erase must overwrite the buffer content before its capacity is released.
	Erase()
}

// Allocator is the host-backed memory counter. Acquire delivers a slot whose
// size the component measures as s_cost*32; Release is only ever invoked after
// Erase.
type Allocator interface {
	Acquire(size uint64) (Slot, error)
	Release(Slot)
}
