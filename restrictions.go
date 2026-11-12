// Package netsec manages Linux io_uring restriction registration for a
// sandbox host that hands an io_uring ring over to untrusted code.
//
// The safe hand-off sequence enforced by this package is:
//
//  1. Create the ring with IORING_SETUP_R_DISABLED so that no SQE can be
//     submitted before restrictions are in place.
//  2. Apply the restriction policy exactly once with Ring.ApplyRestrictions.
//  3. Enable the ring with Ring.Enable.
//
// After enablement the kernel rejects every SQE whose opcode is outside the
// whitelist, whose flags exceed the allowed bits or miss a required bit, and
// rejects every io_uring_register call whose opcode is outside the register
// whitelist. Ring.CheckSQE and Ring.CheckRegisterOp mirror those kernel checks
// so that host-side submission code gets the same answers before entering the
// kernel.
package netsec

import (
	"errors"
	"sort"
	"sync"
)

// State describes the lifecycle stage of a restricted ring.
type State uint8

const (
	// StateCreated is a disabled ring whose restrictions have not been
	// registered yet.
	StateCreated State = iota
	// StateRestricted is a disabled ring whose restrictions have been
	// registered exactly once.
	StateRestricted
	// StateEnabled is an enabled ring guarded by the registered restrictions.
	StateEnabled
	// StateClosed is a ring whose file descriptor has been released.
	StateClosed
)

// RestrictionType identifies the kind of an io_uring_restriction entry. The
// numeric values match the kernel's IORING_RESTRICTION_* constants.
type RestrictionType uint8

const (
	RestrictionRegisterOp       RestrictionType = 0
	RestrictionSQEOp            RestrictionType = 1
	RestrictionSQEFlagsRequired RestrictionType = 2
	RestrictionSQEFlagsAllowed  RestrictionType = 3
)

// RegisterOpcode is an io_uring_register(2) opcode. The numeric values match
// the kernel's IORING_REGISTER_* constants.
type RegisterOpcode uint32

const (
	RegisterBuffers        RegisterOpcode = 0
	RegisterFiles          RegisterOpcode = 1
	RegisterEventFD        RegisterOpcode = 2
	RegisterProbe          RegisterOpcode = 3
	RegisterFilesUpdate    RegisterOpcode = 4
	RegisterEventFDAsync   RegisterOpcode = 5
	RegisterProbeUpdate    RegisterOpcode = 6
	RegisterBuffers2       RegisterOpcode = 7
	RegisterBuffersUpdate  RegisterOpcode = 8
	RegisterFilesSkip      RegisterOpcode = 9
	RegisterFilesUpdate2   RegisterOpcode = 10
	RegisterBuffersUpdate2 RegisterOpcode = 11
	RegisterRestrictions   RegisterOpcode = 17
	RegisterEnableRings    RegisterOpcode = 18
)

// SQEOpcode is an io_uring SQE opcode. The numeric values match the kernel's
// IORING_OP_* constants. Opcodes newer than this list can still be used through
// an explicit numeric conversion.
type SQEOpcode uint8

const (
	OpNop            SQEOpcode = 0
	OpReadv          SQEOpcode = 1
	OpWritev         SQEOpcode = 2
	OpFsync          SQEOpcode = 3
	OpReadFixed      SQEOpcode = 4
	OpWriteFixed     SQEOpcode = 5
	OpPollAdd        SQEOpcode = 6
	OpPollRemove     SQEOpcode = 7
	OpSyncFileRange  SQEOpcode = 8
	OpSendMsg        SQEOpcode = 9
	OpRecvMsg        SQEOpcode = 10
	OpTimeout        SQEOpcode = 11
	OpTimeoutRemove  SQEOpcode = 12
	OpAccept         SQEOpcode = 13
	OpAsyncCancel    SQEOpcode = 14
	OpLinkTimeout    SQEOpcode = 15
	OpConnect        SQEOpcode = 16
	OpFallocate      SQEOpcode = 17
	OpOpenAt         SQEOpcode = 18
	OpClose          SQEOpcode = 19
	OpFilesUpdate    SQEOpcode = 20
	OpStatx          SQEOpcode = 21
	OpRead           SQEOpcode = 22
	OpWrite          SQEOpcode = 23
	OpFadvise        SQEOpcode = 24
	OpMadvise        SQEOpcode = 25
	OpSend           SQEOpcode = 26
	OpRecv           SQEOpcode = 27
	OpOpenAt2        SQEOpcode = 28
	OpEpollCtl       SQEOpcode = 29
	OpSplice         SQEOpcode = 30
	OpProvideBuffers SQEOpcode = 31
	OpRemoveBuffers  SQEOpcode = 32
	OpTee            SQEOpcode = 33
	OpShutdown       SQEOpcode = 34
	OpRenameAt       SQEOpcode = 35
	OpUnlinkAt       SQEOpcode = 36
	OpMkdirAt        SQEOpcode = 37
	OpSymlinkAt      SQEOpcode = 38
	OpLinkAt         SQEOpcode = 39
)

// SQEFlag is an io_uring SQE flag bit. The numeric values match the kernel's
// IOSQE_* constants.
type SQEFlag uint64

const (
	SQEFlagFixedFile      SQEFlag = 1 << 0
	SQEFlagIODrain        SQEFlag = 1 << 1
	SQEFlagIOLink         SQEFlag = 1 << 2
	SQEFlagIOPrio         SQEFlag = 1 << 3
	SQEFlagPoll           SQEFlag = 1 << 4
	SQEFlagBufferSelect   SQEFlag = 1 << 5
	SQEFlagCQESkipSuccess SQEFlag = 1 << 6

	// SQEFlagKnownMask covers every flag bit defined by this package.
	SQEFlagKnownMask SQEFlag = (SQEFlagCQESkipSuccess << 1) - 1
)

// SetupFlag is an io_uring_setup flag. The numeric values match the kernel's
// IORING_SETUP_* constants.
type SetupFlag uint32

const (
	// SetupRDisabled creates a ring that stays disabled until
	// IORING_REGISTER_ENABLE_RINGS is issued.
	SetupRDisabled SetupFlag = 1 << 13
)

// Config is the restriction policy applied to a ring before it is enabled.
type Config struct {
	// AllowedRegisterOpcodes lists the io_uring_register opcodes that remain
	// usable after the ring is enabled. RegisterEnableRings is always added
	// implicitly because enabling the ring requires it.
	AllowedRegisterOpcodes []RegisterOpcode
	// AllowedSQEOpcodes lists the SQE opcodes accepted after enablement.
	AllowedSQEOpcodes []SQEOpcode
	// AllowedSQEFlags is the bit mask of SQE flags that may be set.
	AllowedSQEFlags SQEFlag
	// RequiredSQEFlags is the bit mask of SQE flags that must always be set.
	// Every required bit must also be present in AllowedSQEFlags.
	RequiredSQEFlags SQEFlag
}

// Errors returned by the lifecycle and policy checks. Kernel failures from the
// raw system-call backend are returned unwrapped.
var (
	ErrInvalidConfig              = errors.New("netsec: invalid restriction config")
	ErrRestrictionsAlreadyApplied = errors.New("netsec: restrictions can only be registered once")
	ErrRingNotDisabled            = errors.New("netsec: restrictions can only be registered on a disabled ring")
	ErrRestrictionsNotApplied     = errors.New("netsec: restrictions have not been applied yet")
	ErrRingNotEnabled             = errors.New("netsec: ring has not been enabled yet")
	ErrRingClosed                 = errors.New("netsec: ring is closed")
	ErrOpcodeNotAllowed           = errors.New("netsec: SQE opcode is not in the allowed set")
	ErrFlagNotAllowed             = errors.New("netsec: SQE flag is not in the allowed mask")
	ErrRequiredFlagMissing        = errors.New("netsec: required SQE flag is missing")
	ErrRegisterOpcodeNotAllowed   = errors.New("netsec: register opcode is not in the allowed set")
	ErrUnsupportedPlatform        = errors.New("netsec: io_uring is only available on linux")
)

// policy is the validated, deduplicated representation of Config.
type policy struct {
	registerOps   []uint16
	registerSet   map[uint16]struct{}
	sqeOps        []uint16
	sqeSet        map[uint16]struct{}
	allowedFlags  uint64
	requiredFlags uint64
}

func (c Config) build() (*policy, error) {
	if len(c.AllowedRegisterOpcodes) == 0 {
		return nil, ErrInvalidConfig
	}
	if c.RequiredSQEFlags&^c.AllowedSQEFlags != 0 {
		return nil, ErrInvalidConfig
	}

	registerSet := make(map[uint16]struct{}, len(c.AllowedRegisterOpcodes)+1)
	for _, op := range c.AllowedRegisterOpcodes {
		registerSet[uint16(op)] = struct{}{}
	}
	// Enabling the ring is itself a register operation and must stay allowed.
	registerSet[uint16(RegisterEnableRings)] = struct{}{}
	registerOps := make([]uint16, 0, len(registerSet))
	for op := range registerSet {
		registerOps = append(registerOps, op)
	}
	sort.Slice(registerOps, func(i, j int) bool { return registerOps[i] < registerOps[j] })

	sqeSet := make(map[uint16]struct{}, len(c.AllowedSQEOpcodes))
	for _, op := range c.AllowedSQEOpcodes {
		sqeSet[uint16(op)] = struct{}{}
	}
	sqeOps := make([]uint16, 0, len(sqeSet))
	for op := range sqeSet {
		sqeOps = append(sqeOps, op)
	}
	sort.Slice(sqeOps, func(i, j int) bool { return sqeOps[i] < sqeOps[j] })

	return &policy{
		registerOps:   registerOps,
		registerSet:   registerSet,
		sqeOps:        sqeOps,
		sqeSet:        sqeSet,
		allowedFlags:  uint64(c.AllowedSQEFlags),
		requiredFlags: uint64(c.RequiredSQEFlags),
	}, nil
}

// entries renders the policy as the io_uring_restriction array handed to
// IORING_REGISTER_RESTRICTIONS.
func (p *policy) entries() []restrictionEntry {
	out := make([]restrictionEntry, 0, len(p.registerOps)+len(p.sqeOps)+2)
	for _, op := range p.registerOps {
		out = append(out, restrictionEntry{opType: RestrictionRegisterOp, op: op})
	}
	for _, op := range p.sqeOps {
		out = append(out, restrictionEntry{opType: RestrictionSQEOp, op: op})
	}
	out = append(out, restrictionEntry{
		opType: RestrictionSQEFlagsAllowed,
		flags:  p.allowedFlags,
	})
	if p.requiredFlags != 0 {
		out = append(out, restrictionEntry{
			opType: RestrictionSQEFlagsRequired,
			flags:  p.requiredFlags,
		})
	}
	return out
}

func (p *policy) checkSQE(op uint16, flags uint64) error {
	if _, ok := p.sqeSet[op]; !ok {
		return ErrOpcodeNotAllowed
	}
	if flags&^p.allowedFlags != 0 {
		return ErrFlagNotAllowed
	}
	if flags&p.requiredFlags != p.requiredFlags {
		return ErrRequiredFlagMissing
	}
	return nil
}

func (p *policy) checkRegister(op uint16) error {
	if _, ok := p.registerSet[op]; !ok {
		return ErrRegisterOpcodeNotAllowed
	}
	return nil
}

// restrictionEntry is the platform-neutral shape of an io_uring_restriction.
// The Linux backend converts it into the kernel ABI structure.
type restrictionEntry struct {
	opType RestrictionType
	op     uint16
	flags  uint64
}

// Ring is a restricted io_uring ring. Its methods are safe for concurrent use.
type Ring struct {
	mu    sync.Mutex
	state State
	pol   *policy
	b     backend
}

// backend abstracts the platform-specific io_uring system calls.
type backend interface {
	registerRestrictions(entries []restrictionEntry) error
	enableRings() error
	registerFiles(fds []int) error
	close() error
}

func newRing(b backend) *Ring {
	return &Ring{state: StateCreated, b: b}
}

// State reports the current lifecycle state of the ring.
func (r *Ring) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// ApplyRestrictions registers the restriction policy exactly once. The ring
// must still be disabled; it stays disabled until Enable is called.
func (r *Ring) ApplyRestrictions(cfg Config) error {
	pol, err := cfg.build()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case StateClosed:
		return ErrRingClosed
	case StateEnabled:
		return ErrRingNotDisabled
	case StateRestricted:
		return ErrRestrictionsAlreadyApplied
	}
	if err := r.b.registerRestrictions(pol.entries()); err != nil {
		return err
	}
	r.pol = pol
	r.state = StateRestricted
	return nil
}

// Enable enables the ring after restrictions have been applied.
func (r *Ring) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case StateClosed:
		return ErrRingClosed
	case StateCreated:
		return ErrRestrictionsNotApplied
	case StateEnabled:
		return nil
	}
	if err := r.b.enableRings(); err != nil {
		return err
	}
	r.state = StateEnabled
	return nil
}

// CheckSQE mirrors the kernel's post-enable SQE validation: opcode must be
// whitelisted, flags must not exceed the allowed mask and must contain every
// required bit.
func (r *Ring) CheckSQE(opcode SQEOpcode, flags SQEFlag) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case StateClosed:
		return ErrRingClosed
	case StateCreated:
		return ErrRestrictionsNotApplied
	}
	return r.pol.checkSQE(uint16(opcode), uint64(flags))
}

// CheckRegisterOp mirrors the kernel's post-enable io_uring_register
// validation: only whitelisted register opcodes are accepted.
func (r *Ring) CheckRegisterOp(opcode RegisterOpcode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case StateClosed:
		return ErrRingClosed
	case StateCreated:
		return ErrRestrictionsNotApplied
	}
	return r.pol.checkRegister(uint16(opcode))
}

// RegisterFiles registers a file table through IORING_REGISTER_FILES. The call
// is permitted only while the ring is enabled and RegisterFiles is part of the
// register-opcode whitelist.
func (r *Ring) RegisterFiles(fds []int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case StateClosed:
		return ErrRingClosed
	case StateCreated:
		return ErrRestrictionsNotApplied
	case StateRestricted:
		return ErrRingNotEnabled
	}
	if err := r.pol.checkRegister(uint16(RegisterFiles)); err != nil {
		return err
	}
	return r.b.registerFiles(fds)
}

// Close releases the ring's file descriptor. Closing more than once returns
// ErrRingClosed.
func (r *Ring) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateClosed {
		return ErrRingClosed
	}
	r.state = StateClosed
	return r.b.close()
}
