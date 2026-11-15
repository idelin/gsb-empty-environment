package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Lifecycle and validation errors. Use errors.Is to classify them; returned
// errors may wrap these values with details about the rejected operation.
var (
	ErrRingNotDisabled               = errors.New("netsec: ring is not disabled")
	ErrRestrictionsAlreadyRegistered = errors.New("netsec: restrictions already registered")
	ErrRestrictionsNotRegistered     = errors.New("netsec: restrictions not registered")
	ErrRingAlreadyEnabled            = errors.New("netsec: ring already enabled")
	ErrRingNotEnabled                = errors.New("netsec: ring is not enabled")
	ErrInvalidRestrictions           = errors.New("netsec: invalid restrictions")
	ErrRequiredFlagsNotAllowed       = errors.New("netsec: required flags contain a bit not allowed")
	ErrOpcodeNotAllowed              = errors.New("netsec: SQE opcode not allowed")
	ErrFlagsNotAllowed               = errors.New("netsec: SQE contains disallowed flags")
	ErrRequiredFlagsMissing          = errors.New("netsec: SQE is missing a required flag")
	ErrRegisterOpNotAllowed          = errors.New("netsec: registration opcode not allowed")
)

// Compatibility aliases with less specific names.
var (
	ErrRingDisabled           = ErrRingNotEnabled
	ErrRestrictionRegistered  = ErrRestrictionsAlreadyRegistered
	ErrMissingRequiredFlag    = ErrRequiredFlagsMissing
	ErrUnexpectedFlags        = ErrFlagsNotAllowed
	ErrRegistrationNotAllowed = ErrRegisterOpNotAllowed
)

// SQE is the subset of a submission queue entry needed by the restriction
// policy. The other submission fields are opaque to this package.
type SQE struct {
	Opcode Opcode
	Flags  SQEFlags
}

// Submission is an alias for SQE.
type Submission = SQE

// Restrictions is the immutable-after-registration policy for a ring.
//
// AllowedRegisterOps and AllowedSQEOps are allowlists, not denylists. An empty
// allowlist denies every operation in that category. An SQE flag is accepted
// only when it is contained in AllowedFlags and all RequiredFlags are present.
type Restrictions struct {
	AllowedRegisterOps []RegisterOpcode
	AllowedSQEOps      []Opcode
	AllowedFlags       SQEFlags
	RequiredFlags      SQEFlags
}

// NewRestrictions builds a restriction policy.
func NewRestrictions(
	allowedRegisterOps []RegisterOpcode,
	allowedSQEOps []Opcode,
	allowedFlags SQEFlags,
	requiredFlags SQEFlags,
) Restrictions {
	return Restrictions{
		AllowedRegisterOps: allowedRegisterOps,
		AllowedSQEOps:      allowedSQEOps,
		AllowedFlags:       allowedFlags,
		RequiredFlags:      requiredFlags,
	}
}

type ringState uint8

const (
	// stateUndisabled is the zero value. A zero-value Ring must not be mistaken
	// for a ring created by NewRing, which is always created disabled.
	stateUndisabled ringState = iota
	stateDisabled
	stateEnabled
)

type compiledRestrictions struct {
	registerOps   map[RegisterOpcode]struct{}
	sqeOps        map[Opcode]struct{}
	allowedFlags  SQEFlags
	requiredFlags SQEFlags
}

// Ring is an io_uring restriction state machine. A ring created by NewRing is
// disabled until exactly one restriction policy is registered and Enable is
// called.
type Ring struct {
	mu           sync.Mutex
	state        ringState
	restrictions *compiledRestrictions
}

// RestrictedRing is an alias for Ring.
type RestrictedRing = Ring

// NewRing creates a ring in the disabled state. The ring cannot process
// submissions or non-lifecycle registrations until RegisterRestrictions and
// Enable have both succeeded.
func NewRing() *Ring {
	return &Ring{state: stateDisabled}
}

// NewRestrictedRing is an alias for NewRing.
func NewRestrictedRing() *Ring {
	return NewRing()
}

// RegisterRestrictions installs the one and only restriction policy. It is
// valid only while the ring is disabled and before a policy has been installed.
func (r *Ring) RegisterRestrictions(restrictions Restrictions) error {
	if r == nil {
		return ErrRingNotDisabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.restrictions != nil || r.state == stateEnabled {
		return ErrRestrictionsAlreadyRegistered
	}
	if r.state != stateDisabled {
		return ErrRingNotDisabled
	}

	compiled, err := compileRestrictions(restrictions)
	if err != nil {
		return err
	}
	r.restrictions = compiled
	return nil
}

// Restrict is an alias for RegisterRestrictions.
func (r *Ring) Restrict(restrictions Restrictions) error {
	return r.RegisterRestrictions(restrictions)
}

// Enable activates the ring after a restriction policy has been installed.
func (r *Ring) Enable() error {
	if r == nil {
		return ErrRingNotEnabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case r.state == stateEnabled:
		return ErrRingAlreadyEnabled
	case r.state != stateDisabled:
		return ErrRingNotDisabled
	case r.restrictions == nil:
		return ErrRestrictionsNotRegistered
	default:
		r.state = stateEnabled
		return nil
	}
}

// EnableRings is an alias for Enable that matches IORING_REGISTER_ENABLE_RINGS.
func (r *Ring) EnableRings() error {
	return r.Enable()
}

// Submit validates an SQE against the active restriction policy. Submissions
// are rejected before the ring is enabled.
func (r *Ring) Submit(sqe SQE) error {
	if r == nil {
		return ErrRingNotEnabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != stateEnabled || r.restrictions == nil {
		return ErrRingNotEnabled
	}
	if _, ok := r.restrictions.sqeOps[sqe.Opcode]; !ok {
		return fmt.Errorf("%w: %d", ErrOpcodeNotAllowed, sqe.Opcode)
	}
	if sqe.Flags&^r.restrictions.allowedFlags != 0 {
		return fmt.Errorf("%w: flags %#x, allowed %#x", ErrFlagsNotAllowed, sqe.Flags, r.restrictions.allowedFlags)
	}
	if sqe.Flags&r.restrictions.requiredFlags != r.restrictions.requiredFlags {
		return fmt.Errorf(
			"%w: flags %#x, required %#x",
			ErrRequiredFlagsMissing,
			sqe.Flags,
			r.restrictions.requiredFlags,
		)
	}
	return nil
}

// SubmitSQE is an alias for Submit.
func (r *Ring) SubmitSQE(sqe SQE) error {
	return r.Submit(sqe)
}

// Register validates a post-enable registration operation against the
// AllowedRegisterOps allowlist. Lifecycle operations must use
// RegisterRestrictions and Enable instead.
func (r *Ring) Register(op RegisterOpcode) error {
	if r == nil {
		return ErrRingNotEnabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != stateEnabled || r.restrictions == nil {
		return ErrRingNotEnabled
	}
	if _, ok := r.restrictions.registerOps[op]; !ok {
		return fmt.Errorf("%w: %d", ErrRegisterOpNotAllowed, op)
	}
	return nil
}

// RegisterOp is an alias for Register.
func (r *Ring) RegisterOp(op RegisterOpcode) error {
	return r.Register(op)
}

// RegisterFileTable exercises the IORING_REGISTER_FILES path through the same
// registration allowlist. The fds are opaque to the policy manager.
func (r *Ring) RegisterFileTable(fds []int) error {
	return r.Register(IORING_REGISTER_FILES)
}

// RegisterFiles is an alias for RegisterFileTable.
func (r *Ring) RegisterFiles(fds []int) error {
	return r.RegisterFileTable(fds)
}

// Disabled reports whether the ring is still accepting its one-time
// restriction policy.
func (r *Ring) Disabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state == stateDisabled
}

// IsDisabled is an alias for Disabled.
func (r *Ring) IsDisabled() bool {
	return r.Disabled()
}

// Enabled reports whether submissions and restricted registrations are active.
func (r *Ring) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state == stateEnabled
}

// IsEnabled is an alias for Enabled.
func (r *Ring) IsEnabled() bool {
	return r.Enabled()
}

// RestrictionsRegistered reports whether the one-time policy has been set.
func (r *Ring) RestrictionsRegistered() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restrictions != nil
}

func compileRestrictions(restrictions Restrictions) (*compiledRestrictions, error) {
	if restrictions.RequiredFlags&^restrictions.AllowedFlags != 0 {
		return nil, fmt.Errorf(
			"%w: %w (required %#x, allowed %#x)",
			ErrInvalidRestrictions,
			ErrRequiredFlagsNotAllowed,
			restrictions.RequiredFlags,
			restrictions.AllowedFlags,
		)
	}

	compiled := &compiledRestrictions{
		registerOps:   make(map[RegisterOpcode]struct{}, len(restrictions.AllowedRegisterOps)),
		sqeOps:        make(map[Opcode]struct{}, len(restrictions.AllowedSQEOps)),
		allowedFlags:  restrictions.AllowedFlags,
		requiredFlags: restrictions.RequiredFlags,
	}
	for _, op := range restrictions.AllowedRegisterOps {
		compiled.registerOps[op] = struct{}{}
	}
	for _, op := range restrictions.AllowedSQEOps {
		compiled.sqeOps[op] = struct{}{}
	}
	return compiled, nil
}
