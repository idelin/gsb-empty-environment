package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// MaxGeneration is the largest slot generation. A slot whose generation
// reaches MaxGeneration is permanently burned on revoke; it never wraps.
const MaxGeneration = ^uint32(0)

const (
	tableIDSize = 16
	macSize     = sha256.Size
)

var (
	ErrTableFull  = errors.New("captable: no free slot")
	ErrBadMAC     = errors.New("captable: ticket MAC verification failed")
	ErrSlotRange  = errors.New("captable: slot index out of range")
	ErrStale      = errors.New("captable: ticket does not match live table entry")
	ErrNotSubset  = errors.New("captable: requested permissions are not a subset")
	ErrEmptyPerms = errors.New("captable: ticket must carry at least one permission")
)

// Perms is a permission bitmask granted to a capability ticket.
type Perms uint32

// ObjType tags the type of object a slot holds; it is bound into the MAC.
type ObjType uint16

// Ticket is an indirect capability. All fields are covered by MAC; the
// table ID is fixed per table and never taken from a request.
type Ticket struct {
	TableID    [tableIDSize]byte
	Slot       uint32
	Generation uint32
	Perms      Perms
	ObjType    ObjType
	MAC        [macSize]byte
}

func (t Ticket) String() string {
	return fmt.Sprintf("Ticket{table:%x slot:%d gen:%d perms:%#x type:%d}",
		t.TableID[:4], t.Slot, t.Generation, uint32(t.Perms), uint16(t.ObjType))
}

type slotState struct {
	generation uint32
	obj        any
	objType    ObjType
	perms      Perms
	occupied   bool
	burned     bool
}

// Table is a host-held indirect capability table. Objects are referenced
// through slots so revocation only bumps a generation counter.
type Table struct {
	mu     sync.Mutex
	id     [tableIDSize]byte
	macKey []byte
	slots  []slotState
}

// NewTable creates a table with numSlots slots. The table ID is generated
// internally and fixed until ClearTable; macKey is the host HMAC key.
func NewTable(numSlots int, macKey []byte) (*Table, error) {
	if numSlots <= 0 {
		return nil, errors.New("captable: table needs at least one slot")
	}
	if len(macKey) == 0 {
		return nil, errors.New("captable: HMAC key required")
	}
	t := &Table{
		macKey: append([]byte(nil), macKey...),
		slots:  make([]slotState, numSlots),
	}
	if _, err := rand.Read(t.id[:]); err != nil {
		return nil, fmt.Errorf("captable: table ID: %w", err)
	}
	return t, nil
}

// TableID returns the current fixed table identifier.
func (t *Table) TableID() [tableIDSize]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id
}

// mac computes HMAC-SHA256 over tableID || slot || generation || perms ||
// objType. Every ticket field is covered.
func (t *Table) mac(slot, generation uint32, perms Perms, objType ObjType) [macSize]byte {
	h := hmac.New(sha256.New, t.macKey)
	var buf [tableIDSize + 4 + 4 + 4 + 2]byte
	copy(buf[0:tableIDSize], t.id[:])
	binary.BigEndian.PutUint32(buf[16:20], slot)
	binary.BigEndian.PutUint32(buf[20:24], generation)
	binary.BigEndian.PutUint32(buf[24:28], uint32(perms))
	binary.BigEndian.PutUint16(buf[28:30], uint16(objType))
	h.Write(buf[:])
	var out [macSize]byte
	copy(out[:], h.Sum(nil))
	return out
}

// mintLocked issues a ticket for an occupied slot. Caller holds t.mu.
func (t *Table) mintLocked(slotIdx uint32, s *slotState, perms Perms) Ticket {
	tk := Ticket{
		TableID:    t.id,
		Slot:       slotIdx,
		Generation: s.generation,
		Perms:      perms,
		ObjType:    s.objType,
	}
	tk.MAC = t.mac(slotIdx, s.generation, perms, s.objType)
	return tk
}

func ctBool(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

// validateLocked authenticates a ticket against the live entry: MAC first,
// then constant-time comparison of generation, type and permissions.
// Caller holds t.mu.
func (t *Table) validateLocked(tk *Ticket) (*slotState, error) {
	expected := t.mac(tk.Slot, tk.Generation, tk.Perms, tk.ObjType)
	ok := subtle.ConstantTimeCompare(tk.MAC[:], expected[:])
	ok &= subtle.ConstantTimeCompare(tk.TableID[:], t.id[:])
	if ok != 1 {
		return nil, ErrBadMAC
	}
	if tk.Slot >= uint32(len(t.slots)) {
		return nil, ErrSlotRange
	}
	s := &t.slots[tk.Slot]
	live := subtle.ConstantTimeEq(int32(s.generation), int32(tk.Generation))
	live &= subtle.ConstantTimeEq(int32(s.objType), int32(tk.ObjType))
	live &= subtle.ConstantTimeEq(int32(tk.Perms&^s.perms), 0)
	live &= subtle.ConstantTimeEq(ctBool(s.occupied), 1)
	live &= subtle.ConstantTimeEq(ctBool(s.burned), 0)
	if live != 1 {
		return nil, ErrStale
	}
	return s, nil
}

// Allocate takes the current generation of a free slot and writes the
// object into it, returning a full-permission ticket.
func (t *Table) Allocate(obj any, objType ObjType, perms Perms) (Ticket, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.slots {
		if s := &t.slots[i]; !s.occupied && !s.burned {
			return t.allocateLocked(uint32(i), obj, objType, perms), nil
		}
	}
	return Ticket{}, ErrTableFull
}

func (t *Table) allocateLocked(slotIdx uint32, obj any, objType ObjType, perms Perms) Ticket {
	s := &t.slots[slotIdx]
	s.occupied = true
	s.obj = obj
	s.objType = objType
	s.perms = perms
	return t.mintLocked(slotIdx, s, perms)
}

// Issue derives a permission-subset ticket from an allocated object. The
// base ticket must be live and perms must be a subset of the base perms.
func (t *Table) Issue(base Ticket, perms Perms) (Ticket, error) {
	if perms == 0 {
		return Ticket{}, ErrEmptyPerms
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, err := t.validateLocked(&base)
	if err != nil {
		return Ticket{}, err
	}
	if subtle.ConstantTimeEq(int32(perms&^base.Perms), 0) != 1 {
		return Ticket{}, ErrNotSubset
	}
	return t.mintLocked(base.Slot, s, perms), nil
}

// Use resolves a ticket to its object. Linearization point: the table lock
// is held across the entry read and the capability check, so a Use either
// observes the entry before a Revoke or fails after it.
func (t *Table) Use(tk Ticket, need Perms) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, err := t.validateLocked(&tk)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeEq(int32(tk.Perms&need), int32(need)) != 1 {
		return nil, ErrNotSubset
	}
	return s.obj, nil
}

// Revoke clears the object and advances the slot generation. At
// MaxGeneration the slot is permanently burned; it never wraps.
func (t *Table) Revoke(tk Ticket) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.validateLocked(&tk); err != nil {
		return err
	}
	t.revokeSlotLocked(tk.Slot)
	return nil
}

func (t *Table) revokeSlotLocked(slotIdx uint32) {
	s := &t.slots[slotIdx]
	s.obj = nil
	s.occupied = false
	s.perms = 0
	if s.generation == MaxGeneration {
		s.burned = true
		return
	}
	s.generation++
}

// ClearTable rotates to a fresh table ID, burning every outstanding ticket
// of the old table (their MACs no longer verify), and empties all slots.
// Generation counters and burned flags are kept as defense in depth.
func (t *Table) ClearTable() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := rand.Read(t.id[:]); err != nil {
		return fmt.Errorf("captable: table ID: %w", err)
	}
	for i := range t.slots {
		t.slots[i].obj = nil
		t.slots[i].perms = 0
		t.slots[i].occupied = false
	}
	return nil
}

// Txn is a batch of revokes and allocates. Commit validates every step
// first and then publishes atomically; a failed commit changes nothing, so
// no slot is partially reused.
type Txn struct {
	table   *Table
	revokes []Ticket
	allocs  []allocReq
}

type allocReq struct {
	obj     any
	objType ObjType
	perms   Perms
}

func (t *Table) BeginTxn() *Txn { return &Txn{table: t} }

func (x *Txn) Revoke(tk Ticket) { x.revokes = append(x.revokes, tk) }

func (x *Txn) Allocate(obj any, objType ObjType, perms Perms) {
	x.allocs = append(x.allocs, allocReq{obj: obj, objType: objType, perms: perms})
}

// Commit validates the whole batch under one lock acquisition and applies
// it only if every step checks out.
func (x *Txn) Commit() ([]Ticket, error) {
	t := x.table
	t.mu.Lock()
	defer t.mu.Unlock()

	// Phase 1: validate; no mutation.
	seen := make(map[uint32]bool, len(x.revokes))
	for i := range x.revokes {
		tk := &x.revokes[i]
		if _, err := t.validateLocked(tk); err != nil {
			return nil, fmt.Errorf("captable: txn revoke %d: %w", i, err)
		}
		if seen[tk.Slot] {
			return nil, fmt.Errorf("captable: txn revoke %d: duplicate slot %d", i, tk.Slot)
		}
		seen[tk.Slot] = true
	}
	free := 0
	for i := range t.slots {
		if !t.slots[i].occupied && !t.slots[i].burned {
			free++
		}
	}
	if len(x.allocs) > free+len(x.revokes) {
		return nil, ErrTableFull
	}

	// Phase 2: atomic publish.
	for _, tk := range x.revokes {
		t.revokeSlotLocked(tk.Slot)
	}
	issued := make([]Ticket, 0, len(x.allocs))
	for _, a := range x.allocs {
		for i := range t.slots {
			if s := &t.slots[i]; !s.occupied && !s.burned {
				issued = append(issued, t.allocateLocked(uint32(i), a.obj, a.objType, a.perms))
				break
			}
		}
	}
	return issued, nil
}
