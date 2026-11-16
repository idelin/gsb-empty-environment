package netsec

import (
	"errors"
	"sync"
)

// SQE describes the fields that are relevant to io_uring restrictions.
type SQE struct {
	Opcode Opcode
	Flags  SQEFlags
}

// Ring models the security-relevant lifecycle of an io_uring instance.
//
// A Ring is always created disabled. The only transitions are:
//
//	disabled -> restricted (one IORING_REGISTER_RESTRICTIONS call)
//	restricted -> enabled (one IORING_REGISTER_ENABLE_RINGS call)
//
// All methods are safe for concurrent use. In particular, an SQE cannot race
// with installation of the restriction set while observing an unrestricted
// ring, because the state and policy are checked under the same lock.
type Ring struct {
	mu sync.RWMutex

	disabled   bool
	restricted bool

	allowedRegisterOps map[uint8]struct{}
	allowedSQEOps      map[uint8]struct{}
	allowedFlags       SQEFlags
	requiredFlags      SQEFlags
}

// Manager is an alternative name for Ring.
type Manager = Ring

// NewRing creates an io_uring ring in the disabled state.
func NewRing() *Ring {
	return &Ring{disabled: true}
}

// New is a short form of NewRing.
func New() *Ring {
	return NewRing()
}

// NewDisabledRing is an explicit form of NewRing.
func NewDisabledRing() *Ring {
	return NewRing()
}

// NewManager creates a disabled restriction manager.
func NewManager() *Manager {
	return NewRing()
}

// CreateRing is the constructor expected by ring-creation code. The returned
// ring is disabled and cannot be enabled until restrictions are installed.
func CreateRing() (*Ring, error) {
	return NewRing(), nil
}

// Disabled reports whether the ring still rejects SQEs.
func (r *Ring) Disabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.disabled
}

// Enabled reports whether the ring has left the disabled state.
func (r *Ring) Enabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.disabled
}

// Restricted reports whether the one-time restriction set has been installed.
func (r *Ring) Restricted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.restricted
}

// RegisterRestrictions installs the one-time restriction policy. It is valid
// only while the ring is disabled and only before another restriction set has
// been installed.
func (r *Ring) RegisterRestrictions(restrictions Restrictions) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.disabled {
		return ErrRingNotDisabled
	}
	if r.restricted {
		return ErrRestrictionsAlreadySet
	}
	if err := restrictions.Validate(); err != nil {
		return err
	}

	r.allowedRegisterOps = opcodeSet(restrictions.AllowedRegisterOps)
	r.allowedSQEOps = opcodeSet(restrictions.AllowedSQEOps)
	r.allowedFlags = restrictions.AllowedFlags
	r.requiredFlags = restrictions.RequiredFlags
	r.restricted = true
	return nil
}

// Restrict and ApplyRestrictions are aliases for RegisterRestrictions.
func (r *Ring) Restrict(restrictions Restrictions) error {
	return r.RegisterRestrictions(restrictions)
}

func (r *Ring) ApplyRestrictions(restrictions Restrictions) error {
	return r.RegisterRestrictions(restrictions)
}

// Enable transitions a restricted ring to the enabled state. Enabling is a
// lifecycle operation rather than a normal registration, so the register
// allowlist does not need to contain IORING_REGISTER_ENABLE_RINGS.
func (r *Ring) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.disabled {
		return ErrRingAlreadyEnabled
	}
	if !r.restricted {
		return ErrRestrictionsRequired
	}
	r.disabled = false
	return nil
}

// EnableRings is an alias for Enable.
func (r *Ring) EnableRings() error {
	return r.Enable()
}

// Submit validates an SQE against the installed policy. It must only be called
// after Enable, and it checks the opcode and both flag constraints.
func (r *Ring) Submit(opcode Opcode, flags SQEFlags) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.disabled {
		return ErrRingDisabled
	}
	if !r.restricted {
		return ErrRestrictionsRequired
	}
	if _, ok := r.allowedSQEOps[uint8(opcode)]; !ok {
		return ErrSQEOpcodeNotAllowed
	}
	if flags&^r.allowedFlags != 0 {
		return ErrSQEFlagsNotAllowed
	}
	if flags&r.requiredFlags != r.requiredFlags {
		return ErrSQERequiredFlagsMissing
	}
	return nil
}

// SubmitSQE accepts either an SQE value or an opcode followed by an optional
// flags byte.
func (r *Ring) SubmitSQE(value any, flags ...uint8) error {
	switch sqe := value.(type) {
	case SQE:
		if len(flags) != 0 {
			return ErrInvalidSQE
		}
		return r.Submit(sqe.Opcode, sqe.Flags)
	case *SQE:
		if sqe == nil || len(flags) != 0 {
			return ErrInvalidSQE
		}
		return r.Submit(sqe.Opcode, sqe.Flags)
	case uint8:
		return r.submitOpcode(uint8(sqe), flags)
	case uint16:
		if sqe > 255 {
			return ErrInvalidSQE
		}
		return r.submitOpcode(uint8(sqe), flags)
	case uint32:
		if sqe > 255 {
			return ErrInvalidSQE
		}
		return r.submitOpcode(uint8(sqe), flags)
	case uint64:
		if sqe > 255 {
			return ErrInvalidSQE
		}
		return r.submitOpcode(uint8(sqe), flags)
	case int:
		if sqe < 0 || sqe > 255 {
			return ErrInvalidSQE
		}
		return r.submitOpcode(uint8(sqe), flags)
	default:
		return ErrInvalidSQE
	}
}

func (r *Ring) submitOpcode(opcode uint8, flags []uint8) error {
	if len(flags) > 1 {
		return ErrInvalidSQE
	}
	var sqeFlags uint8
	if len(flags) == 1 {
		sqeFlags = flags[0]
	}
	return r.Submit(opcode, sqeFlags)
}

// ValidateSQE is an alias for SubmitSQE.
func (r *Ring) ValidateSQE(value any, flags ...uint8) error {
	return r.SubmitSQE(value, flags...)
}

// Register handles either a lifecycle control registration or a normal
// post-enable registration. Normal registrations are accepted only after the
// ring is enabled and only when their opcode is in the register allowlist.
//
// The restriction and enable opcodes remain lifecycle-only; callers cannot
// bypass the one-time setup by putting those opcodes in the allowlist.
func (r *Ring) Register(opcode RegisterOpcode, args ...any) error {
	switch uint8(opcode) {
	case uint8(RegisterRestrictions):
		restrictions, err := restrictionsFromArgs(args)
		if err != nil {
			return err
		}
		return r.RegisterRestrictions(restrictions)
	case uint8(RegisterEnableRings):
		return r.Enable()
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.disabled {
		return ErrRingDisabled
	}
	if !r.restricted {
		return ErrRestrictionsRequired
	}
	if _, ok := r.allowedRegisterOps[uint8(opcode)]; !ok {
		return ErrRegisterOpNotAllowed
	}
	return nil
}

// RegisterOp is an alias for Register.
func (r *Ring) RegisterOp(opcode RegisterOpcode, args ...any) error {
	return r.Register(opcode, args...)
}

func restrictionsFromArgs(args []any) (Restrictions, error) {
	if len(args) != 1 {
		return Restrictions{}, ErrInvalidRestrictions
	}
	switch restrictions := args[0].(type) {
	case Restrictions:
		return restrictions, nil
	case *Restrictions:
		if restrictions == nil {
			return Restrictions{}, ErrInvalidRestrictions
		}
		return *restrictions, nil
	default:
		return Restrictions{}, ErrInvalidRestrictions
	}
}

// ErrInvalidSQE is returned when SubmitSQE is given an unsupported value or
// more than one flags byte.
var ErrInvalidSQE = errors.New("io_uring: invalid SQE")
