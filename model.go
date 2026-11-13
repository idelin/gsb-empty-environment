package netsec

import (
	"fmt"
)

const (
	// AddrBits is the architectural address width.
	AddrBits = 12
	// ValueBits is the architectural data width.
	ValueBits = 8
	// MaxStoreBuffer is the hard upper bound on buffered stores.
	MaxStoreBuffer = 4
	// MaxInstructions is the hard upper bound on program length.
	MaxInstructions = 16
	// ProbeGroups is the number of observable groups (the low four bits).
	ProbeGroups = 1 << 4
	// RegisterCount is the number of eight-bit registers.
	RegisterCount = 16
	// MaxResolveDelay is the largest number of cycles before a forwarded
	// load is checked and, if necessary, rolled back.
	MaxResolveDelay = 4
	// NoSlot marks an address whose low six bits are fixed.
	NoSlot int8 = -1
)

// Addr is a 12-bit address.
type Addr uint16

// Value is an eight-bit value.
type Value uint8

// Reg identifies one of the 16 registers.
type Reg uint8

// AddrSpec describes an address before the attacker-selected low six bits are
// substituted. High6 is fixed by the host. Slot is NoSlot for a fixed address,
// or 0/1 for one of the two public address slots controlled by the attacker.
type AddrSpec struct {
	High6 uint8
	Low6  uint8
	Slot  int8
}

// FixedAddr returns a fully fixed address specification.
func FixedAddr(a Addr) AddrSpec {
	return AddrSpec{
		High6: uint8((a >> 6) & 0x3f),
		Low6:  uint8(a & 0x3f),
		Slot:  NoSlot,
	}
}

// SlotAddr returns a public address whose low six bits come from slot.
// The high six bits and the host-provided default low bits are fixed.
func SlotAddr(slot int, high6, low6 uint8) AddrSpec {
	if slot < 0 || slot > 1 {
		panic("netsec: address slot must be 0 or 1")
	}
	return AddrSpec{High6: high6, Low6: low6, Slot: int8(slot)}
}

// Valid reports whether the host-fixed portions of an address are in range.
func (a AddrSpec) Valid() error {
	if a.High6 > 0x3f {
		return fmt.Errorf("high six bits %#x out of range", a.High6)
	}
	if a.Low6 > 0x3f {
		return fmt.Errorf("low six bits %#x out of range", a.Low6)
	}
	if a.Slot < NoSlot || a.Slot > 1 {
		return fmt.Errorf("invalid address slot %d", a.Slot)
	}
	return nil
}

// Resolve substitutes the public input's low six bits for a slot address.
func (a AddrSpec) Resolve(low6 [2]uint8) (Addr, error) {
	if err := a.Valid(); err != nil {
		return 0, err
	}
	low := a.Low6
	if a.Slot != NoSlot {
		low = low6[a.Slot]
		if low > 0x3f {
			return 0, fmt.Errorf("public low six bits %#x out of range", low)
		}
	}
	return Addr(uint16(a.High6)<<6 | uint16(low)), nil
}

// ValueSpec is either a constant or the designated secret value.
type ValueSpec struct {
	Const    Value
	IsSecret bool
}

// ConstValue returns a public constant.
func ConstValue(v Value) ValueSpec { return ValueSpec{Const: v} }

// SecretValue returns the single secret operand.
func SecretValue() ValueSpec { return ValueSpec{IsSecret: true} }

// Resolve returns the concrete value for a run.
func (v ValueSpec) Resolve(secret Value) Value {
	if v.IsSecret {
		return secret
	}
	return v.Const
}

// Kind identifies an instruction.
type Kind uint8

const (
	KindStore Kind = iota
	KindLoad
	KindXor
	KindProbe
	KindBarrier
)

func (k Kind) String() string {
	switch k {
	case KindStore:
		return "store"
	case KindLoad:
		return "load"
	case KindXor:
		return "xor"
	case KindProbe:
		return "probe"
	case KindBarrier:
		return "barrier"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

// Instruction is one item in the bounded pipeline language. Reg is the
// destination of a load/xor or the source of a probe. Addr and Value are used
// by stores; Addr is used by loads; Imm is used by xors.
type Instruction struct {
	Kind  Kind
	Addr  AddrSpec
	Value ValueSpec
	Reg   Reg
	Imm   Value
}

// Store constructs a store of a value specification.
func Store(addr AddrSpec, value ValueSpec) Instruction {
	return Instruction{Kind: KindStore, Addr: addr, Value: value}
}

// Load constructs a load into dst.
func Load(addr AddrSpec, dst Reg) Instruction {
	return Instruction{Kind: KindLoad, Addr: addr, Reg: dst}
}

// Xor constructs an xor of a constant into dst.
func Xor(dst Reg, imm Value) Instruction {
	return Instruction{Kind: KindXor, Reg: dst, Imm: imm}
}

// Probe constructs an architectural/speculative observation of src's low four
// bits.
func Probe(src Reg) Instruction {
	return Instruction{Kind: KindProbe, Reg: src}
}

// Barrier constructs a serializing barrier. It resolves every pending load
// before the following instruction is issued.
func Barrier() Instruction {
	return Instruction{Kind: KindBarrier}
}

// Valid checks the fields used by an instruction.
func (in Instruction) Valid() error {
	switch in.Kind {
	case KindStore:
		if err := in.Addr.Valid(); err != nil {
			return fmt.Errorf("store address: %w", err)
		}
	case KindLoad:
		if err := in.Addr.Valid(); err != nil {
			return fmt.Errorf("load address: %w", err)
		}
		if in.Reg >= RegisterCount {
			return fmt.Errorf("load destination register %d out of range", in.Reg)
		}
	case KindXor:
		if in.Reg >= RegisterCount {
			return fmt.Errorf("xor destination register %d out of range", in.Reg)
		}
	case KindProbe:
		if in.Reg >= RegisterCount {
			return fmt.Errorf("probe register %d out of range", in.Reg)
		}
	case KindBarrier:
		return nil
	default:
		return fmt.Errorf("unknown instruction kind %d", in.Kind)
	}
	return nil
}

// Program is a straight-line instruction sequence of at most 16 instructions.
type Program []Instruction

// Validate checks all instruction fields and the program length.
func (p Program) Validate() error {
	if len(p) > MaxInstructions {
		return fmt.Errorf("program has %d instructions, maximum is %d", len(p), MaxInstructions)
	}
	for pc, in := range p {
		if err := in.Valid(); err != nil {
			return fmt.Errorf("instruction %d (%s): %w", pc, in.Kind, err)
		}
	}
	return nil
}

// SecretStoreCount returns the number of store instructions carrying the
// secret operand.
func (p Program) SecretStoreCount() int {
	n := 0
	for _, in := range p {
		if in.Kind == KindStore && in.Value.IsSecret {
			n++
		}
	}
	return n
}

// PublicInput is the secret-independent part of an experiment input.
type PublicInput struct {
	Low6   [2]uint8
	Memory map[Addr]Value
	Regs   [RegisterCount]Value
}

// Input is one concrete experiment input.
type Input struct {
	Secret Value
	PublicInput
}

func (p PublicInput) validate() error {
	for i, low := range p.Low6 {
		if low > 0x3f {
			return fmt.Errorf("public low six bits %d (%#x) out of range", i, low)
		}
	}
	for addr := range p.Memory {
		if addr > (1<<AddrBits)-1 {
			return fmt.Errorf("memory address %#x out of range", addr)
		}
	}
	return nil
}

// Validate checks addresses and attacker-controlled low bits.
func (in Input) Validate() error { return in.PublicInput.validate() }

func cloneMemory(in map[Addr]Value) map[Addr]Value {
	out := make(map[Addr]Value, len(in))
	for addr, value := range in {
		out[addr] = value
	}
	return out
}
