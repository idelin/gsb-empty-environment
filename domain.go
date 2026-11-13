package netsec

import "context"

// Domain is a host-issued capability for one registered domain. A tenant
// receives only its own *Domain and *Device handles. The fields are
// unexported, so another package cannot forge a capability for a different
// domain; InstallAs and the other capability-taking methods verify both the
// issuing remapper and the domain ID.
type Domain struct {
	rm *Remapper
	id DomainID
}

// ID returns the domain identifier carried by the capability.
func (d *Domain) ID() DomainID {
	if d == nil {
		return 0
	}
	return d.id
}

// NewDomain registers a domain and returns the host-issued capability for it.
// RegisterDomain is the same operation without a capability handle; it is
// useful for host configuration that already authenticates callers.
func (r *Remapper) NewDomain(id DomainID, config DomainConfig) (*Domain, error) {
	if err := r.RegisterDomain(id, config); err != nil {
		return nil, err
	}
	return &Domain{rm: r, id: id}, nil
}

// AttachDeviceTo is AttachDevice with an explicit owner capability.
func (r *Remapper) AttachDeviceTo(requester RequesterID, owner *Domain) (*Device, error) {
	if owner == nil || owner.rm != r {
		return nil, fail("attach-device", ErrForeignDomain,
			"requester %#04x: missing or foreign domain capability", requester)
	}
	return r.AttachDevice(requester, owner.id)
}

// InstallAs installs a table entry using the caller's domain capability. It
// is the tenant-facing form of Install; a capability for another domain is
// rejected even if the caller knows that domain's numeric ID.
func (d *Device) InstallAs(owner *Domain, req InstallRequest) error {
	if d == nil || d.rm == nil {
		return fail("install", ErrUnknownDevice, "nil device")
	}
	if owner == nil || owner.rm != d.rm || owner.id != d.Owner() {
		return fail("install", ErrForeignDomain,
			"device requester %#04x owner %d", d.RequesterID(), d.Owner())
	}
	return d.Install(owner.id, req)
}

// InstallBatchAs is the capability-taking form of InstallBatch.
func (d *Device) InstallBatchAs(owner *Domain, reqs []InstallRequest) error {
	if d == nil || d.rm == nil {
		return fail("install-batch", ErrUnknownDevice, "nil device")
	}
	if owner == nil || owner.rm != d.rm || owner.id != d.Owner() {
		return fail("install-batch", ErrForeignDomain,
			"device requester %#04x owner %d", d.RequesterID(), d.Owner())
	}
	return d.InstallBatch(owner.id, reqs)
}

// InstallBatchContextAs is the capability-taking form of
// InstallBatchContext.
func (d *Device) InstallBatchContextAs(ctx context.Context, owner *Domain, reqs []InstallRequest) error {
	if d == nil || d.rm == nil {
		return fail("install-batch", ErrUnknownDevice, "nil device")
	}
	if owner == nil || owner.rm != d.rm || owner.id != d.Owner() {
		return fail("install-batch", ErrForeignDomain,
			"device requester %#04x owner %d", d.RequesterID(), d.Owner())
	}
	return d.InstallBatchContext(ctx, owner.id, reqs)
}

// ReassignTo is Reassign with an explicit new-owner capability.
func (r *Remapper) ReassignTo(d *Device, newOwner *Domain) (*Device, error) {
	if newOwner == nil || newOwner.rm != r {
		return nil, fail("reassign", ErrForeignDomain, "missing or foreign domain capability")
	}
	return r.Reassign(d, newOwner.id)
}
