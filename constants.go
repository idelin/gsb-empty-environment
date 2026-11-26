// Package seccomp implements classic BPF (cBPF / struct sock_filter)
// instruction decoding, static validation and symbolic path exploration
// for auditing Linux seccomp filters against host syscall prohibition
// policies.
//
// The package is dependency free (standard library only) and is safe to
// import from container security audit tooling.
//
// The central verification entry point is Verify.  Given a decoded filter
// program and a host Policy describing the architectures, syscall numbers
// and argument bit-mask conditions that must be blocked, it explores the
// program DAG symbolically (tracking A, X, M[0..15] and every word of
// struct seccomp_data with interval / bit-mask domains) and returns
// concrete, reproducible bypass tuples (arch, nr, arg0..arg5).
package seccomp

// cBPF instruction classes (code & 0x07), see linux/filter.h and
// linux/bpf_common.h.
const (
	ClassLD   uint16 = 0x00
	ClassLDX  uint16 = 0x01
	ClassST   uint16 = 0x02
	ClassSTX  uint16 = 0x03
	ClassALU  uint16 = 0x04
	ClassJMP  uint16 = 0x05
	ClassRET  uint16 = 0x06
	ClassMISC uint16 = 0x07

	classMask uint16 = 0x07
)

// Load/store size fields (code & 0x18).
const (
	SizeW uint16 = 0x00 // 32-bit word
	SizeH uint16 = 0x08 // 16-bit half word
	SizeB uint16 = 0x10 // 8-bit byte

	sizeMask uint16 = 0x18
)

// Load addressing modes (code & 0xe0).
const (
	ModeIMM uint16 = 0x00
	ModeABS uint16 = 0x20
	ModeIND uint16 = 0x40
	ModeMEM uint16 = 0x60
	ModeLEN uint16 = 0x80
	ModeMSH uint16 = 0xa0

	modeMask uint16 = 0xe0
)

// ALU operations (code & 0xf0).
const (
	AluADD uint16 = 0x00
	AluSUB uint16 = 0x10
	AluMUL uint16 = 0x20
	AluDIV uint16 = 0x30
	AluOR  uint16 = 0x40
	AluAND uint16 = 0x50
	AluLSH uint16 = 0x60
	AluRSH uint16 = 0x70
	AluNEG uint16 = 0x80
	AluMOD uint16 = 0x90
	AluXOR uint16 = 0xa0

	aluMask uint16 = 0xf0
)

// Jump operations (code & 0xf0).
const (
	JmpJA   uint16 = 0x00
	JmpJEQ  uint16 = 0x10
	JmpJGT  uint16 = 0x20
	JmpJGE  uint16 = 0x30
	JmpJSET uint16 = 0x40

	jmpMask uint16 = 0xf0
)

// Source operand selector (code & 0x08): constant K or index register X.
const (
	SrcK uint16 = 0x00
	SrcX uint16 = 0x08

	srcMask uint16 = 0x08
)

// Return source selector.  BPF_RET|BPF_K returns the constant k and
// BPF_RET|BPF_A (0x16) returns the A register.
const (
	RetK uint16 = 0x00
	RetA uint16 = 0x10
)

// MISC operations.
const (
	MiscTAX uint16 = 0x00 // X = A
	MiscTXA uint16 = 0x80 // A = X

	miscMask uint16 = 0xf8
)

// BPF memory scratch slot count (M[0..BPF_MEMWORDS-1]).
const MemWords = 16

// MaxInsns is the classic BPF / seccomp per-filter instruction limit.
const MaxInsns = 4096

// InsnSize is the on-wire size of struct sock_filter.
const InsnSize = 8

// sizeof(struct seccomp_data):
//
//	int               nr                    // [0,4)
//	__u32             arch                  // [4,8)
//	__u64             instruction_pointer   // [8,16)
//	__u64             args[6]               // [16,64)
const SeccompDataSize = 64

// seccomp_data field offsets.
const (
	OffNR     = 0
	OffArch   = 4
	OffIP     = 8
	OffArg0   = 16
	ArgStride = 8
	ArgCount  = 6
)

// Word IDs for the 16 aligned 32-bit words of seccomp_data.
const (
	WordNR     = OffNR / 4   // 0
	WordArch   = OffArch / 4 // 1
	WordIPLo   = OffIP / 4   // 2
	WordIPHi   = 12 / 4      // 3
	WordArg0Lo = OffArg0 / 4 // 4
	WordArg0Hi = 20 / 4      // 5
)

// ArgLoWord returns the seccomp_data word id holding the low 32 bits of
// argument i.
func ArgLoWord(i int) int { return WordArg0Lo + 2*i }

// ArgHiWord returns the seccomp_data word id holding the high 32 bits of
// argument i.
func ArgHiWord(i int) int { return WordArg0Hi + 2*i }

// SECCOMP_RET_* action values, see include/uapi/linux/seccomp.h.
const (
	RetKillProcess uint32 = 0x80000000
	RetKillThread  uint32 = 0x00000000
	RetKill        uint32 = RetKillThread // historical alias
	RetTrap        uint32 = 0x00030000
	RetErrno       uint32 = 0x00050000
	RetUserNotif   uint32 = 0x7fc00000
	RetTrace       uint32 = 0x7ff00000
	RetLog         uint32 = 0x7ffc0000
	RetAllow       uint32 = 0x7fff0000

	// RetActionFull masks the whole upper 16-bit action (includes the
	// kill-process bit 31).
	RetActionFull uint32 = 0xffff0000
	// RetAction masks the comparable action class used by the kernel
	// multi-filter merger; notably it excludes bit 31, so
	// RetKillProcess compares equal-class to RetKillThread.
	RetAction uint32 = 0x7fff0000
	// RetData masks the return-value payload (errno / trace data).
	RetData uint32 = 0x0000ffff
)

// X32SyscallBit is __X32_SYSCALL_BIT: x32 ABI syscall numbers on x86-64
// carry bit 30 set while still reporting AUDIT_ARCH_X86_64 in
// seccomp_data.arch.
const X32SyscallBit uint32 = 0x40000000

// Audit architecture constants (EM_* | __AUDIT_ARCH_64BIT | __AUDIT_ARCH_LE).
const (
	AuditArchI386    uint32 = 0x40000003 // EM_386 | __AUDIT_ARCH_LE
	AuditArchX8664   uint32 = 0xc000003e // EM_X86_64 | 64BIT | LE
	AuditArchAARCH64 uint32 = 0xc00000b7 // EM_AARCH64 | 64BIT | LE
)

// Common syscall numbers used by the bundled attack test-sets.
const (
	SysExecveX8664   uint32 = 59
	SysPtraceX8664   uint32 = 101
	SysBpfX8664      uint32 = 321
	SysExecveI386    uint32 = 11
	SysPtraceI386    uint32 = 26
	SysBpfI386       uint32 = 357
	SysExecveAARCH64 uint32 = 221
	SysPtraceAARCH64 uint32 = 117
	SysBpfAARCH64    uint32 = 280
)
