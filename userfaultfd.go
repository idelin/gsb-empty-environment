// Package netsec provides the authorization layer a sandbox gateway calls
// when intercepting userfaultfd(2) and its UFFDIO_API / UFFDIO_REGISTER
// ioctls.
//
// userfaultfd lets an attacker freeze a thread at the exact moment the
// kernel accesses user memory, stretching race-condition windows. The
// kernel exposes vm.unprivileged_userfaultfd, and UFFD_USER_MODE_ONLY
// restricts a descriptor to faults triggered from user mode. The sandbox
// policy implemented here is strictly stronger than the kernel switch:
//
//   - Callers without CAP_SYS_PTRACE must create the descriptor with
//     UFFD_USER_MODE_ONLY, no matter how vm.unprivileged_userfaultfd is set.
//   - UFFDIO_REGISTER ranges must lie inside the caller's own anonymous
//     private mappings.
//   - Modes other than MISSING (WP, MINOR) need an extra permission grant.
//   - Once a descriptor is handed to another process via SCM_RIGHTS, the
//     verdicts for already-registered ranges do not change: they are bound
//     to the descriptor's registration record, not to the current holder.
package netsec

import (
	"errors"
	"sort"
	"sync"
)

// CapSysPtrace mirrors CAP_SYS_PTRACE from linux/capability.h.
const CapSysPtrace uint64 = 1 << 19

// PageSize is the alignment unit for registered ranges.
const PageSize = 4096

var (
	ErrUnknownCreateFlags   = errors.New("netsec: unknown userfaultfd create flags")
	ErrUserModeOnlyRequired = errors.New("netsec: UFFD_USER_MODE_ONLY required without CAP_SYS_PTRACE")
	ErrBadDescriptor        = errors.New("netsec: descriptor unknown or owned by another process")
	ErrAPINotNegotiated     = errors.New("netsec: UFFDIO_API handshake required before UFFDIO_REGISTER")
	ErrUnknownFeatures      = errors.New("netsec: unknown UFFDIO_API feature bits")
	ErrFeatureNotGranted    = errors.New("netsec: UFFDIO_API feature requires extra permission")
	ErrInvalidRange         = errors.New("netsec: range empty or not page aligned")
	ErrRangeNotAnonPrivate  = errors.New("netsec: range escapes caller's anonymous private mappings")
	ErrUnknownMode          = errors.New("netsec: unknown UFFDIO_REGISTER mode")
	ErrModeNotGranted       = errors.New("netsec: register mode requires extra permission")
	ErrModeNotNegotiated    = errors.New("netsec: register mode not negotiated via UFFDIO_API")
	ErrRangeNotRegistered   = errors.New("netsec: range not registered on this descriptor")
)

// CreateFlags are the flag bits accepted from userfaultfd(2). Values match
// the kernel ABI.
type CreateFlags uint32

const (
	FlagUserModeOnly CreateFlags = 0x1       // UFFD_USER_MODE_ONLY
	FlagCloexec      CreateFlags = 0o2000000 // O_CLOEXEC
	FlagNonblock     CreateFlags = 0o0004000 // O_NONBLOCK

	KnownCreateFlags = FlagUserModeOnly | FlagCloexec | FlagNonblock
)

// Features are the UFFDIO_API feature bits the gateway understands.
type Features uint64

const (
	FeaturePagefaultFlagWP Features = 1 << 0  // UFFD_FEATURE_PAGEFAULT_FLAG_WP
	FeatureMinorHugetlbfs  Features = 1 << 9  // UFFD_FEATURE_MINOR_HUGETLBFS
	FeatureMinorShmem      Features = 1 << 10 // UFFD_FEATURE_MINOR_SHMEM

	KnownFeatures = FeaturePagefaultFlagWP | FeatureMinorHugetlbfs | FeatureMinorShmem
)

// RegisterMode carries UFFDIO_REGISTER mode bits.
type RegisterMode uint32

const (
	ModeMissing RegisterMode = 1 << 0 // UFFDIO_REGISTER_MODE_MISSING
	ModeWP      RegisterMode = 1 << 1 // UFFDIO_REGISTER_MODE_WP
	ModeMinor   RegisterMode = 1 << 2 // UFFDIO_REGISTER_MODE_MINOR

	KnownModes = ModeMissing | ModeWP | ModeMinor
)

// Sysctl mirrors the kernel's vm.unprivileged_userfaultfd switch. It is an
// input to the authorizer for completeness, but the sandbox policy never
// lets it relax the UFFD_USER_MODE_ONLY requirement.
type Sysctl struct {
	UnprivilegedUserfaultfd bool
}

// Caller describes the process performing the intercepted call. Mappings is
// a snapshot of its address space (e.g. parsed from /proc/<pid>/maps).
type Caller struct {
	PID      int
	Caps     uint64
	Mappings []Mapping
}

// Has reports whether the caller holds every bit of cap.
func (c Caller) Has(cap uint64) bool { return c.Caps&cap == cap }

// Mapping is one virtual memory area of a caller.
type Mapping struct {
	Start, End uint64
	Anonymous  bool
	Private    bool
	Path       string // backing file, empty for anonymous areas
}

// GrantFunc decides whether a caller may use a non-MISSING register mode
// (WP or MINOR) or negotiate the matching UFFDIO_API feature.
type GrantFunc func(c Caller, mode RegisterMode) bool

type registration struct {
	start, end uint64
	mode       RegisterMode
}

// descriptor is the underlying userfaultfd object. It survives SCM_RIGHTS
// transfers: every received descriptor number points at the same record.
type descriptor struct {
	creator  Caller
	flags    CreateFlags
	apiDone  bool
	features Features
	regs     []registration
}

type fdEntry struct {
	holder Caller
	desc   *descriptor
}

// Authorizer gates userfaultfd creation and the UFFDIO_API / UFFDIO_REGISTER
// ioctls according to the sandbox policy. It is safe for concurrent use.
type Authorizer struct {
	sysctl Sysctl
	grant  GrantFunc

	mu     sync.Mutex
	nextFD int
	fds    map[int]*fdEntry
}

// NewAuthorizer builds an Authorizer. grant may be nil, in which case no
// caller ever receives WP or MINOR permission.
func NewAuthorizer(sysctl Sysctl, grant GrantFunc) *Authorizer {
	if grant == nil {
		grant = func(Caller, RegisterMode) bool { return false }
	}
	return &Authorizer{sysctl: sysctl, grant: grant, fds: make(map[int]*fdEntry)}
}

func (a *Authorizer) permit(c Caller, mode RegisterMode) bool {
	return mode == ModeMissing || a.grant(c, mode)
}

// lookup returns the descriptor for fd, provided c is its current holder.
// Callers must hold a.mu.
func (a *Authorizer) lookup(c Caller, fd int) (*descriptor, error) {
	e, ok := a.fds[fd]
	if !ok || e.holder.PID != c.PID {
		return nil, ErrBadDescriptor
	}
	return e.desc, nil
}

// Create intercepts userfaultfd(2). Regardless of the
// vm.unprivileged_userfaultfd sysctl, a caller without CAP_SYS_PTRACE must
// pass UFFD_USER_MODE_ONLY so the descriptor can only service faults
// triggered from user mode.
func (a *Authorizer) Create(c Caller, flags CreateFlags) (int, error) {
	if flags&^KnownCreateFlags != 0 {
		return -1, ErrUnknownCreateFlags
	}
	if !c.Has(CapSysPtrace) && flags&FlagUserModeOnly == 0 {
		return -1, ErrUserModeOnlyRequired
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextFD++
	a.fds[a.nextFD] = &fdEntry{
		holder: c,
		desc:   &descriptor{creator: c, flags: flags},
	}
	return a.nextFD, nil
}

// API intercepts the UFFDIO_API handshake. Requesting a feature tied to a
// non-MISSING mode requires the matching extra permission.
func (a *Authorizer) API(c Caller, fd int, feats Features) error {
	if feats&^KnownFeatures != 0 {
		return ErrUnknownFeatures
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d, err := a.lookup(c, fd)
	if err != nil {
		return err
	}
	if feats&FeaturePagefaultFlagWP != 0 && !a.permit(c, ModeWP) {
		return ErrFeatureNotGranted
	}
	if feats&(FeatureMinorHugetlbfs|FeatureMinorShmem) != 0 && !a.permit(c, ModeMinor) {
		return ErrFeatureNotGranted
	}
	d.apiDone = true
	d.features = feats
	return nil
}

// Register intercepts UFFDIO_REGISTER. The range must be page aligned and
// fully covered by the caller's own anonymous private mappings; WP and
// MINOR modes must have been negotiated via UFFDIO_API and require the
// extra permission grant.
func (a *Authorizer) Register(c Caller, fd int, start, end uint64, mode RegisterMode) error {
	if mode == 0 || mode&^KnownModes != 0 {
		return ErrUnknownMode
	}
	if start >= end || start%PageSize != 0 || end%PageSize != 0 {
		return ErrInvalidRange
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d, err := a.lookup(c, fd)
	if err != nil {
		return err
	}
	if !d.apiDone {
		return ErrAPINotNegotiated
	}
	if !coveredByAnonPrivate(c.Mappings, start, end) {
		return ErrRangeNotAnonPrivate
	}
	if mode&ModeWP != 0 {
		if d.features&FeaturePagefaultFlagWP == 0 {
			return ErrModeNotNegotiated
		}
		if !a.permit(c, ModeWP) {
			return ErrModeNotGranted
		}
	}
	if mode&ModeMinor != 0 {
		if d.features&(FeatureMinorHugetlbfs|FeatureMinorShmem) == 0 {
			return ErrModeNotNegotiated
		}
		if !a.permit(c, ModeMinor) {
			return ErrModeNotGranted
		}
	}
	d.regs = append(d.regs, registration{start: start, end: end, mode: mode})
	return nil
}

// PassFD models an SCM_RIGHTS transfer: the recipient gets a fresh
// descriptor number referencing the same underlying userfaultfd object,
// including its registration history.
func (a *Authorizer) PassFD(sender, recipient Caller, fd int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.fds[fd]
	if !ok || e.holder.PID != sender.PID {
		return -1, ErrBadDescriptor
	}
	a.nextFD++
	a.fds[a.nextFD] = &fdEntry{holder: recipient, desc: e.desc}
	return a.nextFD, nil
}

// AuthorizeRegistered checks whether an already-registered range may be
// serviced through fd. The verdict is bound to the descriptor's
// registration record, so it is identical for every holder — including
// processes that received the descriptor via SCM_RIGHTS and themselves
// lack the capabilities or grants the registration originally required.
func (a *Authorizer) AuthorizeRegistered(c Caller, fd int, start, end uint64, mode RegisterMode) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	d, err := a.lookup(c, fd)
	if err != nil {
		return err
	}
	for _, r := range d.regs {
		if r.start <= start && end <= r.end && r.mode&mode == mode {
			return nil
		}
	}
	return ErrRangeNotRegistered
}

// coveredByAnonPrivate reports whether [start, end) is fully covered by
// anonymous private mappings, with no gaps and no file-backed or shared
// areas in between.
func coveredByAnonPrivate(ms []Mapping, start, end uint64) bool {
	sorted := make([]Mapping, len(ms))
	copy(sorted, ms)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	cursor := start
	for _, m := range sorted {
		if m.End <= cursor {
			continue
		}
		if m.Start > cursor {
			return false // gap in coverage
		}
		if !m.Anonymous || !m.Private {
			return false
		}
		cursor = m.End
		if cursor >= end {
			return true
		}
	}
	return false
}
