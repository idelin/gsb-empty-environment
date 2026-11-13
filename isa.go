package netsec

import (
	"errors"
	"fmt"
)

const (
	NumRegisters = 8
	MemorySize   = 256
	MaxCodeLen   = 64
	MaxSteps     = 64
	MaxSpecSteps = 8
	ProbeBlock   = 16
)

// Op is an opcode in the miniature instruction set.
type Op uint8

const (
	OpConst Op = iota
	OpAdd
	OpAddImm
	OpXor
	OpXorImm
	OpLoad
	OpProbe
	OpJeq
	OpBarrier
	OpHalt
)

func (op Op) String() string {
	switch op {
	case OpConst:
		return "const"
	case OpAdd:
		return "add"
	case OpAddImm:
		return "addi"
	case OpXor:
		return "xor"
	case OpXorImm:
		return "xori"
	case OpLoad:
		return "load"
	case OpProbe:
		return "probe"
	case OpJeq:
		return "jeq"
	case OpBarrier:
		return "barrier"
	case OpHalt:
		return "halt"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// Instruction is one instruction. Dst and Src are register numbers. Imm is
// used by the immediate forms, and Target is used by OpJeq. A Target equal to
// len(Program.Code) means "fall off the program" (halt).
type Instruction struct {
	Op     Op
	Dst    uint8
	Src    uint8
	Imm    uint8
	Target int
}

func Const(dst, imm uint8) Instruction {
	return Instruction{Op: OpConst, Dst: dst, Imm: imm}
}

func Add(dst, src uint8) Instruction {
	return Instruction{Op: OpAdd, Dst: dst, Src: src}
}

func AddImm(dst, imm uint8) Instruction {
	return Instruction{Op: OpAddImm, Dst: dst, Imm: imm}
}

func Xor(dst, src uint8) Instruction {
	return Instruction{Op: OpXor, Dst: dst, Src: src}
}

func XorImm(dst, imm uint8) Instruction {
	return Instruction{Op: OpXorImm, Dst: dst, Imm: imm}
}

func Load(dst, addressReg uint8) Instruction {
	return Instruction{Op: OpLoad, Dst: dst, Src: addressReg}
}

func Probe(addressReg uint8) Instruction {
	return Instruction{Op: OpProbe, Src: addressReg}
}

func JumpEq(a, b uint8, target int) Instruction {
	return Instruction{Op: OpJeq, Dst: a, Src: b, Target: target}
}

func Barrier() Instruction {
	return Instruction{Op: OpBarrier}
}

func Halt() Instruction {
	return Instruction{Op: OpHalt}
}

// Space says where an input byte lives at the start of a run.
type Space uint8

const (
	SpaceRegister Space = iota
	SpaceMemory
)

// InputKind distinguishes public and secret input bytes.
type InputKind uint8

const (
	PublicInput InputKind = iota
	SecretInput
)

// InputSlot describes one initial input byte. Public slots are filled from
// Assignment.Public in the order in which they appear in Program.Inputs.
type InputSlot struct {
	Kind  InputKind
	Space Space
	Index uint8
}

func PublicRegister(index uint8) InputSlot {
	return InputSlot{Kind: PublicInput, Space: SpaceRegister, Index: index}
}

func SecretRegister(index uint8) InputSlot {
	return InputSlot{Kind: SecretInput, Space: SpaceRegister, Index: index}
}

func PublicMemory(index uint8) InputSlot {
	return InputSlot{Kind: PublicInput, Space: SpaceMemory, Index: index}
}

func SecretMemory(index uint8) InputSlot {
	return InputSlot{Kind: SecretInput, Space: SpaceMemory, Index: index}
}

// Program couples code with the locations of the public and secret input
// bytes. All bytes not named by an input slot start at zero.
type Program struct {
	Code   []Instruction
	Inputs []InputSlot
}

// Assignment supplies values for the slots in Program.Inputs. Public contains
// one byte per public slot, in declaration order. There is exactly one secret
// byte.
type Assignment struct {
	Public []uint8
	Secret uint8
}

// State is the architectural state of the machine.
type State struct {
	PC     int
	Regs   [NumRegisters]uint8
	Mem    [MemorySize]uint8
	Halted bool
	Steps  int
}

var (
	ErrInvalidProgram         = errors.New("netsec: invalid program")
	ErrInvalidInput           = errors.New("netsec: invalid input assignment")
	ErrMispredictionUnreached = errors.New("netsec: requested misprediction was not reached")
	ErrDegenerateBranch       = errors.New("netsec: misprediction target equals the correct successor")
)

// Validate checks the static limits and input layout.
func (p Program) Validate() error {
	if len(p.Code) == 0 {
		return fmt.Errorf("%w: empty code", ErrInvalidProgram)
	}
	if len(p.Code) > MaxCodeLen {
		return fmt.Errorf("%w: %d instructions exceed limit %d", ErrInvalidProgram, len(p.Code), MaxCodeLen)
	}
	for pc, in := range p.Code {
		switch in.Op {
		case OpConst, OpAddImm, OpXorImm:
			if in.Dst >= NumRegisters {
				return fmt.Errorf("%w: instruction %d has register %d", ErrInvalidProgram, pc, in.Dst)
			}
		case OpAdd, OpXor:
			if in.Dst >= NumRegisters || in.Src >= NumRegisters {
				return fmt.Errorf("%w: instruction %d has register %d or %d", ErrInvalidProgram, pc, in.Dst, in.Src)
			}
		case OpLoad, OpProbe:
			if in.Dst >= NumRegisters || in.Src >= NumRegisters {
				return fmt.Errorf("%w: instruction %d has register %d or %d", ErrInvalidProgram, pc, in.Dst, in.Src)
			}
		case OpJeq:
			if in.Dst >= NumRegisters || in.Src >= NumRegisters {
				return fmt.Errorf("%w: instruction %d has register %d or %d", ErrInvalidProgram, pc, in.Dst, in.Src)
			}
			if in.Target < 0 || in.Target > len(p.Code) {
				return fmt.Errorf("%w: instruction %d has target %d outside [0,%d]", ErrInvalidProgram, pc, in.Target, len(p.Code))
			}
		case OpBarrier, OpHalt:
		default:
			return fmt.Errorf("%w: instruction %d has unknown opcode %d", ErrInvalidProgram, pc, uint8(in.Op))
		}
	}

	public, secret := 0, 0
	seen := make(map[[2]uint8]bool)
	for i, slot := range p.Inputs {
		switch slot.Kind {
		case PublicInput:
			public++
			if public > 2 {
				return fmt.Errorf("%w: more than two public input slots", ErrInvalidProgram)
			}
		case SecretInput:
			secret++
			if secret > 1 {
				return fmt.Errorf("%w: more than one secret input slot", ErrInvalidProgram)
			}
		default:
			return fmt.Errorf("%w: input slot %d has unknown kind %d", ErrInvalidProgram, i, uint8(slot.Kind))
		}
		switch slot.Space {
		case SpaceRegister:
			if slot.Index >= NumRegisters {
				return fmt.Errorf("%w: input slot %d names register %d", ErrInvalidProgram, i, slot.Index)
			}
		case SpaceMemory:
			// Every uint8 index is a valid byte address.
		default:
			return fmt.Errorf("%w: input slot %d has unknown space %d", ErrInvalidProgram, i, uint8(slot.Space))
		}
		key := [2]uint8{uint8(slot.Space), slot.Index}
		if seen[key] {
			return fmt.Errorf("%w: input slots overlap at space %d index %d", ErrInvalidProgram, slot.Space, slot.Index)
		}
		seen[key] = true
	}
	if secret != 1 {
		return fmt.Errorf("%w: expected exactly one secret input slot, got %d", ErrInvalidProgram, secret)
	}
	return nil
}

// PublicSlots returns the public slots in declaration order.
func (p Program) PublicSlots() []InputSlot {
	var out []InputSlot
	for _, slot := range p.Inputs {
		if slot.Kind == PublicInput {
			out = append(out, slot)
		}
	}
	return out
}

// SecretSlot returns the single secret slot. Programs must have one.
func (p Program) SecretSlot() (InputSlot, bool) {
	for _, slot := range p.Inputs {
		if slot.Kind == SecretInput {
			return slot, true
		}
	}
	return InputSlot{}, false
}

// InitialState builds the initial architectural state for an assignment.
func (p Program) InitialState(a Assignment) (State, error) {
	if err := p.Validate(); err != nil {
		return State{}, err
	}
	return p.initialState(a)
}

func (p Program) initialState(a Assignment) (State, error) {
	public := p.PublicSlots()
	if len(a.Public) != len(public) {
		return State{}, fmt.Errorf("%w: got %d public values, want %d", ErrInvalidInput, len(a.Public), len(public))
	}
	st := State{}
	for i, slot := range public {
		if slot.Space == SpaceRegister {
			st.Regs[slot.Index] = a.Public[i]
		} else {
			st.Mem[slot.Index] = a.Public[i]
		}
	}
	secret, ok := p.SecretSlot()
	if !ok {
		return State{}, fmt.Errorf("%w: missing secret slot", ErrInvalidProgram)
	}
	if secret.Space == SpaceRegister {
		st.Regs[secret.Index] = a.Secret
	} else {
		st.Mem[secret.Index] = a.Secret
	}
	return st, nil
}

func clonePublic(in []uint8) []uint8 {
	if len(in) == 0 {
		return []uint8{}
	}
	out := make([]uint8, len(in))
	copy(out, in)
	return out
}
