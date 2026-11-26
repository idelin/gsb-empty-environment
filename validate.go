package seccomp

import "fmt"

// ValidationIssue describes one static rejection.
type ValidationIssue struct {
	PC      int
	Subject string
	Message string
}

func (v ValidationIssue) Error() string {
	if v.PC >= 0 {
		return fmt.Sprintf("seccomp: validation %s at insn %d: %s", v.Subject, v.PC, v.Message)
	}
	return fmt.Sprintf("seccomp: validation %s: %s", v.Subject, v.Message)
}

// ValidationError aggregates all static problems found.
type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "seccomp: validation ok"
	}
	return fmt.Sprintf("seccomp: %d validation issue(s), first: %s", len(e.Issues), e.Issues[0].Error())
}

// Unwrap supports errors.Is/As.
func (e *ValidationError) Unwrap() []error {
	out := make([]error, len(e.Issues))
	for i := range e.Issues {
		out[i] = e.Issues[i]
	}
	return out
}

// ValidateOptions tunes static validation.
type ValidateOptions struct {
	// MaxInstructions defaults to MaxInsns (4096).
	MaxInstructions int
	// ContextSize is the BPF_ABS context size used for offset bounds;
	// it defaults to SeccompDataSize (64).  Set to a negative value to
	// disable data offset checking.
	ContextSize int
	// RequireAlignedABS rejects 32-bit ABS loads not 4-byte aligned and
	// 16-bit loads not 2-byte aligned (the seccomp checker practice).
	RequireAlignedABS bool
	// RejectDynamicReturn makes BPF_RET|BPF_A a validation error because
	// the returned SECCOMP_RET_* action cannot be checked statically.
	RejectDynamicReturn bool
}

// DefaultValidateOptions returns the seccomp-audit validation defaults.
func DefaultValidateOptions() ValidateOptions {
	return ValidateOptions{
		MaxInstructions: MaxInsns,
		ContextSize:     SeccompDataSize,
	}
}

// Validate performs the full static check using default options.
func Validate(insns []Instruction) error {
	return ValidateWith(insns, DefaultValidateOptions())
}

// ValidateWith performs static validation with explicit options.
func ValidateWith(insns []Instruction, opts ValidateOptions) error {
	if opts.MaxInstructions <= 0 {
		opts.MaxInstructions = MaxInsns
	}
	if opts.ContextSize == 0 {
		opts.ContextSize = SeccompDataSize
	}
	var issues []ValidationIssue
	add := func(pc int, subj, format string, args ...any) {
		issues = append(issues, ValidationIssue{PC: pc, Subject: subj, Message: fmt.Sprintf(format, args...)})
	}

	n := len(insns)
	if n == 0 {
		return &ValidationError{Issues: []ValidationIssue{{PC: -1, Subject: "length", Message: "program is empty"}}}
	}
	if n > opts.MaxInstructions {
		add(-1, "length", "program has %d instructions, limit is %d", n, opts.MaxInstructions)
	}

	for pc, insn := range insns {
		if !opcodeKnown(insn.Code) {
			add(pc, "opcode", "illegal/unsupported cBPF opcode 0x%04x", insn.Code)
		}
		checkOperands(pc, insn, opts, add)
		checkJumpBounds(pc, insn, n, add)
	}

	if n > 0 && insns[n-1].Class() != ClassRET {
		add(n-1, "termination", "last instruction must be BPF_RET (kernel classic-BPF requirement)")
	}

	// CFG reachability / termination DAG walk.
	if n > 0 {
		reachable := map[int]bool{0: true}
		stack := []int{0}
		for len(stack) > 0 {
			pc := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, s := range successors(pc, insns) {
				if s < 0 || s >= n {
					// already reported by checkJumpBounds / fallthrough
					continue
				}
				if !reachable[s] {
					reachable[s] = true
					stack = append(stack, s)
				}
			}
		}
		for pc := range reachable {
			insn := insns[pc]
			if insn.Class() == ClassRET {
				if insn.Code == 0x16 || (insn.Code&classMask == ClassRET && insn.Code&0x18 == RetA) {
					if opts.RejectDynamicReturn {
						add(pc, "action", "BPF_RET|BPF_A has no statically known SECCOMP_RET_* action")
					}
					continue
				}
				if !IsLegalAction(insn.K) {
					add(pc, "action", "return constant 0x%08x is not a known SECCOMP_RET_* action", insn.K)
				}
				continue
			}
			succ := successors(pc, insns)
			if len(succ) == 0 {
				add(pc, "termination", "instruction has no successor and is not BPF_RET")
				continue
			}
			for _, s := range succ {
				if s >= n {
					add(pc, "termination", "control flows past end of program without BPF_RET")
				}
			}
		}
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

type issueAdder func(pc int, subj, format string, args ...any)

func checkOperands(pc int, insn Instruction, opts ValidateOptions, add issueAdder) {
	code := insn.Code
	switch code & classMask {
	case ClassST:
		if insn.K >= MemWords {
			add(pc, "mem", "ST index k=%d exceeds M[0..%d]", insn.K, MemWords-1)
		}
	case ClassSTX:
		if insn.K >= MemWords {
			add(pc, "mem", "STX index k=%d exceeds M[0..%d]", insn.K, MemWords-1)
		}
	case ClassLD, ClassLDX:
		mode := code & modeMask
		size := code & sizeMask
		if mode == ModeMEM && insn.K >= MemWords {
			add(pc, "mem", "MEM load index k=%d exceeds M[0..%d]", insn.K, MemWords-1)
		}
		if mode == ModeMSH && insn.K >= uint32(opts.ContextSize) {
			add(pc, "offset", "MSH offset %d out of context size %d", insn.K, opts.ContextSize)
		}
		if mode == ModeABS {
			sz := loadSize(size)
			if opts.ContextSize > 0 && uint64(insn.K)+uint64(sz) > uint64(opts.ContextSize) {
				add(pc, "offset", "ABS load at %d width %d exceeds context size %d", insn.K, sz, opts.ContextSize)
			}
			if opts.RequireAlignedABS {
				align := uint32(sz) - 1
				if sz >= 2 && insn.K&align != 0 {
					add(pc, "alignment", "ABS %d-byte load at unaligned offset %d", sz, insn.K)
				}
			}
		}
		if mode == ModeIND && opts.ContextSize > 0 {
			// Static base must itself be in range; X+k is checked at
			// emulation/symbolic time.
			if insn.K >= uint32(opts.ContextSize) {
				add(pc, "offset", "IND base offset %d out of context size %d", insn.K, opts.ContextSize)
			}
		}
	case ClassALU:
		if code&aluMask == AluDIV && code&srcMask == SrcK && insn.K == 0 {
			add(pc, "alu", "division by constant zero")
		}
		if shiftOp(code) && code&srcMask == SrcK && insn.K >= 32 {
			add(pc, "alu", "constant shift by %d >= 32 is invalid", insn.K)
		}
	}
}

func shiftOp(code uint16) bool {
	op := code & aluMask
	return op == AluLSH || op == AluRSH
}

func loadSize(sizeField uint16) uint32 {
	switch sizeField {
	case SizeH:
		return 2
	case SizeB:
		return 1
	default:
		return 4
	}
}

func checkJumpBounds(pc int, insn Instruction, n int, add issueAdder) {
	if insn.Class() != ClassJMP {
		return
	}
	targets := map[string]int{}
	if insn.Code&jmpMask == JmpJA {
		off := uint64(insn.K)
		if off > uint64(n) {
			add(pc, "jump", "JA offset %d out of program bounds (len=%d)", insn.K, n)
			return
		}
		targets["ja"] = JumpTarget(pc, int(insn.K))
	} else {
		targets["jt"] = JumpTarget(pc, int(insn.JT))
		targets["jf"] = JumpTarget(pc, int(insn.JF))
	}
	for kind, t := range targets {
		if t <= pc {
			add(pc, "jump", "%s target %d is not a forward jump (backward/self jumps are rejected)", kind, t)
		}
		if t < 0 || t >= n {
			add(pc, "jump", "%s target %d out of bounds [0,%d) (pc=%d)", kind, t, n, pc)
		}
	}
}

// successors returns the static CFG successor pcs (empty for RET).
func successors(pc int, insns []Instruction) []int {
	insn := insns[pc]
	switch insn.Class() {
	case ClassRET:
		return nil
	case ClassJMP:
		if insn.Code&jmpMask == JmpJA {
			return []int{JumpTarget(pc, int(insn.K))}
		}
		return []int{
			JumpTarget(pc, int(insn.JT)),
			JumpTarget(pc, int(insn.JF)),
		}
	default:
		return []int{pc + 1}
	}
}

// knownOpcodes is the complete legal cBPF opcode set accepted by classic
// BPF / seccomp (BPF_XOR included).
var knownOpcodes = map[uint16]bool{}

func init() {
	add := func(codes ...uint16) {
		for _, c := range codes {
			knownOpcodes[c] = true
		}
	}
	// LD
	add(0x00, 0x20, 0x28, 0x30, 0x40, 0x48, 0x50, 0x60, 0x80)
	// LDX
	add(0x01, 0x61, 0xb1)
	// ST / STX
	add(0x02, 0x03)
	// ALU K and X
	for _, op := range []uint16{
		AluADD, AluSUB, AluMUL, AluDIV, AluOR, AluAND,
		AluLSH, AluRSH, AluMOD, AluXOR,
	} {
		add(ClassALU|op|SrcK, ClassALU|op|SrcX)
	}
	add(ClassALU | AluNEG) // 0x84 (source bit ignored)
	add(0x8c)              // tolerated NEG with X source bit set
	// JMP
	add(0x05)
	for _, op := range []uint16{JmpJEQ, JmpJGT, JmpJGE, JmpJSET} {
		add(ClassJMP|op|SrcK, ClassJMP|op|SrcX)
	}
	// RET
	add(0x06, 0x16)
	// MISC
	add(0x07, 0x87)
}

func opcodeKnown(code uint16) bool {
	return knownOpcodes[code]
}
