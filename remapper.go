package netsec

import (
	"context"
	"fmt"
	"sync"
)

// Remapper is the host-side interrupt remapping unit. Tenants receive Device
// handles and domain IDs; they must never receive the Remapper itself, because
// its ingress and administrative methods are part of the trusted host.
type Remapper struct {
	mu   sync.Mutex
	cond *sync.Cond

	domains map[DomainID]*domain
	devices map[RequesterID]*Device
	entries map[entryKey]*mapping
}

type domain struct {
	id      DomainID
	vectors VectorRange
	vcpus   map[VCPUID]VCPUGeneration
	// floor remembers the newest generation ever issued for a vCPU. It
	// prevents a removed vCPU's old generation from being re-registered.
	floor map[VCPUID]VCPUGeneration
}

type mapping struct {
	target   target
	present  bool
	inflight int
}

// NewRemapper creates an empty interrupt remapper.
func NewRemapper() *Remapper {
	r := &Remapper{
		domains: make(map[DomainID]*domain),
		devices: make(map[RequesterID]*Device),
		entries: make(map[entryKey]*mapping),
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// New is a short alias for NewRemapper.
func New() *Remapper { return NewRemapper() }

// RegisterDomain registers a domain's initial vCPU set and allowed vector
// range. Re-registering an existing domain is rejected so a stale tenant
// configuration cannot silently replace a live one.
func (r *Remapper) RegisterDomain(id DomainID, config DomainConfig) error {
	if err := config.Vectors.Validate(); err != nil {
		return fail("register-domain", ErrVectorInvalid, "domain %d: %v", id, err)
	}

	vcpus := make(map[VCPUID]VCPUGeneration, len(config.VCPUs))
	floor := make(map[VCPUID]VCPUGeneration, len(config.VCPUs))
	for _, vcpu := range config.VCPUs {
		if vcpu.Generation == 0 {
			return fail("register-domain", ErrZeroVCPUGeneration,
				"domain %d vCPU %d", id, vcpu.ID)
		}
		if _, exists := vcpus[vcpu.ID]; exists {
			return fail("register-domain", ErrVCPUGenerationRegress,
				"domain %d has duplicate vCPU %d", id, vcpu.ID)
		}
		vcpus[vcpu.ID] = vcpu.Generation
		floor[vcpu.ID] = vcpu.Generation
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.domains[id]; exists {
		return fail("register-domain", ErrDomainExists, "domain %d", id)
	}
	r.domains[id] = &domain{
		id:      id,
		vectors: config.Vectors,
		vcpus:   vcpus,
		floor:   floor,
	}
	return nil
}

// AddVCPU adds a new vCPU or advances an existing vCPU's generation. A
// generation that does not advance the high-water mark is rejected, including
// after a hot-removal. This makes stale entries and stale requests fail closed.
func (r *Remapper) AddVCPU(domainID DomainID, vcpu VCPUID, generation VCPUGeneration) error {
	if generation == 0 {
		return fail("add-vcpu", ErrZeroVCPUGeneration,
			"domain %d vCPU %d", domainID, vcpu)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	dom, ok := r.domains[domainID]
	if !ok {
		return fail("add-vcpu", ErrUnknownDomain, "domain %d", domainID)
	}
	if current, exists := dom.vcpus[vcpu]; exists {
		if generation == current {
			return nil
		}
		if generation < current {
			return fail("add-vcpu", ErrVCPUGenerationRegress,
				"domain %d vCPU %d generation %d < current %d",
				domainID, vcpu, generation, current)
		}
	}
	if generation <= dom.floor[vcpu] {
		return fail("add-vcpu", ErrVCPUGenerationRegress,
			"domain %d vCPU %d generation %d <= high-water %d",
			domainID, vcpu, generation, dom.floor[vcpu])
	}
	dom.vcpus[vcpu] = generation
	dom.floor[vcpu] = generation
	return nil
}

// RemoveVCPU hot-removes a vCPU. All present mappings targeting it are first
// made non-deliverable, then the call waits for every in-flight receipt for
// those mappings before deleting them. After RemoveVCPU returns, no callback
// admitted for that vCPU can still be running.
func (r *Remapper) RemoveVCPU(domainID DomainID, vcpu VCPUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dom, ok := r.domains[domainID]
	if !ok {
		return fail("remove-vcpu", ErrUnknownDomain, "domain %d", domainID)
	}
	if _, ok := dom.vcpus[vcpu]; !ok {
		return fail("remove-vcpu", ErrUnknownVCPU,
			"domain %d vCPU %d", domainID, vcpu)
	}
	// Remove the vCPU from the live set before draining. New installs that
	// race with the barrier then fail validation instead of publishing an
	// entry for a vCPU that is on its way out.
	delete(dom.vcpus, vcpu)

	type victim struct {
		key entryKey
		m   *mapping
	}
	var victims []victim
	for key, m := range r.entries {
		if m.target.domain != domainID || m.target.vcpu != vcpu {
			continue
		}
		m.present = false
		victims = append(victims, victim{key: key, m: m})
	}
	for _, v := range victims {
		// RemoveVCPU is a host control operation; no context is needed.
		_ = r.waitDrainedLocked(nil, v.m)
	}
	for _, v := range victims {
		delete(r.entries, v.key)
	}
	return nil
}

// SetVectorRange narrows or widens a domain's allowed vector range. Entries
// that fall outside the new range are revoked through the same drain barrier
// as RemoveVCPU.
func (r *Remapper) SetVectorRange(domainID DomainID, vectors VectorRange) error {
	if err := vectors.Validate(); err != nil {
		return fail("set-vector-range", ErrVectorInvalid, "domain %d: %v", domainID, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	dom, ok := r.domains[domainID]
	if !ok {
		return fail("set-vector-range", ErrUnknownDomain, "domain %d", domainID)
	}
	dom.vectors = vectors

	type victim struct {
		key entryKey
		m   *mapping
	}
	var victims []victim
	for key, m := range r.entries {
		if m.target.domain != domainID || vectors.Contains(m.target.vector) {
			continue
		}
		m.present = false
		victims = append(victims, victim{key: key, m: m})
	}
	for _, v := range victims {
		_ = r.waitDrainedLocked(nil, v.m)
	}
	for _, v := range victims {
		delete(r.entries, v.key)
	}
	return nil
}

// Lookup returns a snapshot of a remapping entry. ok is true when the entry
// object exists; a revoked entry exists with Present == false until its
// generation is reclaimed or it is explicitly deleted.
func (r *Remapper) Lookup(requester RequesterID, generation DeviceGeneration, index TableIndex) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := entryKey{requester: requester, generation: generation, tableIndex: index}
	m, ok := r.entries[key]
	if !ok {
		return Entry{}, false
	}
	return m.snapshot(key), true
}

func (m *mapping) snapshot(key entryKey) Entry {
	return Entry{
		RequesterID:      key.requester,
		DeviceGeneration: key.generation,
		TableIndex:       key.tableIndex,
		DomainID:         m.target.domain,
		VCPUID:           m.target.vcpu,
		VCPUGeneration:   m.target.vcpuGeneration,
		Vector:           m.target.vector,
		DeliveryMode:     m.target.mode,
		Present:          m.present,
	}
}

func targetFrom(req InstallRequest) target {
	return target{
		vcpu:           req.VCPU,
		vcpuGeneration: req.VCPUGeneration,
		vector:         req.Vector,
		mode:           req.DeliveryMode,
	}
}

func (r *Remapper) deviceLocked(d *Device) error {
	if d == nil || d.rm != r {
		return fail("device", ErrUnknownDevice, "nil or foreign device handle")
	}
	cur, ok := r.devices[d.requester]
	if !ok {
		return fail("device", ErrUnknownDevice, "requester %#04x", d.requester)
	}
	if cur != d {
		return fail("device", ErrStaleDeviceHandle,
			"requester %#04x handle generation %d", d.requester, d.generation)
	}
	return nil
}

func (r *Remapper) domainLocked(id DomainID) (*domain, error) {
	dom, ok := r.domains[id]
	if !ok {
		return nil, fail("domain", ErrUnknownDomain, "domain %d", id)
	}
	return dom, nil
}

// validateInstallLocked checks all authorization and routing constraints for
// one request while r.mu is held.
func (r *Remapper) validateInstallLocked(d *Device, caller DomainID, req InstallRequest) error {
	if err := r.deviceLocked(d); err != nil {
		return err
	}
	if caller != d.owner {
		return fail("install", ErrForeignDomain,
			"domain %d attempted to program requester %#04x owned by domain %d",
			caller, d.requester, d.owner)
	}
	if req.DeliveryMode != DeliveryFixed {
		return fail("install", ErrDeliveryModeNotFixed,
			"index %d requested %s", req.TableIndex, req.DeliveryMode)
	}
	if !req.Vector.Valid() {
		return fail("install", ErrVectorInvalid, "index %d vector %#x", req.TableIndex, req.Vector)
	}
	dom, err := r.domainLocked(caller)
	if err != nil {
		return err
	}
	if !dom.vectors.Contains(req.Vector) {
		return fail("install", ErrVectorOutOfRange,
			"domain %d index %d vector %#x outside [%#x,%#x]",
			caller, req.TableIndex, req.Vector, dom.vectors.Min, dom.vectors.Max)
	}
	current, ok := dom.vcpus[req.VCPU]
	if !ok {
		return fail("install", ErrUnknownVCPU,
			"domain %d index %d vCPU %d", caller, req.TableIndex, req.VCPU)
	}
	if current != req.VCPUGeneration {
		return fail("install", ErrStaleVCPUGeneration,
			"domain %d vCPU %d requested generation %d, current %d",
			caller, req.VCPU, req.VCPUGeneration, current)
	}
	return nil
}

// waitDrainedLocked waits until all callbacks admitted for m have returned.
// The caller must hold r.mu. cond.Wait releases and reacquires it, so receipts
// can make progress while the barrier is active.
func (r *Remapper) waitDrainedLocked(ctx context.Context, m *mapping) error {
	for m.inflight > 0 {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if err := r.waitLocked(ctx); err != nil {
			return err
		}
	}
	return nil
}

// waitDeviceIdleLocked serializes device control operations. The caller must
// hold r.mu.
func (r *Remapper) waitDeviceIdleLocked(ctx context.Context, d *Device) error {
	for d.busy {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if err := r.waitLocked(ctx); err != nil {
			return err
		}
	}
	return nil
}

// waitLocked performs one condition wait, waking early when ctx is canceled.
// The caller must hold r.mu.
func (r *Remapper) waitLocked(ctx context.Context) error {
	if ctx == nil || ctx.Done() == nil {
		r.cond.Wait()
		return nil
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-done:
		}
	}()
	r.cond.Wait()
	close(done)
	return ctx.Err()
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (r *Remapper) releaseDeviceLocked(d *Device) {
	d.busy = false
	r.cond.Broadcast()
}

func (r *Remapper) entriesForLocked(requester RequesterID, generation DeviceGeneration) map[entryKey]*mapping {
	out := make(map[entryKey]*mapping)
	for key, m := range r.entries {
		if key.requester == requester && key.generation == generation {
			out[key] = m
		}
	}
	return out
}

func (r *Remapper) revokeGenerationLocked(requester RequesterID, generation DeviceGeneration) {
	entries := r.entriesForLocked(requester, generation)
	for _, m := range entries {
		m.present = false
	}
	for _, m := range entries {
		_ = r.waitDrainedLocked(nil, m)
	}
	for key := range entries {
		delete(r.entries, key)
	}
}

func (r *Remapper) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("netsec.Remapper{domains:%d devices:%d entries:%d}",
		len(r.domains), len(r.devices), len(r.entries))
}
