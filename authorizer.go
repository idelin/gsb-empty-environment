package uffd

import "fmt"

// Authorizer holds the gateway policy that is not per-caller: the sysctl
// value, page size and any global additional mode grants.
type Authorizer struct {
	sysctl   Sysctl
	pageSize uintptr
	extra    Permissions
}

// Policy is an alias for Authorizer.
type Policy = Authorizer

// Option configures an Authorizer.
type Option func(*Authorizer)

// WithPageSize sets the page size used for range validation.
func WithPageSize(size uintptr) Option {
	return func(a *Authorizer) {
		if size != 0 {
			a.pageSize = size
		}
	}
}

// WithPermissions grants additional registration modes to every caller.
// Prefer granting them to individual callers via Capabilities where possible.
func WithPermissions(perms Permissions) Option {
	return func(a *Authorizer) { a.extra |= perms }
}

// WithExtraPermissions is an alias for WithPermissions.
func WithExtraPermissions(perms Permissions) Option { return WithPermissions(perms) }

// WithWriteProtect enables WP mode and UFFDIO_WRITEPROTECT globally.
func WithWriteProtect() Option { return WithPermissions(PermWriteProtect) }

// WithMinor enables MINOR mode globally.
func WithMinor() Option { return WithPermissions(PermMinor) }

// NewAuthorizer constructs a policy for the supplied sysctl value.
func NewAuthorizer(sysctl Sysctl, opts ...Option) *Authorizer {
	a := &Authorizer{sysctl: sysctl, pageSize: defaultPageSize}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

// New is an alias for NewAuthorizer.
func New(sysctl Sysctl, opts ...Option) *Authorizer { return NewAuthorizer(sysctl, opts...) }

// NewPolicy is an alias for NewAuthorizer.
func NewPolicy(sysctl Sysctl, opts ...Option) *Policy { return NewAuthorizer(sysctl, opts...) }

// CheckCreate applies the stateless creation policy.
func CheckCreate(sysctl Sysctl, caps Capabilities, flags CreateFlags) error {
	if flags&^knownCreateFlags != 0 {
		return fmt.Errorf("%w: %#x", ErrUnknownCreateFlag, uint32(flags&^knownCreateFlags))
	}

	if caps.CAPSysPtrace {
		return nil
	}
	if sysctl == UserfaultfdDisabled {
		return fmt.Errorf("%w: vm.unprivileged_userfaultfd=0", ErrUnprivilegedDisabled)
	}
	if flags&UserModeOnly == 0 {
		return fmt.Errorf("%w: caller lacks CAP_SYS_PTRACE and flags %#x", ErrUserModeOnlyRequired, uint32(flags))
	}
	return nil
}

// Create authorizes creation using the configured sysctl value.
func (a *Authorizer) Create(caller Caller, flags CreateFlags) (*Descriptor, error) {
	return a.CreateWithSysctl(a.sysctl, caller, flags)
}

// CreateWithSysctl authorizes creation using an explicit (current) sysctl
// value, which is useful when the gateway re-reads it per syscall.
func (a *Authorizer) CreateWithSysctl(sysctl Sysctl, caller Caller, flags CreateFlags) (*Descriptor, error) {
	if err := CheckCreate(sysctl, caller.Caps, flags); err != nil {
		return nil, err
	}

	pageSize := a.pageSize
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	d := &Descriptor{
		id:       nextDescriptorID(),
		creator:  caller.Process,
		flags:    flags,
		pageSize: pageSize,
		extra:    a.extra,
		holders:  map[ProcessID]struct{}{caller.Process: {}},
	}
	return d, nil
}

// AuthorizeCreate is a convenience for callers that do not keep an
// Authorizer.
func AuthorizeCreate(sysctl Sysctl, caller Caller, flags CreateFlags, opts ...Option) (*Descriptor, error) {
	return NewAuthorizer(sysctl, opts...).Create(caller, flags)
}
