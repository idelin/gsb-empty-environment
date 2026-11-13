package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"sync"
)

// PENDING REVIEW -- NOT FOR PRODUCTION.
//
// ReviewTable16 is the 16-bit generation variant under audit. Its
// generation wraps modulo 2^16 on revoke instead of burning the slot, so
// after 2^16 revoke/allocate cycles a stale ticket verifies again and
// resolves to a *different* object. The model proof in
// model_proof_test.go demonstrates the hazard; Table (32-bit, burning)
// is the correct design.

// Ticket16 is the 16-bit-generation capability ticket.
type Ticket16 struct {
	TableID    [tableIDSize]byte
	Slot       uint32
	Generation uint16
	Perms      Perms
	ObjType    ObjType
	MAC        [macSize]byte
}

type slot16 struct {
	generation uint16
	obj        any
	objType    ObjType
	perms      Perms
	occupied   bool
}

// ReviewTable16 mirrors Table with a wrapping 16-bit generation.
type ReviewTable16 struct {
	mu     sync.Mutex
	id     [tableIDSize]byte
	macKey []byte
	slots  []slot16
}

func NewReviewTable16(numSlots int, macKey []byte) (*ReviewTable16, error) {
	if numSlots <= 0 {
		return nil, errors.New("captable: table needs at least one slot")
	}
	if len(macKey) == 0 {
		return nil, errors.New("captable: HMAC key required")
	}
	t := &ReviewTable16{
		macKey: append([]byte(nil), macKey...),
		slots:  make([]slot16, numSlots),
	}
	if _, err := rand.Read(t.id[:]); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *ReviewTable16) mac(slot uint32, generation uint16, perms Perms, objType ObjType) [macSize]byte {
	h := hmac.New(sha256.New, t.macKey)
	var buf [tableIDSize + 4 + 2 + 4 + 2]byte
	copy(buf[0:tableIDSize], t.id[:])
	binary.BigEndian.PutUint32(buf[16:20], slot)
	binary.BigEndian.PutUint16(buf[20:22], generation)
	binary.BigEndian.PutUint32(buf[22:26], uint32(perms))
	binary.BigEndian.PutUint16(buf[26:28], uint16(objType))
	h.Write(buf[:])
	var out [macSize]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Allocate takes the current (possibly reused) generation of a free slot.
func (t *ReviewTable16) Allocate(obj any, objType ObjType, perms Perms) (Ticket16, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.slots {
		s := &t.slots[i]
		if s.occupied {
			continue
		}
		s.occupied = true
		s.obj = obj
		s.objType = objType
		s.perms = perms
		tk := Ticket16{
			TableID:    t.id,
			Slot:       uint32(i),
			Generation: s.generation,
			Perms:      perms,
			ObjType:    objType,
		}
		tk.MAC = t.mac(tk.Slot, tk.Generation, perms, objType)
		return tk, nil
	}
	return Ticket16{}, ErrTableFull
}

// Use resolves a 16-bit ticket. It accepts a wrapped-around stale ticket:
// that is the hazard under review.
func (t *ReviewTable16) Use(tk Ticket16, need Perms) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
	if live != 1 {
		return nil, ErrStale
	}
	if subtle.ConstantTimeEq(int32(tk.Perms&need), int32(need)) != 1 {
		return nil, ErrNotSubset
	}
	return s.obj, nil
}

// Revoke clears the object and increments the 16-bit generation, which
// WRAPS modulo 2^16. The slot is never burned.
func (t *ReviewTable16) Revoke(tk Ticket16) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	expected := t.mac(tk.Slot, tk.Generation, tk.Perms, tk.ObjType)
	ok := subtle.ConstantTimeCompare(tk.MAC[:], expected[:])
	ok &= subtle.ConstantTimeCompare(tk.TableID[:], t.id[:])
	if ok != 1 {
		return ErrBadMAC
	}
	if tk.Slot >= uint32(len(t.slots)) {
		return ErrSlotRange
	}
	s := &t.slots[tk.Slot]
	live := subtle.ConstantTimeEq(int32(s.generation), int32(tk.Generation))
	live &= subtle.ConstantTimeEq(ctBool(s.occupied), 1)
	if live != 1 {
		return ErrStale
	}
	s.obj = nil
	s.occupied = false
	s.perms = 0
	s.generation++ // wraps 65535 -> 0: the hazard under review
	return nil
}
