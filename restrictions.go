package netsec

import "errors"

// Restricted io_uring operations use the ABI-sized types below. SQE opcodes,
// register opcodes, and SQE flags are all one byte wide.
type (
	Opcode         = uint8
	RegisterOpcode = uint8
	SQEFlags       = uint8
)

// SQE opcodes from the io_uring ABI.
const (
	OpNop            Opcode = 0
	OpReadv          Opcode = 1
	OpWritev         Opcode = 2
	OpFsync          Opcode = 3
	OpReadFixed      Opcode = 4
	OpWriteFixed     Opcode = 5
	OpPollAdd        Opcode = 6
	OpPollRemove     Opcode = 7
	OpSyncFileRange  Opcode = 8
	OpSendmsg        Opcode = 9
	OpRecvmsg        Opcode = 10
	OpTimeout        Opcode = 11
	OpTimeoutRemove  Opcode = 12
	OpAccept         Opcode = 13
	OpAsyncCancel    Opcode = 14
	OpLinkTimeout    Opcode = 15
	OpConnect        Opcode = 16
	OpFallocate      Opcode = 17
	OpOpenat         Opcode = 18
	OpClose          Opcode = 19
	OpFilesUpdate    Opcode = 20
	OpStatx          Opcode = 21
	OpRead           Opcode = 22
	OpWrite          Opcode = 23
	OpFadvise        Opcode = 24
	OpMadvise        Opcode = 25
	OpSend           Opcode = 26
	OpRecv           Opcode = 27
	OpOpenat2        Opcode = 28
	OpEpollCtl       Opcode = 29
	OpSplice         Opcode = 30
	OpProvideBuffers Opcode = 31
	OpRemoveBuffers  Opcode = 32
	OpTee            Opcode = 33
	OpShutdown       Opcode = 34
	OpRenameat       Opcode = 35
	OpUnlinkat       Opcode = 36
	OpMkdirat        Opcode = 37
	OpSymlinkat      Opcode = 38
	OpLinkat         Opcode = 39
)

// Register opcodes from the io_uring ABI.
const (
	RegisterBuffers        RegisterOpcode = 0
	UnregisterBuffers      RegisterOpcode = 1
	RegisterFiles          RegisterOpcode = 2
	UnregisterFiles        RegisterOpcode = 3
	RegisterEventFD        RegisterOpcode = 4
	UnregisterEventFD      RegisterOpcode = 5
	RegisterFilesUpdate    RegisterOpcode = 6
	RegisterEventFDAsync   RegisterOpcode = 7
	RegisterProbe          RegisterOpcode = 8
	RegisterPersonality    RegisterOpcode = 9
	UnregisterPersonality  RegisterOpcode = 10
	RegisterRestrictions   RegisterOpcode = 11
	RegisterEnableRings    RegisterOpcode = 12
	RegisterFiles2         RegisterOpcode = 13
	RegisterFilesUpdate2   RegisterOpcode = 14
	RegisterBuffers2       RegisterOpcode = 15
	RegisterBuffersUpdate  RegisterOpcode = 16
	RegisterIOWQAFF        RegisterOpcode = 17
	UnregisterIOWQAFF      RegisterOpcode = 18
	RegisterIOWQMaxWorkers RegisterOpcode = 19
	RegisterRingFDs        RegisterOpcode = 20
	UnregisterRingFDs      RegisterOpcode = 21
	RegisterPBufRing       RegisterOpcode = 22
	UnregisterPBufRing     RegisterOpcode = 23
	RegisterSyncCancel     RegisterOpcode = 24
	RegisterFileAllocRange RegisterOpcode = 25
)

// Restriction record kinds from the io_uring ABI.
const (
	RestrictionRegisterOp       uint8 = 0
	RestrictionSQEOp            uint8 = 1
	RestrictionSQEFlagsAllowed  uint8 = 2
	RestrictionSQEFlagsRequired uint8 = 3
)

// IOSQE_* flag bits.
const (
	SQEFlagFixedFile      SQEFlags = 1 << 0
	SQEFlagIODrain        SQEFlags = 1 << 1
	SQEFlagIOLink         SQEFlags = 1 << 2
	SQEFlagIOHardlink     SQEFlags = 1 << 3
	SQEFlagAsync          SQEFlags = 1 << 4
	SQEFlagBufferSelect   SQEFlags = 1 << 5
	SQEFlagCQESkipSuccess SQEFlags = 1 << 6
)

// Linux ABI spellings are provided for hosts that already use them.
const (
	IORING_OP_NOP             = OpNop
	IORING_OP_READV           = OpReadv
	IORING_OP_WRITEV          = OpWritev
	IORING_OP_FSYNC           = OpFsync
	IORING_OP_READ_FIXED      = OpReadFixed
	IORING_OP_WRITE_FIXED     = OpWriteFixed
	IORING_OP_POLL_ADD        = OpPollAdd
	IORING_OP_POLL_REMOVE     = OpPollRemove
	IORING_OP_SYNC_FILE_RANGE = OpSyncFileRange
	IORING_OP_SENDMSG         = OpSendmsg
	IORING_OP_RECVMSG         = OpRecvmsg
	IORING_OP_TIMEOUT         = OpTimeout
	IORING_OP_TIMEOUT_REMOVE  = OpTimeoutRemove
	IORING_OP_ACCEPT          = OpAccept
	IORING_OP_ASYNC_CANCEL    = OpAsyncCancel
	IORING_OP_LINK_TIMEOUT    = OpLinkTimeout
	IORING_OP_CONNECT         = OpConnect
	IORING_OP_FALLOCATE       = OpFallocate
	IORING_OP_OPENAT          = OpOpenat
	IORING_OP_CLOSE           = OpClose
	IORING_OP_FILES_UPDATE    = OpFilesUpdate
	IORING_OP_STATX           = OpStatx
	IORING_OP_READ            = OpRead
	IORING_OP_WRITE           = OpWrite
	IORING_OP_FADVISE         = OpFadvise
	IORING_OP_MADVISE         = OpMadvise
	IORING_OP_SEND            = OpSend
	IORING_OP_RECV            = OpRecv
	IORING_OP_OPENAT2         = OpOpenat2
	IORING_OP_EPOLL_CTL       = OpEpollCtl
	IORING_OP_SPLICE          = OpSplice
	IORING_OP_PROVIDE_BUFFERS = OpProvideBuffers
	IORING_OP_REMOVE_BUFFERS  = OpRemoveBuffers
	IORING_OP_TEE             = OpTee
	IORING_OP_SHUTDOWN        = OpShutdown
	IORING_OP_RENAMEAT        = OpRenameat
	IORING_OP_UNLINKAT        = OpUnlinkat
	IORING_OP_MKDIRAT         = OpMkdirat
	IORING_OP_SYMLINKAT       = OpSymlinkat
	IORING_OP_LINKAT          = OpLinkat

	IORING_REGISTER_BUFFERS          = RegisterBuffers
	IORING_UNREGISTER_BUFFERS        = UnregisterBuffers
	IORING_REGISTER_FILES            = RegisterFiles
	IORING_UNREGISTER_FILES          = UnregisterFiles
	IORING_REGISTER_EVENTFD          = RegisterEventFD
	IORING_UNREGISTER_EVENTFD        = UnregisterEventFD
	IORING_REGISTER_FILES_UPDATE     = RegisterFilesUpdate
	IORING_REGISTER_EVENTFD_ASYNC    = RegisterEventFDAsync
	IORING_REGISTER_PROBE            = RegisterProbe
	IORING_REGISTER_PERSONALITY      = RegisterPersonality
	IORING_UNREGISTER_PERSONALITY    = UnregisterPersonality
	IORING_REGISTER_RESTRICTIONS     = RegisterRestrictions
	IORING_REGISTER_ENABLE_RINGS     = RegisterEnableRings
	IORING_REGISTER_FILES2           = RegisterFiles2
	IORING_REGISTER_FILES_UPDATE2    = RegisterFilesUpdate2
	IORING_REGISTER_BUFFERS2         = RegisterBuffers2
	IORING_REGISTER_BUFFERS_UPDATE   = RegisterBuffersUpdate
	IORING_REGISTER_IOWQ_AFF         = RegisterIOWQAFF
	IORING_UNREGISTER_IOWQ_AFF       = UnregisterIOWQAFF
	IORING_REGISTER_IOWQ_MAX_WORKERS = RegisterIOWQMaxWorkers
	IORING_REGISTER_RING_FDS         = RegisterRingFDs
	IORING_UNREGISTER_RING_FDS       = UnregisterRingFDs
	IORING_REGISTER_PBUF_RING        = RegisterPBufRing
	IORING_UNREGISTER_PBUF_RING      = UnregisterPBufRing
	IORING_REGISTER_SYNC_CANCEL      = RegisterSyncCancel
	IORING_REGISTER_FILE_ALLOC_RANGE = RegisterFileAllocRange

	IORING_RESTRICTION_REGISTER_OP        = RestrictionRegisterOp
	IORING_RESTRICTION_SQE_OP             = RestrictionSQEOp
	IORING_RESTRICTION_SQE_FLAGS_ALLOWED  = RestrictionSQEFlagsAllowed
	IORING_RESTRICTION_SQE_FLAGS_REQUIRED = RestrictionSQEFlagsRequired

	IOSQE_FIXED_FILE       = SQEFlagFixedFile
	IOSQE_IO_DRAIN         = SQEFlagIODrain
	IOSQE_IO_LINK          = SQEFlagIOLink
	IOSQE_IO_HARDLINK      = SQEFlagIOHardlink
	IOSQE_ASYNC            = SQEFlagAsync
	IOSQE_BUFFER_SELECT    = SQEFlagBufferSelect
	IOSQE_CQE_SKIP_SUCCESS = SQEFlagCQESkipSuccess
)

// Restrictions is the policy installed by IORING_REGISTER_RESTRICTIONS.
//
// A nil or empty operation list denies every operation in that category.
// RequiredFlags must be a subset of AllowedFlags.
type Restrictions struct {
	AllowedRegisterOps []RegisterOpcode
	AllowedSQEOps      []Opcode
	AllowedFlags       SQEFlags
	RequiredFlags      SQEFlags
}

// RestrictionSet and RestrictionConfig are descriptive aliases for Restrictions.
type RestrictionSet = Restrictions
type RestrictionConfig = Restrictions

var (
	ErrRingNotDisabled         = errors.New("io_uring: ring is not disabled")
	ErrRingDisabled            = errors.New("io_uring: ring is disabled")
	ErrRingAlreadyEnabled      = errors.New("io_uring: ring is already enabled")
	ErrRestrictionsAlreadySet  = errors.New("io_uring: restrictions are already set")
	ErrRestrictionsRequired    = errors.New("io_uring: restrictions must be set before enabling the ring")
	ErrInvalidRestrictions     = errors.New("io_uring: invalid restrictions")
	ErrRequiredFlagsNotAllowed = errors.New("io_uring: required SQE flags are not in the allowed flag set")
	ErrSQEOpcodeNotAllowed     = errors.New("io_uring: SQE opcode is not allowed")
	ErrSQEFlagsNotAllowed      = errors.New("io_uring: SQE flags contain disallowed bits")
	ErrSQERequiredFlagsMissing = errors.New("io_uring: SQE is missing required flags")
	ErrRegisterOpNotAllowed    = errors.New("io_uring: register opcode is not allowed")
)

// Common alternative error names.
var (
	ErrAlreadyRestricted  = ErrRestrictionsAlreadySet
	ErrRestrictionsNotSet = ErrRestrictionsRequired
)

func validateRestrictions(restrictions Restrictions) error {
	if restrictions.RequiredFlags&^restrictions.AllowedFlags != 0 {
		return ErrRequiredFlagsNotAllowed
	}
	return nil
}

// Validate checks whether the policy can be installed as a whole.
func (r Restrictions) Validate() error {
	return validateRestrictions(r)
}

func opcodeSet[T ~uint8](opcodes []T) map[uint8]struct{} {
	set := make(map[uint8]struct{}, len(opcodes))
	for _, opcode := range opcodes {
		set[uint8(opcode)] = struct{}{}
	}
	return set
}
