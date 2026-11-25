package volrecover

import (
	"sort"
)

// ImageView is the read-only block image abstraction consumed by the host and
// the verifier. It exposes the current unit records, stored signed headers,
// the committed root pointer, and the durable monotonic revocation register.
type ImageView interface {
	Unit(id UnitID) (Record, bool)
	Units() []Record
	Header(id Digest) (*Header, bool)
	Headers() []*Header
	RootPointer() Digest
	Revocation(slot SlotID) (seq uint64, revoked bool)
	Revocations() map[SlotID]uint64
}

// memStore is one concrete state of the in-memory block device. A crash
// enumeration forks independent copies of this state.
type memStore struct {
	records    map[UnitID]Record
	headers    map[Digest]*Header
	root       Digest
	revocation map[SlotID]uint64
}

// MemDevice is an in-memory block device used by hosts and tests. It never
// touches real storage and its state can be cloned for crash enumeration.
type MemDevice struct {
	state memStore
}

// NewMemDevice creates an empty memory device with the given committed root
// pointer.
func NewMemDevice(rootPointer Digest) *MemDevice {
	return &MemDevice{state: memStore{
		records:    map[UnitID]Record{},
		headers:    map[Digest]*Header{},
		root:       rootPointer,
		revocation: map[SlotID]uint64{},
	}}
}

// WriteUnit records one independently-written unit. A later write replaces
// the full (version, commitment) pair atomically at the model level; crash
// enumeration decides which writes actually survive.
func (d *MemDevice) WriteUnit(id UnitID, version uint64, payload []byte) {
	d.state.records[id] = Record{Unit: id, Version: version, Commit: UnitCommit(payload)}
}

// WriteRecord writes an already-computed unit record.
func (d *MemDevice) WriteRecord(rec Record) {
	d.state.records[rec.Unit] = rec
}

// PutHeader stores a signed header addressed by its id.
func (d *MemDevice) PutHeader(h *Header) {
	cp := *h
	if len(h.Sig) != 0 {
		cp.Sig = append([]byte(nil), h.Sig...)
	}
	d.state.headers[h.ID] = &cp
}

// SetRootPointer commits the current root header pointer.
func (d *MemDevice) SetRootPointer(id Digest) { d.state.root = id }

// Revoke records one slot in the reliable monotonic revocation register. The
// register cannot be rolled back.
func (d *MemDevice) Revoke(slot SlotID, seq uint64) {
	if old, ok := d.state.revocation[slot]; !ok || seq > old {
		d.state.revocation[slot] = seq
	}
}

// Snapshot returns an independent deep copy of the device state.
func (d *MemDevice) Snapshot() *MemDevice {
	return &MemDevice{state: cloneStore(d.state)}
}

// View returns a read-only view over the current state. The view stays pinned
// to the state captured here; later device writes do not change it.
func (d *MemDevice) View() *MemView {
	return &MemView{store: cloneStore(d.state)}
}

// MemView is a pinned read-only snapshot of a MemDevice.
type MemView struct {
	store memStore
}

// Unit returns the current record for a physical unit.
func (v *MemView) Unit(id UnitID) (Record, bool) {
	rec, ok := v.store.records[id]
	return rec, ok
}

// Units returns all current unit records sorted by unit id.
func (v *MemView) Units() []Record {
	out := make([]Record, 0, len(v.store.records))
	for _, rec := range v.store.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out
}

// Header looks up a stored header by id.
func (v *MemView) Header(id Digest) (*Header, bool) {
	h, ok := v.store.headers[id]
	if !ok {
		return nil, false
	}
	cp := *h
	return &cp, true
}

// Headers returns every stored header sorted by id for deterministic output.
func (v *MemView) Headers() []*Header {
	type pair struct {
		id Digest
		h  *Header
	}
	all := make([]pair, 0, len(v.store.headers))
	for id, h := range v.store.headers {
		all = append(all, pair{id: id, h: h})
	}
	sort.Slice(all, func(i, j int) bool {
		for b := 0; b < 32; b++ {
			if all[i].id[b] != all[j].id[b] {
				return all[i].id[b] < all[j].id[b]
			}
		}
		return false
	})
	out := make([]*Header, 0, len(all))
	for _, p := range all {
		cp := *p.h
		out = append(out, &cp)
	}
	return out
}

// RootPointer returns the committed root header pointer.
func (v *MemView) RootPointer() Digest { return v.store.root }

// Revocation returns the revocation sequence for a slot when it is revoked.
func (v *MemView) Revocation(slot SlotID) (uint64, bool) {
	seq, ok := v.store.revocation[slot]
	return seq, ok
}

// Revocations returns a copy of the monotonic revocation register.
func (v *MemView) Revocations() map[SlotID]uint64 {
	out := make(map[SlotID]uint64, len(v.store.revocation))
	for slot, seq := range v.store.revocation {
		out[slot] = seq
	}
	return out
}

func cloneStore(s memStore) memStore {
	cp := memStore{
		records:    make(map[UnitID]Record, len(s.records)),
		headers:    make(map[Digest]*Header, len(s.headers)),
		root:       s.root,
		revocation: make(map[SlotID]uint64, len(s.revocation)),
	}
	for id, rec := range s.records {
		cp.records[id] = rec
	}
	for id, h := range s.headers {
		hc := *h
		if len(h.Sig) != 0 {
			hc.Sig = append([]byte(nil), h.Sig...)
		}
		cp.headers[id] = &hc
	}
	for slot, seq := range s.revocation {
		cp.revocation[slot] = seq
	}
	return cp
}

var _ ImageView = (*MemView)(nil)
