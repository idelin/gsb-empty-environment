package netsec

import "math"

// Device is the tenant-facing handle for one requester. Its requester ID and
// generation are host-owned fields; installs never accept a requester ID from
// the caller, and interrupts sent through this handle always use the
// host-assigned requester.
type Device struct {
	rm         *Remapper
	requester  RequesterID
	owner      DomainID
	generation DeviceGeneration
	busy       bool
}

// AttachDevice binds an unforgeable requester ID to its owning domain and
// returns the tenant-visible device handle. The request is made by the host;
// a tenant cannot attach a requester of its choosing.
func (r *Remapper) AttachDevice(requester RequesterID, owner DomainID) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.devices[requester]; exists {
		return nil, fail("attach-device", ErrDeviceExists, "requester %#04x", requester)
	}
	if _, ok := r.domains[owner]; !ok {
		return nil, fail("attach-device", ErrUnknownDomain,
			"requester %#04x owner %d", requester, owner)
	}
	d := &Device{
		rm:         r,
		requester:  requester,
		owner:      owner,
		generation: 1,
	}
	r.devices[requester] = d
	return d, nil
}

// CurrentDevice returns the live device handle for a requester. After a
// Reassign the previous handle is revoked and this returns the replacement.
func (r *Remapper) CurrentDevice(requester RequesterID) (*Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[requester]
	return d, ok
}

// RequesterID returns the host-assigned requester identity.
func (d *Device) RequesterID() RequesterID {
	if d == nil {
		return 0
	}
	return d.requester
}

// Generation returns the device's current generation. A revoked handle
// (for example after Reassign) returns 0.
func (d *Device) Generation() DeviceGeneration {
	if d == nil || d.rm == nil {
		return 0
	}
	d.rm.mu.Lock()
	defer d.rm.mu.Unlock()
	if d.rm.devices[d.requester] != d {
		return 0
	}
	return d.generation
}

// Owner returns the device's current owning domain. A revoked handle returns
// 0.
func (d *Device) Owner() DomainID {
	if d == nil || d.rm == nil {
		return 0
	}
	d.rm.mu.Lock()
	defer d.rm.mu.Unlock()
	if d.rm.devices[d.requester] != d {
		return 0
	}
	return d.owner
}

// Install installs or replaces one table entry. Replacements are serialized
// per device and go through the full close/barrier/publish sequence: the entry
// is first made non-deliverable, then Install waits for every in-flight
// receipt for that entry, then writes the new target and publishes it.
//
// If revalidation after the barrier fails (for example because the vCPU was
// hot-removed), the entry stays non-deliverable; the remapper never restores
// the old target.
func (d *Device) Install(caller DomainID, req InstallRequest) error {
	if d == nil || d.rm == nil {
		return fail("install", ErrUnknownDevice, "nil device")
	}
	r := d.rm
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.deviceLocked(d); err != nil {
		return err
	}
	if err := r.waitDeviceIdleLocked(nil, d); err != nil {
		return err
	}
	if err := r.validateInstallLocked(d, caller, req); err != nil {
		return err
	}

	d.busy = true
	defer r.releaseDeviceLocked(d)

	key := entryKey{
		requester:  d.requester,
		generation: d.generation,
		tableIndex: req.TableIndex,
	}
	m := r.entries[key]
	if m == nil {
		m = &mapping{}
		r.entries[key] = m
	}

	m.present = false
	if err := r.waitDrainedLocked(nil, m); err != nil {
		return err
	}
	// The barrier releases the lock. Revalidate everything before publishing
	// so a vCPU removal or range change during the wait cannot be bypassed.
	if err := r.validateInstallLocked(d, caller, req); err != nil {
		return err
	}
	if r.entries[key] != m {
		return fail("install", ErrNoMapping,
			"requester %#04x index %d entry was revoked during update",
			d.requester, req.TableIndex)
	}
	m.target = targetFrom(req)
	m.target.domain = caller
	m.present = true
	return nil
}

// Reset advances the device generation and revokes every entry of the old
// generation. The generation is advanced before the drain barrier, so any
// request carrying the old generation is rejected immediately, even while
// old in-flight receipts are still completing.
func (d *Device) Reset() error {
	if d == nil || d.rm == nil {
		return fail("reset", ErrUnknownDevice, "nil device")
	}
	r := d.rm
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.deviceLocked(d); err != nil {
		return err
	}
	if err := r.waitDeviceIdleLocked(nil, d); err != nil {
		return err
	}
	if err := r.deviceLocked(d); err != nil {
		return err
	}
	if d.generation == DeviceGeneration(math.MaxUint64) {
		return fail("reset", ErrGenerationExhausted, "requester %#04x", d.requester)
	}

	d.busy = true
	defer r.releaseDeviceLocked(d)

	old := d.generation
	d.generation++
	r.revokeGenerationLocked(d.requester, old)
	return nil
}

// Reassign moves a device to another domain ("re-tenancy"). It advances the
// generation, revokes all old-generation mappings through the drain barrier,
// and returns a fresh device handle for the new owner. The old handle is
// revoked: its Install/Reset/SendInterrupt calls fail with
// ErrStaleDeviceHandle, so a previous tenant cannot keep injecting into or
// resetting a device after it has been re-tenanted.
func (r *Remapper) Reassign(d *Device, newOwner DomainID) (*Device, error) {
	if d == nil || d.rm != r {
		return nil, fail("reassign", ErrUnknownDevice, "nil or foreign device handle")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.deviceLocked(d); err != nil {
		return nil, err
	}
	if _, ok := r.domains[newOwner]; !ok {
		return nil, fail("reassign", ErrUnknownDomain, "domain %d", newOwner)
	}
	if err := r.waitDeviceIdleLocked(nil, d); err != nil {
		return nil, err
	}
	if err := r.deviceLocked(d); err != nil {
		return nil, err
	}
	if d.generation == DeviceGeneration(math.MaxUint64) {
		return nil, fail("reassign", ErrGenerationExhausted, "requester %#04x", d.requester)
	}

	d.busy = true
	defer r.releaseDeviceLocked(d)

	old := d.generation
	d.generation++
	r.revokeGenerationLocked(d.requester, old)
	// Keep the old owner visible while old in-flight receipts drain, so a
	// callback observing Owner still sees the domain that admitted it. Only
	// publish the new owner once the barrier has completed.
	d.owner = newOwner
	replacement := &Device{
		rm:         r,
		requester:  d.requester,
		owner:      newOwner,
		generation: d.generation,
	}
	r.devices[d.requester] = replacement
	return replacement, nil
}
