package netsec

import "sync"

// Access is a bitmask of the operations a registered-file slot may serve.
type Access uint8

const (
	AccessNone Access = 0
	AccessRead Access = 1 << iota
	AccessWrite
)

// Can reports whether the mask grants every bit in want.
func (a Access) Can(want Access) bool { return a&want == want }

// Credential is the immutable credential snapshot pinned to a registered
// file. PersonalityID is the id the client must present in sqe.personality;
// CredID is the identity whose authority covers the slot.
type Credential struct {
	CredID        uint64
	PersonalityID uint16
	UID           uint32
	GID           uint32
}

// SlotState marks the lifecycle of a registered-files entry.
type SlotState uint8

const (
	SlotEmpty   SlotState = iota // never registered, or already revoked
	SlotActive                   // usable by a validated SQE
	SlotRevoked                  // the backing fd was closed or its rights were taken away
)

// Slot is one entry of the proxy's registered-files table. A Revoked slot
// stays in the table forever: its index must never be reactivated by simply
// re-registering the same number, so callers must go through RegisterSlot
// (which bumps the generation) instead of mutating the entry in place.
type Slot struct {
	State       SlotState
	Access      Access
	Cred        Credential
	Description string
	Generation  uint64
}

// SlotTable is the concurrency-safe, credential-aware registered-files table
// used by the validator. Revocation is sticky: Revoke flips the slot to
// SlotRevoked and all later validations referencing it fail until a fresh
// RegisterSlot call replaces it with a new generation.
type SlotTable struct {
	mu    sync.RWMutex
	slots []Slot
}

// NewSlotTable allocates a table able to hold size registered files.
func NewSlotTable(size int) *SlotTable {
	if size < 0 {
		size = 0
	}
	return &SlotTable{slots: make([]Slot, size)}
}

// Len returns the table capacity.
func (t *SlotTable) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.slots)
}

// RegisterSlot installs or replaces the entry at index and starts a new
// generation. Revoked entries can only come back through this path.
func (t *SlotTable) RegisterSlot(index uint64, access Access, cred Credential, desc string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index >= uint64(len(t.slots)) {
		return ErrSlotOutOfRange
	}
	prev := t.slots[index]
	t.slots[index] = Slot{
		State:       SlotActive,
		Access:      access,
		Cred:        cred,
		Description: desc,
		Generation:  prev.Generation + 1,
	}
	return nil
}

// Revoke marks index revoked. The original fd being closed or losing its
// rights must trigger this; the kernel-level fixed-file reference may outlive
// the fd, so the proxy has to enforce revocation itself. Revoking an empty
// slot still flips it to SlotRevoked so the index cannot be used before a
// proper (new-generation) registration.
func (t *SlotTable) Revoke(index uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index >= uint64(len(t.slots)) {
		return ErrSlotOutOfRange
	}
	t.slots[index].State = SlotRevoked
	return nil
}

// SetAccess narrows or restores the rights of an active slot without changing
// its generation or credential. It never reactivates a revoked slot.
func (t *SlotTable) SetAccess(index uint64, access Access) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index >= uint64(len(t.slots)) {
		return ErrSlotOutOfRange
	}
	if t.slots[index].State != SlotActive {
		return ErrSlotNotActive
	}
	t.slots[index].Access = access
	return nil
}

// Lookup returns a value copy of the slot. The copy is what the validator
// consumes, so a concurrent Revoke can never partially mutate an in-flight
// decision.
func (t *SlotTable) Lookup(index uint64) (Slot, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if index >= uint64(len(t.slots)) {
		return Slot{}, false
	}
	return t.slots[index], true
}

// Snapshot returns a point-in-time copy of the whole table. Batch/chain
// validation runs against the snapshot so all linked SQEs are judged under
// the same table state (no TOCTOU across the chain).
func (t *SlotTable) Snapshot() []Slot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Slot, len(t.slots))
	copy(out, t.slots)
	return out
}
