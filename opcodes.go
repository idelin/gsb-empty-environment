package netsec

// Opcode is the operation code stored in an io_uring submission queue entry.
// io_uring encodes it as an unsigned byte.
type Opcode = uint8

// OpCode is an alias for Opcode.
type OpCode = Opcode

// RegisterOpcode identifies an io_uring registration operation.
// io_uring passes it to io_uring_register(2) as an unsigned int.
type RegisterOpcode = uint32

// RegisterOp is an alias for RegisterOpcode.
type RegisterOp = RegisterOpcode

// SQEFlags contains the IOSQE_* flags of a submission queue entry.
type SQEFlags = uint8

// Flags is an alias for SQEFlags.
type Flags = SQEFlags

// SQE opcodes from the io_uring ABI.
const (
	IORING_OP_NOP             Opcode = 0
	IORING_OP_READV           Opcode = 1
	IORING_OP_WRITEV          Opcode = 2
	IORING_OP_FSYNC           Opcode = 3
	IORING_OP_READ_FIXED      Opcode = 4
	IORING_OP_WRITE_FIXED     Opcode = 5
	IORING_OP_POLL_ADD        Opcode = 6
	IORING_OP_POLL_REMOVE     Opcode = 7
	IORING_OP_SYNC_FILE_RANGE Opcode = 8
	IORING_OP_SENDMSG         Opcode = 9
	IORING_OP_RECVMSG         Opcode = 10
	IORING_OP_TIMEOUT         Opcode = 11
	IORING_OP_TIMEOUT_REMOVE  Opcode = 12
	IORING_OP_ACCEPT          Opcode = 13
	IORING_OP_ASYNC_CANCEL    Opcode = 14
	IORING_OP_LINK_TIMEOUT    Opcode = 15
	IORING_OP_CONNECT         Opcode = 16
	IORING_OP_FALLOCATE       Opcode = 17
	IORING_OP_OPENAT          Opcode = 18
	IORING_OP_CLOSE           Opcode = 19
	IORING_OP_FILES_UPDATE    Opcode = 20
	IORING_OP_STATX           Opcode = 21
	IORING_OP_READ            Opcode = 22
	IORING_OP_WRITE           Opcode = 23
	IORING_OP_FADVISE         Opcode = 24
	IORING_OP_MADVISE         Opcode = 25
	IORING_OP_SEND            Opcode = 26
	IORING_OP_RECV            Opcode = 27
	IORING_OP_OPENAT2         Opcode = 28
	IORING_OP_EPOLL_CTL       Opcode = 29
	IORING_OP_SPLICE          Opcode = 30
	IORING_OP_PROVIDE_BUFFERS Opcode = 31
	IORING_OP_REMOVE_BUFFERS  Opcode = 32
	IORING_OP_TEE             Opcode = 33
	IORING_OP_SHUTDOWN        Opcode = 34
	IORING_OP_RENAMEAT        Opcode = 35
	IORING_OP_UNLINKAT        Opcode = 36
	IORING_OP_MKDIRAT         Opcode = 37
	IORING_OP_SYMLINKAT       Opcode = 38
	IORING_OP_LINKAT          Opcode = 39
	IORING_OP_MSG_RING        Opcode = 40
	IORING_OP_FSETXATTR       Opcode = 41
	IORING_OP_SETXATTR        Opcode = 42
	IORING_OP_FGETXATTR       Opcode = 43
	IORING_OP_GETXATTR        Opcode = 44
	IORING_OP_SOCKET          Opcode = 45
	IORING_OP_URING_CMD       Opcode = 46
)

// Short SQE opcode names.
const (
	OpNop            = IORING_OP_NOP
	OpReadv          = IORING_OP_READV
	OpWritev         = IORING_OP_WRITEV
	OpFsync          = IORING_OP_FSYNC
	OpReadFixed      = IORING_OP_READ_FIXED
	OpWriteFixed     = IORING_OP_WRITE_FIXED
	OpPollAdd        = IORING_OP_POLL_ADD
	OpPollRemove     = IORING_OP_POLL_REMOVE
	OpSyncFileRange  = IORING_OP_SYNC_FILE_RANGE
	OpSendmsg        = IORING_OP_SENDMSG
	OpRecvmsg        = IORING_OP_RECVMSG
	OpTimeout        = IORING_OP_TIMEOUT
	OpTimeoutRemove  = IORING_OP_TIMEOUT_REMOVE
	OpAccept         = IORING_OP_ACCEPT
	OpAsyncCancel    = IORING_OP_ASYNC_CANCEL
	OpLinkTimeout    = IORING_OP_LINK_TIMEOUT
	OpConnect        = IORING_OP_CONNECT
	OpFallocate      = IORING_OP_FALLOCATE
	OpOpenat         = IORING_OP_OPENAT
	OpClose          = IORING_OP_CLOSE
	OpFilesUpdate    = IORING_OP_FILES_UPDATE
	OpStatx          = IORING_OP_STATX
	OpRead           = IORING_OP_READ
	OpWrite          = IORING_OP_WRITE
	OpFadvise        = IORING_OP_FADVISE
	OpMadvise        = IORING_OP_MADVISE
	OpSend           = IORING_OP_SEND
	OpRecv           = IORING_OP_RECV
	OpOpenat2        = IORING_OP_OPENAT2
	OpEpollCtl       = IORING_OP_EPOLL_CTL
	OpSplice         = IORING_OP_SPLICE
	OpProvideBuffers = IORING_OP_PROVIDE_BUFFERS
	OpRemoveBuffers  = IORING_OP_REMOVE_BUFFERS
	OpTee            = IORING_OP_TEE
	OpShutdown       = IORING_OP_SHUTDOWN
	OpRenameat       = IORING_OP_RENAMEAT
	OpUnlinkat       = IORING_OP_UNLINKAT
	OpMkdirat        = IORING_OP_MKDIRAT
	OpSymlinkat      = IORING_OP_SYMLINKAT
	OpLinkat         = IORING_OP_LINKAT
	OpMsgRing        = IORING_OP_MSG_RING
	OpFsetxattr      = IORING_OP_FSETXATTR
	OpSetxattr       = IORING_OP_SETXATTR
	OpFgetxattr      = IORING_OP_FGETXATTR
	OpGetxattr       = IORING_OP_GETXATTR
	OpSocket         = IORING_OP_SOCKET
	OpUringCmd       = IORING_OP_URING_CMD
)

// Registration opcodes from the io_uring ABI.
const (
	IORING_REGISTER_BUFFERS          RegisterOpcode = 0
	IORING_UNREGISTER_BUFFERS        RegisterOpcode = 1
	IORING_REGISTER_FILES            RegisterOpcode = 2
	IORING_UNREGISTER_FILES          RegisterOpcode = 3
	IORING_REGISTER_EVENTFD          RegisterOpcode = 4
	IORING_UNREGISTER_EVENTFD        RegisterOpcode = 5
	IORING_REGISTER_FILES_UPDATE     RegisterOpcode = 6
	IORING_REGISTER_EVENTFD_ASYNC    RegisterOpcode = 7
	IORING_REGISTER_PROBE            RegisterOpcode = 8
	IORING_REGISTER_PERSONALITY      RegisterOpcode = 9
	IORING_UNREGISTER_PERSONALITY    RegisterOpcode = 10
	IORING_REGISTER_RESTRICTIONS     RegisterOpcode = 11
	IORING_REGISTER_ENABLE_RINGS     RegisterOpcode = 12
	IORING_REGISTER_FILES2           RegisterOpcode = 13
	IORING_REGISTER_FILES_UPDATE2    RegisterOpcode = 14
	IORING_REGISTER_BUFFERS2         RegisterOpcode = 15
	IORING_UNREGISTER_BUFFERS2       RegisterOpcode = 16
	IORING_REGISTER_RING_FDS         RegisterOpcode = 17
	IORING_UNREGISTER_RING_FDS       RegisterOpcode = 18
	IORING_REGISTER_RING_FDS2        RegisterOpcode = 19
	IORING_REGISTER_SYNC_CANCEL      RegisterOpcode = 20
	IORING_REGISTER_IOWQ_AFF         RegisterOpcode = 21
	IORING_UNREGISTER_IOWQ_AFF       RegisterOpcode = 22
	IORING_REGISTER_IOWQ_MAX_WORKERS RegisterOpcode = 23
	IORING_REGISTER_NAPI             RegisterOpcode = 24
	IORING_UNREGISTER_NAPI           RegisterOpcode = 25
	IORING_REGISTER_CLOCK            RegisterOpcode = 26
)

// Short registration opcode names.
const (
	RegisterBuffers        = IORING_REGISTER_BUFFERS
	UnregisterBuffers      = IORING_UNREGISTER_BUFFERS
	RegisterFiles          = IORING_REGISTER_FILES
	UnregisterFiles        = IORING_UNREGISTER_FILES
	RegisterEventFD        = IORING_REGISTER_EVENTFD
	UnregisterEventFD      = IORING_UNREGISTER_EVENTFD
	RegisterFilesUpdate    = IORING_REGISTER_FILES_UPDATE
	RegisterEventFDAsync   = IORING_REGISTER_EVENTFD_ASYNC
	RegisterProbe          = IORING_REGISTER_PROBE
	RegisterPersonality    = IORING_REGISTER_PERSONALITY
	UnregisterPersonality  = IORING_UNREGISTER_PERSONALITY
	RegisterRestrictions   = IORING_REGISTER_RESTRICTIONS
	RegisterEnableRings    = IORING_REGISTER_ENABLE_RINGS
	RegisterFiles2         = IORING_REGISTER_FILES2
	RegisterFilesUpdate2   = IORING_REGISTER_FILES_UPDATE2
	RegisterBuffers2       = IORING_REGISTER_BUFFERS2
	UnregisterBuffers2     = IORING_UNREGISTER_BUFFERS2
	RegisterRingFDs        = IORING_REGISTER_RING_FDS
	UnregisterRingFDs      = IORING_UNREGISTER_RING_FDS
	RegisterRingFDs2       = IORING_REGISTER_RING_FDS2
	RegisterSyncCancel     = IORING_REGISTER_SYNC_CANCEL
	RegisterIOWQAff        = IORING_REGISTER_IOWQ_AFF
	UnregisterIOWQAff      = IORING_UNREGISTER_IOWQ_AFF
	RegisterIOWQMaxWorkers = IORING_REGISTER_IOWQ_MAX_WORKERS
	RegisterNapi           = IORING_REGISTER_NAPI
	UnregisterNapi         = IORING_UNREGISTER_NAPI
	RegisterClock          = IORING_REGISTER_CLOCK
)

// IOSQE_* SQE flags from the io_uring ABI.
const (
	IOSQE_FIXED_FILE       SQEFlags = 1 << 0
	IOSQE_IO_DRAIN         SQEFlags = 1 << 1
	IOSQE_IO_LINK          SQEFlags = 1 << 2
	IOSQE_IO_HARDLINK      SQEFlags = 1 << 3
	IOSQE_ASYNC            SQEFlags = 1 << 4
	IOSQE_BUFFER_SELECT    SQEFlags = 1 << 5
	IOSQE_CQE_SKIP_SUCCESS SQEFlags = 1 << 6
)

// Short SQE flag names.
const (
	SQEFixedFile      = IOSQE_FIXED_FILE
	SQEIODrain        = IOSQE_IO_DRAIN
	SQEIOChain        = IOSQE_IO_LINK
	SQEIOHardlink     = IOSQE_IO_HARDLINK
	SQEAsync          = IOSQE_ASYNC
	SQEBufferSelect   = IOSQE_BUFFER_SELECT
	SQECQESkipSuccess = IOSQE_CQE_SKIP_SUCCESS
)

// Ring creation setup flags from the io_uring ABI.
const (
	IORING_SETUP_IOPOLL        uint32 = 1 << 0
	IORING_SETUP_SQPOLL        uint32 = 1 << 1
	IORING_SETUP_SQ_AFF        uint32 = 1 << 2
	IORING_SETUP_CQSIZE        uint32 = 1 << 3
	IORING_SETUP_CLAMP         uint32 = 1 << 4
	IORING_SETUP_ATTACH_WQ     uint32 = 1 << 5
	IORING_SETUP_R_DISABLED    uint32 = 1 << 6
	IORING_SETUP_SUBMIT_ALL    uint32 = 1 << 7
	IORING_SETUP_COOP_TASKRUN  uint32 = 1 << 8
	IORING_SETUP_TASKRUN_FLAG  uint32 = 1 << 9
	IORING_SETUP_SQE128        uint32 = 1 << 10
	IORING_SETUP_CQE32         uint32 = 1 << 11
	IORING_SETUP_SINGLE_ISSUER uint32 = 1 << 12
	IORING_SETUP_DEFER_TASKRUN uint32 = 1 << 13
)
