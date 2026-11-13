package netsec

// SendInterrupt injects an interrupt as the device would after a tenant writes
// its MSI-X table. The requester ID and current device generation are stamped
// by the host-side handle; tableIndex and vector are the only tenant-chosen
// fields.
func (d *Device) SendInterrupt(tableIndex TableIndex, vector Vector, deliver DeliveryFunc) error {
	if d == nil || d.rm == nil {
		return fail("interrupt", ErrUnknownDevice, "nil device")
	}
	r := d.rm

	r.mu.Lock()
	if err := r.deviceLocked(d); err != nil {
		r.mu.Unlock()
		return err
	}
	msg := Interrupt{
		RequesterID:      d.requester,
		DeviceGeneration: d.generation,
		TableIndex:       tableIndex,
		Vector:           vector,
	}
	r.mu.Unlock()
	return r.Deliver(msg, deliver)
}

// Deliver is the trusted interrupt ingress. The root complex fills in
// RequesterID and DeviceGeneration from the physical transaction; TableIndex
// and Vector come from the message written by the device/tenant. Host code
// must not expose this method to tenants.
//
// Lookup order is security-critical:
//  1. match (requester, device generation, table index);
//  2. require the message vector to equal the registered vector;
//  3. revalidate the owning domain, vCPU generation and vector range;
//  4. only then admit the callback and count it as in-flight.
func (r *Remapper) Deliver(msg Interrupt, deliver DeliveryFunc) error {
	if deliver == nil {
		return fail("deliver", ErrNilCallback, "")
	}
	if !msg.Vector.Valid() {
		return fail("deliver", ErrVectorInvalid,
			"requester %#04x index %d vector %#x",
			msg.RequesterID, msg.TableIndex, msg.Vector)
	}

	r.mu.Lock()

	dev, ok := r.devices[msg.RequesterID]
	if !ok {
		r.mu.Unlock()
		return fail("deliver", ErrUnknownDevice, "requester %#04x", msg.RequesterID)
	}
	// This check is what makes old-generation requests fail even if a stale
	// entry object still exists for a short transition window.
	if dev.generation != msg.DeviceGeneration {
		r.mu.Unlock()
		return fail("deliver", ErrStaleDeviceGeneration,
			"requester %#04x message generation %d, current %d",
			msg.RequesterID, msg.DeviceGeneration, dev.generation)
	}

	key := entryKey{
		requester:  msg.RequesterID,
		generation: msg.DeviceGeneration,
		tableIndex: msg.TableIndex,
	}
	m := r.entries[key]
	if m == nil || !m.present {
		r.mu.Unlock()
		return fail("deliver", ErrNoMapping,
			"requester %#04x generation %d index %d",
			msg.RequesterID, msg.DeviceGeneration, msg.TableIndex)
	}
	// Take one immutable snapshot of the target while the lock is held. All
	// validation and error formatting below use this snapshot, never the
	// mapping fields after the lock is released.
	t := m.target

	// Match the entry first, then compare the message vector with the vector
	// registered for this exact (requester, generation, index).
	if msg.Vector != t.vector {
		r.mu.Unlock()
		return fail("deliver", ErrVectorMismatch,
			"requester %#04x index %d message vector %#x, registered %#x",
			msg.RequesterID, msg.TableIndex, msg.Vector, t.vector)
	}
	if t.mode != DeliveryFixed {
		r.mu.Unlock()
		return fail("deliver", ErrDeliveryModeNotFixed,
			"requester %#04x index %d mode %s",
			msg.RequesterID, msg.TableIndex, t.mode)
	}
	if t.domain != dev.owner {
		r.mu.Unlock()
		return fail("deliver", ErrForeignDomain,
			"entry domain %d is not owner %d of requester %#04x",
			t.domain, dev.owner, msg.RequesterID)
	}
	dom, ok := r.domains[t.domain]
	if !ok {
		r.mu.Unlock()
		return fail("deliver", ErrUnknownDomain,
			"entry domain %d for requester %#04x", t.domain, msg.RequesterID)
	}
	currentGen, ok := dom.vcpus[t.vcpu]
	if !ok {
		r.mu.Unlock()
		return fail("deliver", ErrUnknownVCPU,
			"domain %d vCPU %d", t.domain, t.vcpu)
	}
	if currentGen != t.vcpuGeneration {
		r.mu.Unlock()
		return fail("deliver", ErrStaleVCPUGeneration,
			"domain %d vCPU %d entry generation %d, current %d",
			t.domain, t.vcpu, t.vcpuGeneration, currentGen)
	}
	if !dom.vectors.Contains(t.vector) {
		r.mu.Unlock()
		return fail("deliver", ErrVectorOutOfRange,
			"domain %d vector %#x outside [%#x,%#x]",
			t.domain, t.vector, dom.vectors.Min, dom.vectors.Max)
	}

	// Admission and the final domain/vCPU revalidation happen in the same
	// critical section as the unlock below. Any removal or reassignment that
	// happens after this point observes the callback as already in-flight and
	// its drain barrier waits for the receipt.
	m.inflight++
	delivery := Delivery{
		RequesterID:      msg.RequesterID,
		DeviceGeneration: msg.DeviceGeneration,
		TableIndex:       msg.TableIndex,
		DomainID:         t.domain,
		VCPUID:           t.vcpu,
		VCPUGeneration:   t.vcpuGeneration,
		Vector:           t.vector,
		DeliveryMode:     t.mode,
	}
	r.mu.Unlock()

	var err error
	func() {
		defer func() {
			r.mu.Lock()
			m.inflight--
			if m.inflight == 0 {
				r.cond.Broadcast()
			}
			r.mu.Unlock()
		}()
		err = deliver(delivery)
	}()
	return err
}
