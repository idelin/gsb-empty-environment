package uffd

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type registration struct {
	start uintptr
	end   uintptr
	mode  RegisterMode
}

// Registration is an immutable snapshot of a registered interval.
type Registration struct {
	Start uintptr
	End   uintptr
	Mode  RegisterMode
}

var descriptorSeq uint64

func nextDescriptorID() uint64 { return atomic.AddUint64(&descriptorSeq, 1) }

// Descriptor is the gateway's model of one userfaultfd object. Sharing the
// object with SCM_RIGHTS shares this state; it is not cloned.
type Descriptor struct {
	mu sync.Mutex

	id       uint64
	creator  ProcessID
	flags    CreateFlags
	pageSize uintptr
	extra    Permissions
	holders  map[ProcessID]struct{}
	regs     []registration
	apiDone  bool
	features FeatureSet
}

// ID is a process-local identifier useful for audit logs.
func (d *Descriptor) ID() uint64 { return d.id }

// Creator returns the process that created the userfaultfd.
func (d *Descriptor) Creator() ProcessID { return d.creator }

// Flags returns the flags supplied at creation.
func (d *Descriptor) Flags() CreateFlags { return d.flags }

// UserModeOnly reports whether UFFD_USER_MODE_ONLY was supplied.
func (d *Descriptor) UserModeOnly() bool { return d.flags&UserModeOnly != 0 }

// APIReady reports whether a valid UFFDIO_API handshake has completed.
func (d *Descriptor) APIReady() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.apiDone
}

// NegotiatedFeatures returns the feature set from the last successful
// UFFDIO_API handshake.
func (d *Descriptor) NegotiatedFeatures() FeatureSet {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.features
}

// Registrations returns a snapshot of the registered intervals.
func (d *Descriptor) Registrations() []Registration {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]Registration, len(d.regs))
	for i, r := range d.regs {
		out[i] = Registration{Start: r.start, End: r.end, Mode: r.mode}
	}
	return out
}

// IsHolder reports whether the process currently holds this descriptor.
func (d *Descriptor) IsHolder(caller Caller) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.holders[caller.Process]
	return ok
}

// Pass models SCM_RIGHTS delivery. Both the sender and recipient refer to the
// same descriptor and registration state afterwards.
func (d *Descriptor) Pass(sender, recipient Caller) (*Descriptor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(sender); err != nil {
		return nil, err
	}
	d.holders[recipient.Process] = struct{}{}
	return d, nil
}

func (d *Descriptor) requireHolderLocked(caller Caller) error {
	if _, ok := d.holders[caller.Process]; !ok {
		return fmt.Errorf("%w: process %d", ErrNotHolder, caller.Process)
	}
	return nil
}

func (d *Descriptor) permits(caller Caller, want Permissions) bool {
	return d.extra.Has(want) || caller.Caps.permissions().Has(want)
}

func (d *Descriptor) normalize(start, length, end uintptr) (uintptr, uintptr, error) {
	return normalizeRange(start, length, end, d.pageSize)
}

// API authorizes UFFDIO_API. A zero Version is accepted as APIVersion.
func (d *Descriptor) API(caller Caller, req APIRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(caller); err != nil {
		return err
	}
	version := req.Version
	if version == 0 {
		version = APIVersion
	}
	if version != APIVersion {
		return fmt.Errorf("%w: version %#x", ErrInvalidAPI, version)
	}
	if req.Features&^knownFeatures != 0 {
		return fmt.Errorf("%w: features %#x", ErrInvalidAPI, uint64(req.Features&^knownFeatures))
	}
	d.apiDone = true
	d.features = req.Features
	return nil
}

// RegisterRange authorizes UFFDIO_REGISTER. Only the creator may register,
// including after the descriptor has been passed to another process.
func (d *Descriptor) RegisterRange(caller Caller, start, length, end uintptr, mode RegisterMode, mappings []Mapping) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(caller); err != nil {
		return err
	}
	if caller.Process != d.creator {
		return fmt.Errorf("%w: creator %d, caller %d", ErrNotCreator, d.creator, caller.Process)
	}
	if mode == 0 {
		return ErrModeRequired
	}
	if mode&^knownRegisterModes != 0 {
		return fmt.Errorf("%w: %#x", ErrUnknownMode, uint32(mode&^knownRegisterModes))
	}

	start, end, err := d.normalize(start, length, end)
	if err != nil {
		return err
	}

	if mode&ModeWriteProtect != 0 && !d.permits(caller, PermWriteProtect) {
		return fmt.Errorf("%w: WP registration", ErrPermissionDenied)
	}
	if mode&ModeMinor != 0 && !d.permits(caller, PermMinor) {
		return fmt.Errorf("%w: MINOR registration", ErrPermissionDenied)
	}

	if mappings == nil {
		mappings = caller.Mappings
	}
	if err := checkAnonymousPrivateCoverage(start, end, caller, mappings); err != nil {
		return err
	}

	regs, err := insertRegistration(d.regs, registration{start: start, end: end, mode: mode})
	if err != nil {
		return err
	}
	d.regs = regs
	return nil
}

// Register authorizes a decoded UFFDIO_REGISTER request.
func (d *Descriptor) Register(caller Caller, req RegisterRequest) error {
	return d.RegisterRange(caller, req.Start, req.Length, req.End, req.Mode, req.Mappings)
}

// UnregisterRange authorizes UFFDIO_UNREGISTER. Only the creator may alter
// registration state, so a descriptor receiver cannot revoke or reshape the
// original registrations.
func (d *Descriptor) UnregisterRange(caller Caller, start, length, end uintptr) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(caller); err != nil {
		return err
	}
	if caller.Process != d.creator {
		return fmt.Errorf("%w: creator %d, caller %d", ErrNotCreator, d.creator, caller.Process)
	}

	start, end, err := d.normalize(start, length, end)
	if err != nil {
		return err
	}
	if err := registrationCoverage(d.regs, start, end, 0); err != nil {
		return err
	}
	d.regs = removeRegistrations(d.regs, start, end)
	return nil
}

// Unregister authorizes a decoded UFFDIO_UNREGISTER request.
func (d *Descriptor) Unregister(caller Caller, req UnregisterRequest) error {
	return d.UnregisterRange(caller, req.Start, req.Length, req.End)
}

// CopyRange authorizes UFFDIO_COPY. Any holder may service an already
// registered MISSING range; the range's authorization is not re-evaluated
// against the receiver's mappings.
func (d *Descriptor) CopyRange(caller Caller, start, length, end uintptr) error {
	return d.authorizeSink(caller, start, length, end, ModeMissing, "copy")
}

// Copy authorizes a decoded UFFDIO_COPY request.
func (d *Descriptor) Copy(caller Caller, req CopyRequest) error {
	return d.CopyRange(caller, req.Start, req.Length, req.End)
}

// ZeroPageRange authorizes UFFDIO_ZEROPAGE, which has the same destination
// requirements as COPY.
func (d *Descriptor) ZeroPageRange(caller Caller, start, length, end uintptr) error {
	return d.authorizeSink(caller, start, length, end, ModeMissing, "zero page")
}

// ZeroPage authorizes a decoded UFFDIO_ZEROPAGE request.
func (d *Descriptor) ZeroPage(caller Caller, req CopyRequest) error {
	return d.ZeroPageRange(caller, req.Start, req.Length, req.End)
}

// ContinueRange authorizes UFFDIO_CONTINUE, which needs a MINOR registration.
func (d *Descriptor) ContinueRange(caller Caller, start, length, end uintptr) error {
	return d.authorizeSink(caller, start, length, end, ModeMinor, "continue")
}

// Continue authorizes a decoded UFFDIO_CONTINUE request.
func (d *Descriptor) Continue(caller Caller, req CopyRequest) error {
	return d.ContinueRange(caller, req.Start, req.Length, req.End)
}

func (d *Descriptor) authorizeSink(caller Caller, start, length, end uintptr, need RegisterMode, op string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(caller); err != nil {
		return err
	}
	start, end, err := d.normalize(start, length, end)
	if err != nil {
		return err
	}
	if err := registrationCoverage(d.regs, start, end, need); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// WakeRange authorizes UFFDIO_WAKE for any registered range.
func (d *Descriptor) WakeRange(caller Caller, start, length, end uintptr) error {
	return d.authorizeSink(caller, start, length, end, 0, "wake")
}

// Wake authorizes a decoded UFFDIO_WAKE request.
func (d *Descriptor) Wake(caller Caller, req WakeRequest) error {
	return d.WakeRange(caller, req.Start, req.Length, req.End)
}

// WriteProtectRange authorizes UFFDIO_WRITEPROTECT. The range must already be
// registered with WP mode, and this caller must hold an additional WP grant.
// The latter check is what stops a descriptor receiver from write-protecting
// the creator's mappings with only the creator's grant.
func (d *Descriptor) WriteProtectRange(caller Caller, start, length, end uintptr, protect bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.requireHolderLocked(caller); err != nil {
		return err
	}
	start, end, err := d.normalize(start, length, end)
	if err != nil {
		return err
	}
	if err := registrationCoverage(d.regs, start, end, ModeWriteProtect); err != nil {
		return fmt.Errorf("write protect: %w", err)
	}
	if !d.permits(caller, PermWriteProtect) {
		return fmt.Errorf("%w: UFFDIO_WRITEPROTECT", ErrPermissionDenied)
	}
	_ = protect // both protecting and unprotecting alter the original mapping
	return nil
}

// WriteProtect authorizes a decoded UFFDIO_WRITEPROTECT request.
func (d *Descriptor) WriteProtect(caller Caller, req WriteProtectRequest) error {
	protect := req.Protect || req.Mode&WriteProtectEnable != 0
	return d.WriteProtectRange(caller, req.Start, req.Length, req.End, protect)
}

// HandleIoctl dispatches an intercepted UFFDIO_* request.
func (d *Descriptor) HandleIoctl(caller Caller, req IoctlRequest) error {
	switch req.Kind {
	case UFFDIO_API:
		return d.API(caller, req.API)
	case UFFDIO_REGISTER:
		return d.RegisterRange(caller, req.Start, req.Length, req.End, req.Mode, req.Mappings)
	case UFFDIO_UNREGISTER:
		return d.UnregisterRange(caller, req.Start, req.Length, req.End)
	case UFFDIO_COPY:
		return d.CopyRange(caller, req.Start, req.Length, req.End)
	case UFFDIO_ZEROPAGE:
		return d.ZeroPageRange(caller, req.Start, req.Length, req.End)
	case UFFDIO_WAKE:
		return d.WakeRange(caller, req.Start, req.Length, req.End)
	case UFFDIO_WRITEPROTECT:
		protect := req.Protect || req.WriteProtectMode&WriteProtectEnable != 0
		return d.WriteProtectRange(caller, req.Start, req.Length, req.End, protect)
	case UFFDIO_CONTINUE:
		return d.ContinueRange(caller, req.Start, req.Length, req.End)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownIoctl, req.Kind)
	}
}

// AuthorizeIoctl is a convenience dispatcher for gateways that keep the
// descriptor externally.
func AuthorizeIoctl(d *Descriptor, caller Caller, req IoctlRequest) error {
	if d == nil {
		return fmt.Errorf("%w: nil descriptor", ErrNotHolder)
	}
	return d.HandleIoctl(caller, req)
}
