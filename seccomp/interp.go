package seccomp

import "fmt"

// Context is the input a compiled program evaluates, mirroring
// seccomp_data.
type Context struct {
	NR   int32
	Arch uint32
	Args [numArgs]uint64
}

// maxSteps bounds interpreter execution so malformed programs terminate.
const maxSteps = 1 << 22

// wordAt reads the 32-bit word at a byte offset in the context, using the
// little-endian seccomp_data layout.
func wordAt(c *Context, off uint32) (uint32, error) {
	switch {
	case off == offNr:
		return uint32(c.NR), nil
	case off == offArch:
		return c.Arch, nil
	case off >= offArgs && off < offArgs+8*numArgs && off%4 == 0:
		i := (off - offArgs) / 8
		if (off-offArgs)%8 == 0 {
			return uint32(c.Args[i]), nil
		}
		return uint32(c.Args[i] >> 32), nil
	}
	return 0, fmt.Errorf("seccomp: interpreter: invalid load offset %d", off)
}

// Run executes a classic BPF program against ctx and returns its verdict.
// It is written directly from the BPF instruction semantics, independently
// of the compiler, so tests can cross-check the two.
//
// Jump semantics: after executing the instruction at pc, control continues
// at pc+1; jumps add their offset (jt/jf, or k for JA) to pc+1.
func Run(prog []Instruction, ctx *Context) (uint32, error) {
	var a uint32
	pc := 0
	for step := 0; step < maxSteps; step++ {
		if pc < 0 || pc >= len(prog) {
			return 0, fmt.Errorf("seccomp: interpreter: pc %d out of bounds (len %d)", pc, len(prog))
		}
		in := prog[pc]
		switch in.Code {
		case opLdWAbs:
			w, err := wordAt(ctx, in.K)
			if err != nil {
				return 0, err
			}
			a = w
			pc++
		case opAndK:
			a &= in.K
			pc++
		case opJeqK:
			if a == in.K {
				pc += 1 + int(in.JT)
			} else {
				pc += 1 + int(in.JF)
			}
		case opJgtK:
			if a > in.K {
				pc += 1 + int(in.JT)
			} else {
				pc += 1 + int(in.JF)
			}
		case opJgeK:
			if a >= in.K {
				pc += 1 + int(in.JT)
			} else {
				pc += 1 + int(in.JF)
			}
		case opJa:
			pc += 1 + int(in.K)
		case opRetK:
			return in.K, nil
		case opRetA:
			return a, nil
		default:
			return 0, fmt.Errorf("seccomp: interpreter: unknown opcode %#x at pc %d", in.Code, pc)
		}
	}
	return 0, fmt.Errorf("seccomp: interpreter: step limit %d exceeded", maxSteps)
}
