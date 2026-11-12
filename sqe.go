package netsec

import "encoding/binary"

// SQESize is the fixed wire size of struct io_uring_sqe defined in
// linux/io_uring.h. The validator only ever accepts exactly this many bytes.
const SQESize = 64

// Byte offsets of the io_uring_sqe fields the validator inspects. The layout
// follows the 6.12 uapi (file_index_upper / addr3 union at offset 48).
const (
	offOpcode         = 0
	offFlags          = 1
	offIOPrio         = 2
	offFD             = 4
	offOff            = 8
	offAddr           = 16
	offLen            = 24
	offRWFlags        = 28
	offUserData       = 32
	offBufIndex       = 40
	offSpliceFDIn     = 40 // int32; shares a union with personality (offset 42)
	offPersonality    = 42
	offFileIndexUpper = 48
)

// IOSQE_* flag bits (uapi/linux/io_uring.h).
const (
	IOSQEFixedFile      uint8 = 1 << 0 // use registered-files table instead of an fd
	IOSQEIODrain        uint8 = 1 << 1
	IOSQEIOLink         uint8 = 1 << 2 // link to the next SQE
	IOSQEHARDLink       uint8 = 1 << 3
	IOSQEAsync          uint8 = 1 << 4
	IOSQEBufferSelect   uint8 = 1 << 5
	IOSQECQESkipSuccess uint8 = 1 << 6
	IOSQEFileIndexUpper uint8 = 1 << 7 // high byte of the fixed-file index lives in sqe.file_index_upper
)

// DefaultAllowedFlags covers every flag bit currently defined by the kernel.
const DefaultAllowedFlags uint8 = 0xff

// IORING_OP_* opcodes (uapi/linux/io_uring.h). Only the opcodes the proxy
// knows how to classify are listed; anything else is rejected by the
// whitelist even if a future kernel grows new opcodes.
const (
	OpNop            uint8 = 0
	OpReadv          uint8 = 1
	OpWritev         uint8 = 2
	OpFsync          uint8 = 3
	OpReadFixed      uint8 = 4
	OpWriteFixed     uint8 = 5
	OpPollAdd        uint8 = 6
	OpPollRemove     uint8 = 7
	OpSyncFileRange  uint8 = 8
	OpSendmsg        uint8 = 9
	OpRecvmsg        uint8 = 10
	OpTimeout        uint8 = 11
	OpTimeoutRemove  uint8 = 12
	OpAccept         uint8 = 13
	OpAsyncCancel    uint8 = 14
	OpLinkTimeout    uint8 = 15
	OpConnect        uint8 = 16
	OpFallocate      uint8 = 17
	OpOpenat         uint8 = 18
	OpClose          uint8 = 19
	OpFilesUpdate    uint8 = 20
	OpStatx          uint8 = 21
	OpRead           uint8 = 22
	OpWrite          uint8 = 23
	OpFadvise        uint8 = 24
	OpMadvise        uint8 = 25
	OpSend           uint8 = 26
	OpRecv           uint8 = 27
	OpOpenat2        uint8 = 28
	OpEpollCtl       uint8 = 29
	OpSplice         uint8 = 30
	OpProvideBuffers uint8 = 31
	OpRemoveBuffers  uint8 = 32
	OpTee            uint8 = 33
	OpShutdown       uint8 = 34
	OpRenameat       uint8 = 35
	OpUnlinkat       uint8 = 36
	OpMkdirat        uint8 = 37
	OpSymlinkat      uint8 = 38
	OpLinkat         uint8 = 39
	OpMsgRing        uint8 = 40
	OpUringCmd       uint8 = 41
	OpSendZC         uint8 = 42
	OpSendmsgZC      uint8 = 43
	OpReadMultishot  uint8 = 44
	OpWaitid         uint8 = 45
	OpFutexWait      uint8 = 46
	OpFutexWake      uint8 = 47
	OpFutexWaitv     uint8 = 48
	OpFixedFDInstall uint8 = 49
	OpNapiTimeout    uint8 = 50
)

// SQE is the parsed view of one 64-byte submission queue entry.
type SQE struct {
	raw         [SQESize]byte
	Opcode      uint8
	Flags       uint8
	IOPrio      uint16
	FD          int32
	Offset      uint64
	Addr        uint64
	Len         uint32
	RWFlags     uint32
	UserData    uint64
	BufIndex    uint16
	Personality uint16
	// SpliceFDIn is the splice/tee source slot. The wire field is int32;
	// the 6.12 sparse-files union widened it to int64 but then reuses the
	// personality half-word, so the validator only honors the low 32 bits
	// (which are identical in both layouts).
	SpliceFDIn     int64
	FileIndexUpper uint8
}

// ParseSQE copies and decodes a 64-byte SQE. It never retains a reference to
// the caller's buffer, so later mutation cannot affect validation.
func ParseSQE(b []byte) (*SQE, error) {
	if len(b) != SQESize {
		return nil, &RuleError{
			Rule:   RuleSQESize,
			Detail: "sqe must be exactly 64 bytes",
			cause:  ErrSQESize,
		}
	}
	e := &SQE{}
	copy(e.raw[:], b)
	e.Opcode = b[offOpcode]
	e.Flags = b[offFlags]
	e.IOPrio = binary.LittleEndian.Uint16(b[offIOPrio:])
	e.FD = int32(binary.LittleEndian.Uint32(b[offFD:]))
	e.Offset = binary.LittleEndian.Uint64(b[offOff:])
	e.Addr = binary.LittleEndian.Uint64(b[offAddr:])
	e.Len = binary.LittleEndian.Uint32(b[offLen:])
	e.RWFlags = binary.LittleEndian.Uint32(b[offRWFlags:])
	e.UserData = binary.LittleEndian.Uint64(b[offUserData:])
	e.BufIndex = binary.LittleEndian.Uint16(b[offBufIndex:])
	e.SpliceFDIn = int64(int32(binary.LittleEndian.Uint32(b[offSpliceFDIn:])))
	e.Personality = binary.LittleEndian.Uint16(b[offPersonality:])
	e.FileIndexUpper = b[offFileIndexUpper]
	return e, nil
}

// Raw returns a copy of the original SQE bytes.
func (e *SQE) Raw() []byte {
	out := make([]byte, SQESize)
	copy(out, e.raw[:])
	return out
}

// HasFlag reports whether the IOSQE flag bit f is set.
func (e *SQE) HasFlag(f uint8) bool { return e.Flags&f != 0 }

// Linked reports whether the SQE links to the next SQE in the submission
// batch (IOSQE_IO_LINK or IOSQE_IO_HARDLINK).
func (e *SQE) Linked() bool {
	return e.HasFlag(IOSQEIOLink) || e.HasFlag(IOSQEHARDLink)
}

// FixedIndex decodes the registered-files slot index. When
// IOSQE_FILE_INDEX_UPPER is set the kernel combines sqe.fd (low 32 bits) with
// sqe.file_index_upper (bits 32-39); otherwise the index is the plain fd
// field.
func (e *SQE) FixedIndex() uint64 {
	idx := uint64(uint32(e.FD))
	if e.HasFlag(IOSQEFileIndexUpper) {
		idx |= uint64(e.FileIndexUpper) << 32
	}
	return idx
}

// opcodeNames is used for human-readable rule errors.
var opcodeNames = map[uint8]string{
	OpNop: "IORING_OP_NOP", OpReadv: "IORING_OP_READV", OpWritev: "IORING_OP_WRITEV",
	OpFsync: "IORING_OP_FSYNC", OpReadFixed: "IORING_OP_READ_FIXED",
	OpWriteFixed: "IORING_OP_WRITE_FIXED", OpPollAdd: "IORING_OP_POLL_ADD",
	OpPollRemove: "IORING_OP_POLL_REMOVE", OpSyncFileRange: "IORING_OP_SYNC_FILE_RANGE",
	OpSendmsg: "IORING_OP_SENDMSG", OpRecvmsg: "IORING_OP_RECVMSG",
	OpTimeout: "IORING_OP_TIMEOUT", OpTimeoutRemove: "IORING_OP_TIMEOUT_REMOVE",
	OpAccept: "IORING_OP_ACCEPT", OpAsyncCancel: "IORING_OP_ASYNC_CANCEL",
	OpLinkTimeout: "IORING_OP_LINK_TIMEOUT", OpConnect: "IORING_OP_CONNECT",
	OpFallocate: "IORING_OP_FALLOCATE", OpOpenat: "IORING_OP_OPENAT",
	OpClose: "IORING_OP_CLOSE", OpFilesUpdate: "IORING_OP_FILES_UPDATE",
	OpStatx: "IORING_OP_STATX", OpRead: "IORING_OP_READ", OpWrite: "IORING_OP_WRITE",
	OpFadvise: "IORING_OP_FADVISE", OpMadvise: "IORING_OP_MADVISE",
	OpSend: "IORING_OP_SEND", OpRecv: "IORING_OP_RECV", OpOpenat2: "IORING_OP_OPENAT2",
	OpEpollCtl: "IORING_OP_EPOLL_CTL", OpSplice: "IORING_OP_SPLICE",
	OpProvideBuffers: "IORING_OP_PROVIDE_BUFFERS", OpRemoveBuffers: "IORING_OP_REMOVE_BUFFERS",
	OpTee: "IORING_OP_TEE", OpShutdown: "IORING_OP_SHUTDOWN",
	OpRenameat: "IORING_OP_RENAMEAT", OpUnlinkat: "IORING_OP_UNLINKAT",
	OpMkdirat: "IORING_OP_MKDIRAT", OpSymlinkat: "IORING_OP_SYMLINKAT",
	OpLinkat: "IORING_OP_LINKAT", OpMsgRing: "IORING_OP_MSG_RING",
	OpUringCmd: "IORING_OP_URING_CMD", OpSendZC: "IORING_OP_SEND_ZC",
	OpSendmsgZC: "IORING_OP_SENDMSG_ZC", OpReadMultishot: "IORING_OP_READ_MULTISHOT",
	OpWaitid: "IORING_OP_WAITID", OpFutexWait: "IORING_OP_FUTEX_WAIT",
	OpFutexWake: "IORING_OP_FUTEX_WAKE", OpFutexWaitv: "IORING_OP_FUTEX_WAITV",
	OpFixedFDInstall: "IORING_OP_FIXED_FD_INSTALL", OpNapiTimeout: "IORING_OP_NAPI_TIMEOUT",
}

// OpcodeName returns the kernel name of an opcode, or a hex fallback.
func OpcodeName(op uint8) string {
	if name, ok := opcodeNames[op]; ok {
		return name
	}
	return "IORING_OP_UNKNOWN(0x" + hexByte(op) + ")"
}

func hexByte(b uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0x0f]})
}
