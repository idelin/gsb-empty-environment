package netsec

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// handleSlot is one entry in a process handle table.
type handleSlot struct {
	open             bool
	object           ObjectID
	objectGeneration uint32
	granted          Access // concrete bits, generics already expanded
	inherit          bool
	generation       uint32 // handle generation, bumped on every slot reuse
}

type process struct {
	id         uint32
	generation uint32
	integrity  IntegrityLevel
	table      []handleSlot
}

type object struct {
	id         ObjectID
	typ        ObjectType
	generation uint32
}

func (p *process) allocSlot() uint32 {
	for i := range p.table {
		if !p.table[i].open {
			p.table[i].open = true
			p.table[i].generation++
			return uint32(i)
		}
	}
	p.table = append(p.table, handleSlot{open: true, generation: 1})
	return uint32(len(p.table) - 1)
}

// Host is the trusted handle-table model. It validates every duplication
// against the live state at the linearization point and never trusts
// request-supplied access masks or stale references.
type Host struct {
	secret  uint64
	types   map[string]ObjectType
	procs   map[uint32]*process
	objects map[ObjectID]*object
}

// NewHost creates an empty host. secret keys the broker-capability MAC.
func NewHost(secret uint64) *Host {
	return &Host{
		secret:  secret,
		types:   make(map[string]ObjectType),
		procs:   make(map[uint32]*process),
		objects: make(map[ObjectID]*object),
	}
}

// AddProcess registers a process with a unique ID; generation starts at 1.
func (h *Host) AddProcess(id uint32, lvl IntegrityLevel) ProcessRef {
	h.procs[id] = &process{id: id, generation: 1, integrity: lvl}
	return ProcessRef{ID: id, Generation: 1}
}

// RestartProcess bumps the process generation and closes all its handles.
// Every reference bound to the old generation is now stale.
func (h *Host) RestartProcess(id uint32) {
	if p, ok := h.procs[id]; ok {
		p.generation++
		p.table = nil
	}
}

// AddObject registers a kernel object; generation starts at 1.
func (h *Host) AddObject(id ObjectID, typ ObjectType) {
	h.objects[id] = &object{id: id, typ: typ, generation: 1}
	h.types[typ.Name] = typ
}

// RetireObject bumps the object generation, modelling object replacement.
// Handles to the old generation can no longer be duplicated.
func (h *Host) RetireObject(id ObjectID) {
	if o, ok := h.objects[id]; ok {
		o.generation++
	}
}

// ProcRef returns the current reference (ID + generation) for a process.
func (h *Host) ProcRef(id uint32) (ProcessRef, bool) {
	p, ok := h.procs[id]
	if !ok {
		return ProcessRef{}, false
	}
	return ProcessRef{ID: p.id, Generation: p.generation}, true
}

// HandleGeneration returns the current handle generation of a slot.
func (h *Host) HandleGeneration(procID uint32, slot uint32) (uint32, bool) {
	p, ok := h.procs[procID]
	if !ok || int(slot) >= len(p.table) {
		return 0, false
	}
	return p.table[slot].generation, true
}

func (h *Host) resolveProc(ref ProcessRef) (*process, error) {
	p, ok := h.procs[ref.ID]
	if !ok || p.generation != ref.Generation {
		return nil, ErrNoSuchProcess
	}
	return p, nil
}

// OpenHandle is the trusted open path: the host expands generic bits and
// installs a handle with the resulting concrete granted access.
func (h *Host) OpenHandle(ref ProcessRef, objID ObjectID, access Access, inherit bool) (HandleRef, error) {
	p, err := h.resolveProc(ref)
	if err != nil {
		return HandleRef{}, err
	}
	o, ok := h.objects[objID]
	if !ok {
		return HandleRef{}, ErrNoSuchObject
	}
	granted := o.typ.Expand(access)
	if granted == 0 {
		return HandleRef{}, ErrEmptyAccess
	}
	idx := p.allocSlot()
	s := &p.table[idx]
	s.object = objID
	s.objectGeneration = o.generation
	s.granted = granted
	s.inherit = inherit
	return HandleRef{Slot: idx, Generation: s.generation}, nil
}

// CloseHandle closes a handle. The slot may be reused later; references to
// the old handle generation then fail validation.
func (h *Host) CloseHandle(ref ProcessRef, href HandleRef) error {
	p, err := h.resolveProc(ref)
	if err != nil {
		return err
	}
	if int(href.Slot) >= len(p.table) {
		return ErrNoSuchHandle
	}
	s := &p.table[href.Slot]
	if !s.open || s.generation != href.Generation {
		return ErrStaleHandle
	}
	s.open = false
	return nil
}

func (h *Host) capMAC(src, tgt ProcessRef, typeName string) uint64 {
	w := fnv.New64a()
	var buf [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		w.Write(buf[:])
	}
	put(h.secret)
	put(uint64(src.ID))
	put(uint64(src.Generation))
	put(uint64(tgt.ID))
	put(uint64(tgt.Generation))
	w.Write([]byte(typeName))
	return w.Sum64()
}

// IssueBrokerCapability mints a capability bound to the current generations
// of both processes and to the object type.
func (h *Host) IssueBrokerCapability(src, tgt ProcessRef, typeName string) (*Capability, error) {
	if _, err := h.resolveProc(src); err != nil {
		return nil, err
	}
	if _, err := h.resolveProc(tgt); err != nil {
		return nil, err
	}
	if _, ok := h.types[typeName]; !ok {
		return nil, ErrNoSuchObjectType
	}
	return &Capability{Source: src, Target: tgt, ObjectType: typeName, mac: h.capMAC(src, tgt, typeName)}, nil
}

func (h *Host) verifyCapability(c *Capability, src, tgt ProcessRef, typeName string) bool {
	return c != nil &&
		c.Source == src && c.Target == tgt && c.ObjectType == typeName &&
		c.mac == h.capMAC(src, tgt, typeName)
}

// dupPlan is a fully validated duplication, ready to publish.
type dupPlan struct {
	tgt     *process
	obj     *object
	access  Access
	inherit bool
}

// validate checks a duplicate request against the live state. It performs no
// mutation, so a batch can validate every item before publishing any.
func (h *Host) validate(req DuplicateRequest) (dupPlan, error) {
	src, err := h.resolveProc(req.Source)
	if err != nil {
		return dupPlan{}, err
	}
	tgt, err := h.resolveProc(req.Target)
	if err != nil {
		return dupPlan{}, err
	}
	if int(req.SourceHandle.Slot) >= len(src.table) {
		return dupPlan{}, ErrNoSuchHandle
	}
	slot := &src.table[req.SourceHandle.Slot]
	if !slot.open || slot.generation != req.SourceHandle.Generation {
		return dupPlan{}, ErrStaleHandle
	}
	obj := h.objects[slot.object]
	if obj.generation != slot.objectGeneration {
		return dupPlan{}, ErrStaleObject
	}
	if src.integrity != tgt.integrity &&
		!h.verifyCapability(req.Broker, req.Source, req.Target, obj.typ.Name) {
		return dupPlan{}, ErrBrokerRequired
	}
	if req.SameAccess && req.DesiredAccess != 0 {
		return dupPlan{}, ErrAccessConflict
	}
	access := slot.granted
	if !req.SameAccess {
		access = obj.typ.Expand(req.DesiredAccess)
	}
	if access == 0 {
		return dupPlan{}, ErrEmptyAccess
	}
	if access&^slot.granted != 0 {
		return dupPlan{}, ErrAccessNotGranted
	}
	if access&^obj.typ.CopyableMask != 0 {
		return dupPlan{}, ErrAccessNotCopyable
	}
	return dupPlan{tgt: tgt, obj: obj, access: access, inherit: req.Inherit}, nil
}

// publish installs a validated duplication into a fresh target slot with a
// new handle generation chosen by the host.
func (h *Host) publish(plan dupPlan) HandleRef {
	idx := plan.tgt.allocSlot()
	s := &plan.tgt.table[idx]
	s.object = plan.obj.id
	s.objectGeneration = plan.obj.generation
	s.granted = plan.access
	s.inherit = plan.inherit
	return HandleRef{Slot: idx, Generation: s.generation}
}

// Duplicate validates one request and publishes the new handle atomically.
func (h *Host) Duplicate(req DuplicateRequest) (HandleRef, error) {
	plan, err := h.validate(req)
	if err != nil {
		return HandleRef{}, err
	}
	return h.publish(plan), nil
}

// DuplicateBatch validates every request against the pre-batch state and
// publishes all of them atomically. Any failure leaves no partial handles.
func (h *Host) DuplicateBatch(reqs []DuplicateRequest) ([]HandleRef, error) {
	plans := make([]dupPlan, len(reqs))
	for i, r := range reqs {
		p, err := h.validate(r)
		if err != nil {
			return nil, fmt.Errorf("batch item %d: %w", i, err)
		}
		plans[i] = p
	}
	refs := make([]HandleRef, len(plans))
	for i, p := range plans {
		refs[i] = h.publish(p)
	}
	return refs, nil
}

// EffectiveAccess returns the expanded access of an open handle.
func (h *Host) EffectiveAccess(procID uint32, slotIdx uint32) (Access, bool) {
	p, ok := h.procs[procID]
	if !ok || int(slotIdx) >= len(p.table) {
		return 0, false
	}
	s := p.table[slotIdx]
	if !s.open {
		return 0, false
	}
	return h.objects[s.object].typ.Expand(s.granted), true
}

// HasEffectiveAccess reports whether the process holds any open handle whose
// expanded access includes bit.
func (h *Host) HasEffectiveAccess(procID uint32, bit Access) bool {
	p, ok := h.procs[procID]
	if !ok {
		return false
	}
	for i := range p.table {
		if !p.table[i].open {
			continue
		}
		if h.objects[p.table[i].object].typ.Expand(p.table[i].granted)&bit != 0 {
			return true
		}
	}
	return false
}

// HandleCount returns the number of open handles of a process.
func (h *Host) HandleCount(procID uint32) int {
	p, ok := h.procs[procID]
	if !ok {
		return 0
	}
	n := 0
	for i := range p.table {
		if p.table[i].open {
			n++
		}
	}
	return n
}

// StateKey encodes the full host state for BFS deduplication.
func (h *Host) StateKey() string {
	var b strings.Builder
	pids := make([]uint32, 0, len(h.procs))
	for id := range h.procs {
		pids = append(pids, id)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	for _, id := range pids {
		p := h.procs[id]
		fmt.Fprintf(&b, "P%d#g%d^%d(", p.id, p.generation, p.integrity)
		for i, s := range p.table {
			if s.open {
				fmt.Fprintf(&b, "%d:O%d#%d,a%x,i%t,g%d;", i, s.object, s.objectGeneration, s.granted, s.inherit, s.generation)
			} else {
				fmt.Fprintf(&b, "%d:closed,g%d;", i, s.generation)
			}
		}
		b.WriteString(")")
	}
	oids := make([]ObjectID, 0, len(h.objects))
	for id := range h.objects {
		oids = append(oids, id)
	}
	sort.Slice(oids, func(i, j int) bool { return oids[i] < oids[j] })
	for _, id := range oids {
		fmt.Fprintf(&b, "O%d#%d;", id, h.objects[id].generation)
	}
	return b.String()
}

func (h *Host) clone() *Host {
	c := &Host{
		secret:  h.secret,
		types:   make(map[string]ObjectType, len(h.types)),
		procs:   make(map[uint32]*process, len(h.procs)),
		objects: make(map[ObjectID]*object, len(h.objects)),
	}
	for k, v := range h.types {
		c.types[k] = v
	}
	for k, v := range h.procs {
		cp := *v
		cp.table = append([]handleSlot(nil), v.table...)
		c.procs[k] = &cp
	}
	for k, v := range h.objects {
		co := *v
		c.objects[k] = &co
	}
	return c
}

// CloneSystem deep-copies the host for model checking.
func (h *Host) CloneSystem() System { return h.clone() }
