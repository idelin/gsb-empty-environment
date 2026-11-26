package seccomp

import "errors"

// ErrFallThrough is returned by the concrete emulator if control reaches the
// end of the program without BPF_RET.  Validate rejects such programs.
var ErrFallThrough = errors.New("seccomp: bpf program fell through without return")

// Run concretely executes a decoded cBPF program against d and returns the
// raw SECCOMP_RET_* value.  It implements the complete classic instruction
// set with 32-bit modular semantics.  A runtime division/modulo by a zero X
// register mirrors the kernel and yields return value 0
// (SECCOMP_RET_KILL_THREAD).
func Run(insns []Instruction, d *Data) (uint32, error) {
	ret, _, err := runConcrete(insns, d)
	return ret, err
}

// RunTuple is a convenience wrapper building seccomp_data from a Tuple.
func RunTuple(insns []Instruction, t Tuple) (uint32, error) {
	return Run(insns, t.Data())
}

// runConcrete returns the action and the executed pc trace.
func runConcrete(insns []Instruction, d *Data) (uint32, []int, error) {
	if d == nil {
		d = &Data{}
	}
	img := d.Bytes()
	var a, x uint32
	var mem [MemWords]uint32
	var path []int

	loadAt := func(off uint32, size uint16) uint32 {
		o := int(off)
		if o < 0 || o+int(size) > len(img) {
			return 0
		}
		switch size {
		case SizeH:
			return uint32(img[o]) | uint32(img[o+1])<<8
		case SizeB:
			return uint32(img[o])
		default:
			return uint32(img[o]) | uint32(img[o+1])<<8 |
				uint32(img[o+2])<<16 | uint32(img[o+3])<<24
		}
	}

	for pc := 0; pc < len(insns); pc++ {
		path = append(path, pc)
		insn := insns[pc]
		code := insn.Code
		k := insn.K

		switch code & classMask {
		case ClassLD, ClassLDX:
			size := loadSize(code & sizeMask)
			mode := code & modeMask
			switch mode {
			case ModeIMM:
				if code&classMask == ClassLD {
					a = k
				} else {
					x = k
				}
			case ModeABS:
				v := loadAt(k, uint16(size))
				if code&classMask == ClassLD {
					a = v
				} else {
					x = v
				}
			case ModeIND:
				v := loadAt(k+x, uint16(size))
				if code&classMask == ClassLD {
					a = v
				} else {
					x = v
				}
			case ModeMEM:
				if int(k) < MemWords {
					if code&classMask == ClassLD {
						a = mem[k]
					} else {
						x = mem[k]
					}
				}
			case ModeLEN:
				// seccomp_data has no skb len; audit model uses 0.
				if code&classMask == ClassLD {
					a = 0
				} else {
					x = 0
				}
			case ModeMSH:
				x = 4 * (loadAt(k, 1) & 0x0f)
			}

		case ClassST:
			if int(k) < MemWords {
				mem[k] = a
			}
		case ClassSTX:
			if int(k) < MemWords {
				mem[k] = x
			}

		case ClassALU:
			var src uint32 = k
			if code&srcMask == SrcX {
				src = x
			}
			switch code & aluMask {
			case AluADD:
				a += src
			case AluSUB:
				a -= src
			case AluMUL:
				a *= src
			case AluDIV:
				if src == 0 {
					return RetKillThread, path, nil
				}
				a /= src
			case AluMOD:
				if src == 0 {
					return RetKillThread, path, nil
				}
				a %= src
			case AluOR:
				a |= src
			case AluAND:
				a &= src
			case AluXOR:
				a ^= src
			case AluLSH:
				a = shiftLeft32(a, src)
			case AluRSH:
				a = shiftRight32(a, src)
			case AluNEG:
				a = -a
			}

		case ClassJMP:
			var take bool
			switch code & jmpMask {
			case JmpJA:
				pc = JumpTarget(pc, int(k)) - 1 // loop pc++
				continue
			case JmpJEQ:
				op := k
				if code&srcMask == SrcX {
					op = x
				}
				take = a == op
			case JmpJGT:
				op := k
				if code&srcMask == SrcX {
					op = x
				}
				take = a > op
			case JmpJGE:
				op := k
				if code&srcMask == SrcX {
					op = x
				}
				take = a >= op
			case JmpJSET:
				op := k
				if code&srcMask == SrcX {
					op = x
				}
				take = a&op != 0
			}
			off := insn.JF
			if take {
				off = insn.JT
			}
			pc = JumpTarget(pc, int(off)) - 1
			continue

		case ClassRET:
			if code == 0x16 || code&0x18 == RetA {
				return a, path, nil
			}
			return k, path, nil

		case ClassMISC:
			switch code & miscMask {
			case MiscTAX:
				x = a
			case MiscTXA:
				a = x
			}
		}
	}
	return 0, path, ErrFallThrough
}

func shiftLeft32(v uint32, n uint32) uint32 {
	if n >= 32 {
		return 0
	}
	return v << n
}

func shiftRight32(v uint32, n uint32) uint32 {
	if n >= 32 {
		return 0
	}
	return v >> n
}
