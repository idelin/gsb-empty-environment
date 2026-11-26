package seccomp

import "fmt"

// String returns a tcpdump-style mnemonic for the instruction.
func (insn Instruction) String() string {
	switch insn.Code & classMask {
	case ClassLD, ClassLDX:
		return insn.disasmLoad()
	case ClassST, ClassSTX:
		reg := "M[A]"
		if insn.Class() == ClassST {
			reg = "M[k]=A"
			return fmt.Sprintf("st M[%d], A", insn.K)
		}
		_ = reg
		return fmt.Sprintf("stx M[%d], X", insn.K)
	case ClassALU:
		return insn.disasmALU()
	case ClassJMP:
		return insn.disasmJMP()
	case ClassRET:
		if insn.Code&0x18 == RetA || insn.Code == 0x16 {
			return "ret A"
		}
		return fmt.Sprintf("ret %s", ActionName(insn.K))
	case ClassMISC:
		switch insn.Code & miscMask {
		case MiscTAX:
			return "tax X=A"
		case MiscTXA:
			return "txa A=X"
		default:
			return fmt.Sprintf("misc ??? code=0x%04x", insn.Code)
		}
	}
	return fmt.Sprintf(".word 0x%04x,%d,%d,0x%08x", insn.Code, insn.JT, insn.JF, insn.K)
}

// Disassemble returns a numbered human readable listing of the program.
func Disassemble(insns []Instruction) string {
	out := ""
	for i, insn := range insns {
		out += fmt.Sprintf("%4d: %-40s ; raw=%s\n", i, insn.String(), insn.BytesHex())
	}
	return out
}

// BytesHex returns the raw 8 bytes as spaced hex.
func (insn Instruction) BytesHex() string {
	b := insn.Bytes()
	return fmt.Sprintf("%02x %02x %02x %02x %02x %02x %02x %02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7])
}

func sizeName(code uint16) string {
	switch code & sizeMask {
	case SizeW:
		return "w"
	case SizeH:
		return "h"
	case SizeB:
		return "b"
	}
	return "?"
}

func (insn Instruction) disasmLoad() string {
	class := "ld"
	dst := "A"
	if insn.Class() == ClassLDX {
		class = "ldx"
		dst = "X"
	}
	code := insn.Code
	mode := code & modeMask
	size := sizeName(code)
	switch mode {
	case ModeIMM:
		if insn.Class() == ClassLDX && size != "w" {
			return fmt.Sprintf("ldx%s #0x%x", size, insn.K)
		}
		return fmt.Sprintf("%s %s = #0x%x (%d)", class, dst, insn.K, insn.K)
	case ModeABS:
		return fmt.Sprintf("%s%s %s = seccomp[%d]", class, size, dst, insn.K)
	case ModeIND:
		return fmt.Sprintf("%s%s %s = seccomp[X+%d]", class, size, dst, insn.K)
	case ModeMEM:
		return fmt.Sprintf("%s %s = M[%d]", class, dst, insn.K)
	case ModeLEN:
		return fmt.Sprintf("%s%s %s = len", class, size, dst)
	case ModeMSH:
		return fmt.Sprintf("ldxb X = 4*(seccomp[%d]&0xf)", insn.K)
	}
	return fmt.Sprintf("%s ??? code=0x%04x k=0x%x", class, code, insn.K)
}

func (insn Instruction) disasmALU() string {
	ops := map[uint16]string{
		AluADD: "add", AluSUB: "sub", AluMUL: "mul", AluDIV: "div",
		AluOR: "or", AluAND: "and", AluLSH: "lsh", AluRSH: "rsh",
		AluNEG: "neg", AluMOD: "mod", AluXOR: "xor",
	}
	op := ops[insn.Code&aluMask]
	if op == "" {
		op = "alu?"
	}
	if insn.Code&aluMask == AluNEG {
		return fmt.Sprintf("A = -A (%s)", op)
	}
	if insn.Code&srcMask == SrcX {
		return fmt.Sprintf("A = A %s X", op)
	}
	return fmt.Sprintf("A = A %s #0x%x (%d)", op, insn.K, insn.K)
}

func (insn Instruction) disasmJMP() string {
	pc := 0 // offsets are rendered relatively by callers; show raw skips.
	_ = pc
	t := int(insn.JT)
	f := int(insn.JF)
	switch insn.Code & jmpMask {
	case JmpJA:
		return fmt.Sprintf("ja +%d (-> pc+%d)", insn.K, int(insn.K)+1)
	case JmpJEQ, JmpJGT, JmpJGE, JmpJSET:
		names := map[uint16]string{
			JmpJEQ: "jeq", JmpJGT: "jgt", JmpJGE: "jge", JmpJSET: "jset",
		}
		name := names[insn.Code&jmpMask]
		operand := fmt.Sprintf("#0x%x", insn.K)
		if insn.Code&srcMask == SrcX {
			operand = "X"
		}
		cond := map[string]string{
			"jeq":  "A == " + operand,
			"jgt":  "A > " + operand + " (u)",
			"jge":  "A >= " + operand + " (u)",
			"jset": "A & " + operand + " != 0",
		}[name]
		return fmt.Sprintf("%s %s ? +%d : +%d", name, cond, t+1, f+1)
	}
	return fmt.Sprintf("jmp ??? code=0x%04x", insn.Code)
}

// ActionName renders a SECCOMP_RET_* value with its data payload.
func ActionName(ret uint32) string {
	action := ret & RetActionFull
	data := ret & RetData
	var base string
	switch action {
	case RetKillProcess:
		base = "KILL_PROCESS"
	case RetKillThread:
		base = "KILL_THREAD"
	case RetTrap:
		base = "TRAP"
	case RetErrno:
		base = "ERRNO"
	case RetUserNotif:
		base = "USER_NOTIF"
	case RetTrace:
		base = "TRACE"
	case RetLog:
		base = "LOG"
	case RetAllow:
		base = "ALLOW"
	default:
		return fmt.Sprintf("UNKNOWN_ACTION=0x%08x", ret)
	}
	if data != 0 && (action == RetErrno || action == RetTrace) {
		return fmt.Sprintf("%s(data=0x%x/%d)", base, data, data)
	}
	if data != 0 {
		return fmt.Sprintf("%s|0x%04x", base, data)
	}
	return base
}

// IsLegalAction reports whether ret carries a known SECCOMP_RET_* action
// class in its upper bits.
func IsLegalAction(ret uint32) bool {
	switch ret & RetActionFull {
	case RetKillProcess, RetKillThread, RetTrap, RetErrno,
		RetUserNotif, RetTrace, RetLog, RetAllow:
		return true
	}
	return false
}

// IsLaxAction reports whether ret is one of the actions that let the
// syscall proceed (ALLOW, and optionally LOG which allows after logging).
func IsLaxAction(ret uint32, includeLog bool) bool {
	a := ret & RetActionFull
	if a == RetAllow {
		return true
	}
	if includeLog && a == RetLog {
		return true
	}
	return false
}
