package packetvm

import "errors"

// ErrOutOfBounds is returned by the interpreter only if a verified program
// ever attempts an in-bounds-unsafe read. Because Compile rejects such
// programs, observing this error indicates a verifier defect.
var ErrOutOfBounds = errors.New("packetvm: packet read outside verified bounds")

// ErrMalformedProgram is returned for structurally invalid bytecode.
var ErrMalformedProgram = errors.New("packetvm: malformed program")

// Execute runs a compiled program over pkt and returns its boolean verdict.
// It is deliberately written as an independent, straightforward reference
// machine: it shares no code with either the compiler or the verifier.
func (p *Program) Execute(pkt []byte) (bool, error) {
	if p == nil || len(p.Insns) == 0 {
		return false, ErrMalformedProgram
	}
	var regs [2]uint32
	pktLen := uint32(len(pkt))

	// The step cap makes a malformed cyclic program fail closed rather than
	// hang; verified programs are acyclic and terminate well within it.
	steps := 0
	pc := 0
	for pc >= 0 && pc < len(p.Insns) {
		if steps > len(p.Insns)+1 {
			return false, ErrMalformedProgram
		}
		steps++
		in := p.Insns[pc]

		switch in.Op {
		case OpImm:
			regs[in.Dst] = in.Imm
			pc++
		case OpLen:
			regs[in.Dst] = pktLen
			pc++
		case OpLoad:
			val, err := readFixed(pkt, in.Offset, in.Size)
			if err != nil {
				return false, err
			}
			regs[in.Dst] = val
			pc++
		case OpJmp:
			pc += int(in.Off)
		case OpJmpIf:
			rhs := in.Imm
			if in.UseReg {
				rhs = regs[R1]
			}
			if Compare(in.Cond, regs[R0], rhs) {
				pc += int(in.Off)
			} else {
				pc++
			}
		case OpRet:
			return in.Verdict, nil
		default:
			return false, ErrMalformedProgram
		}
	}
	return false, ErrMalformedProgram
}

// readFixed performs a network-byte-order (big-endian) fixed-width read.
func readFixed(pkt []byte, offset uint32, size Size) (uint32, error) {
	end, overflow := addOverflow(offset, uint32(size))
	if overflow || end > uint32(len(pkt)) {
		return 0, ErrOutOfBounds
	}
	off := int(offset)
	switch size {
	case Byte:
		return uint32(pkt[off]), nil
	case Half:
		return uint32(pkt[off])<<8 | uint32(pkt[off+1]), nil
	case Word:
		return uint32(pkt[off])<<24 |
			uint32(pkt[off+1])<<16 |
			uint32(pkt[off+2])<<8 |
			uint32(pkt[off+3]), nil
	default:
		return 0, ErrMalformedProgram
	}
}
