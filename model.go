// Package stlf models a small out-of-order processor with speculative
// store-to-load forwarding (STLF), bounded nested branch misprediction, and
// read-only microarchitectural cache observations.
//
// The package is deliberately self contained.  It is intended to be imported by
// a security verification host which wants to search for speculative leaks and
// check proposed speculation-barrier patches.
package stlf

import "fmt"

const (
	// AddressBits is the number of physical-address bits in the model.
	AddressBits = 12
	// AddressSpace is the number of modelled physical addresses.
	AddressSpace = 1 << AddressBits
	// ValueBits is the width of bytes and registers.
	ValueBits = 8
	// RegisterCount is the number of general purpose registers.
	RegisterCount = 16
	// StoreBufferSize is the maximum number of uncommitted stores.
	StoreBufferSize = 4
	// MaxStoreBuffer is an alias kept for hosts that use the longer spelling.
	MaxStoreBuffer = StoreBufferSize
	// MaxInstructions is the maximum accepted program length.
	MaxInstructions = 20
	// MaxMispredictDepth is the deepest nested wrong-path expansion.
	MaxMispredictDepth = 2
	// MaxSpeculationDepth is an alias for MaxMispredictDepth.
	MaxSpeculationDepth = MaxMispredictDepth
	// ForwardingWindow is the number of cycles a partial STLF value remains
	// visible.  A load at cycle c can be observed through cycle c+4.
	ForwardingWindow = 4
	// ObservationWays is the number of read-only observation classes.
	ObservationWays = 16
	// LowAddressBits is the width of the speculative alias key.
	LowAddressBits = 6
	// MaxPatchedInstructions bounds an instrumented program.  The original
	// program is still limited to MaxInstructions.
	MaxPatchedInstructions = 32
)

// Op is a modelled instruction opcode.
type Op uint8

const (
	OpStore Op = iota
	OpLoad
	OpXor
	OpCmpBranch
	OpProbe
	OpBarrier
)

func (o Op) String() string {
	switch o {
	case OpStore:
		return "store"
	case OpLoad:
		return "load"
	case OpXor:
		return "xor"
	case OpCmpBranch:
		return "cmp_branch"
	case OpProbe:
		return "probe"
	case OpBarrier:
		return "barrier"
	default:
		return fmt.Sprintf("op(%d)", uint8(o))
	}
}

// AddrMode selects how an effective load/store address is formed.
type AddrMode uint8

const (
	// AddrImmediate uses Instruction.Addr directly.
	AddrImmediate AddrMode = iota
	// AddrRegister uses Registers[Instruction.Index].
	AddrRegister
	// AddrIndexed uses Address + Registers[Instruction.Index], modulo 4096.
	AddrIndexed
)

// Instruction is one modelled instruction.  A branch target is an instruction
// index in [0, len(code)]; target == len(code) means "fall off the end".
type Instruction struct {
	Op Op

	// Reg is the primary register.  It is the destination of load/xor, the
	// probed register of probe, the left operand of cmp_branch, and the value
	// source of store (unless StoreImm is true).
	Reg uint8
	// Src is the second register operand for xor/cmp_branch when SrcIsReg.
	Src uint8
	// Imm is the immediate value operand for xor/cmp_branch and immediate
	// stores.
	Imm uint8

	// Addr and Index describe the effective address of load/store.
	Addr     uint16
	AddrMode AddrMode
	Index    uint8

	SrcIsReg bool // xor/cmp_branch second operand comes from Src
	StoreImm bool // store writes Imm instead of Registers[Reg]

	// Target and PredictedTaken are used by cmp_branch.
	Target         int
	PredictedTaken bool
}

// Store builds a register store to a fixed 12-bit address.
func Store(addr uint16, valueReg uint8) Instruction {
	return Instruction{Op: OpStore, Reg: valueReg, Addr: addr & 0x0fff, AddrMode: AddrImmediate}
}

// StoreImm builds an immediate store to a fixed 12-bit address.
func StoreImm(addr uint16, value uint8) Instruction {
	return Instruction{Op: OpStore, Imm: value, StoreImm: true, Addr: addr & 0x0fff, AddrMode: AddrImmediate}
}

// StoreReg builds a register store whose address is in addressReg.
func StoreReg(addressReg, valueReg uint8) Instruction {
	return Instruction{Op: OpStore, Reg: valueReg, AddrMode: AddrRegister, Index: addressReg}
}

// StoreIndexed builds a register store at base+Registers[indexReg].
func StoreIndexed(base uint16, indexReg, valueReg uint8) Instruction {
	return Instruction{Op: OpStore, Reg: valueReg, Addr: base & 0x0fff, AddrMode: AddrIndexed, Index: indexReg}
}

// Load builds a load from a fixed 12-bit address.
func Load(dst uint8, addr uint16) Instruction {
	return Instruction{Op: OpLoad, Reg: dst, Addr: addr & 0x0fff, AddrMode: AddrImmediate}
}

// LoadReg builds a load from the address held in addressReg.
func LoadReg(dst, addressReg uint8) Instruction {
	return Instruction{Op: OpLoad, Reg: dst, AddrMode: AddrRegister, Index: addressReg}
}

// LoadIndexed builds a load from base+Registers[indexReg].
func LoadIndexed(dst uint8, base uint16, indexReg uint8) Instruction {
	return Instruction{Op: OpLoad, Reg: dst, Addr: base & 0x0fff, AddrMode: AddrIndexed, Index: indexReg}
}

// XorImm builds reg ^= imm.
func XorImm(reg, imm uint8) Instruction {
	return Instruction{Op: OpXor, Reg: reg, Imm: imm}
}

// XorReg builds dst ^= src.
func XorReg(dst, src uint8) Instruction {
	return Instruction{Op: OpXor, Reg: dst, Src: src, SrcIsReg: true}
}

// CmpBranchImm branches to target when reg == imm.
func CmpBranchImm(reg, imm uint8, target int, predictedTaken bool) Instruction {
	return Instruction{Op: OpCmpBranch, Reg: reg, Imm: imm, Target: target, PredictedTaken: predictedTaken}
}

// CmpBranchReg branches to target when reg == src.
func CmpBranchReg(reg, src uint8, target int, predictedTaken bool) Instruction {
	return Instruction{Op: OpCmpBranch, Reg: reg, Src: src, SrcIsReg: true, Target: target, PredictedTaken: predictedTaken}
}

// Probe builds a read-only observation of register reg & 0x0f.
func Probe(reg uint8) Instruction {
	return Instruction{Op: OpProbe, Reg: reg}
}

// Barrier builds a speculation barrier.
func Barrier() Instruction {
	return Instruction{Op: OpBarrier}
}

// Trace is an ordered sequence of observation-class indices in [0, 15].
type Trace []uint8

// WayMask returns the set of observation classes touched by t.
func (t Trace) WayMask() uint16 {
	var m uint16
	for _, way := range t {
		m |= uint16(1) << (way & 0x0f)
	}
	return m
}

// Equal reports whether two observation sequences are word-for-word equal.
func (t Trace) Equal(other Trace) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		if (t[i] & 0x0f) != (other[i] & 0x0f) {
			return false
		}
	}
	return true
}

// String renders observations as hexadecimal nibbles.
func (t Trace) String() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 0, len(t)+2)
	b = append(b, '[')
	for i, way := range t {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, hex[way&0x0f])
	}
	b = append(b, ']')
	return string(b)
}

// Host fixes the public, host-dependent part of the scenario.
type Host struct {
	// High6 is the fixed upper six bits of the modelled physical address.
	High6 uint8
	// SecretSlot is the memory byte overwritten by the secret input.
	SecretSlot uint16
	// PublicLowReg receives Input.PublicLow6 at reset.
	PublicLowReg uint8
	// HasSecretReg, when true, also places the secret in SecretReg.
	HasSecretReg bool
	SecretReg    uint8
	// InitialRegs and PublicMemory are the public reset state.
	InitialRegs  [RegisterCount]uint8
	PublicMemory [AddressSpace]uint8
}

// Address joins the host-fixed high bits with a public six-bit address part.
func (h Host) Address(low6 uint8) uint16 {
	return (uint16(h.High6&0x3f) << LowAddressBits) | uint16(low6&0x3f)
}

// LowAddress returns the low six bits used as the speculative alias key.
func LowAddress(addr uint16) uint8 { return uint8(addr & 0x3f) }

// HighAddress returns the high six bits of a 12-bit address.
func HighAddress(addr uint16) uint8 { return uint8((addr >> LowAddressBits) & 0x3f) }

// Input is the public-low/secret split.  Two inputs are a valid dual-input
// witness only when PublicLow6 is equal and Secret differs.
type Input struct {
	PublicLow6 uint8
	Secret     uint8
}

// Scenario bundles a host and code.
type Scenario struct {
	Host Host
	Code []Instruction
}

// StoreEntry is one uncommitted store-buffer item.
type StoreEntry struct {
	Addr  uint16
	Value uint8
	PC    int
	Seq   uint64
	Spec  bool
}

// StoreValue is the architecturally relevant part of a store-buffer entry.
type StoreValue struct {
	Addr  uint16
	Value uint8
}

// MachineState is the full architectural state used by the reference model.
type MachineState struct {
	Regs      [RegisterCount]uint8
	Memory    [AddressSpace]uint8
	StoreBuf  []StoreEntry
	PC        int
	Cycle     int
	ArchTrace Trace
	Halted    bool
	Truncated bool
}

// Clone makes a deep copy.
func (s MachineState) Clone() MachineState {
	c := s
	c.StoreBuf = append([]StoreEntry(nil), s.StoreBuf...)
	c.ArchTrace = append(Trace(nil), s.ArchTrace...)
	return c
}

// StoreValues returns the address/value pairs currently buffered.
func (s MachineState) StoreValues() []StoreValue {
	out := make([]StoreValue, len(s.StoreBuf))
	for i, e := range s.StoreBuf {
		out[i] = StoreValue{Addr: e.Addr, Value: e.Value}
	}
	return out
}

// MemHash is a fast deterministic digest of the 4096-byte memory image.
func (s MachineState) MemHash() uint64 { return MemoryHash(&s.Memory) }

// MemoryHash is a fast deterministic digest of a memory image.
func MemoryHash(mem *[AddressSpace]uint8) uint64 {
	// FNV-1a, inlined to avoid allocations on the hot path.
	h := uint64(14695981039346656037)
	for _, b := range mem {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

// ProbeEvent records one read-only observation.
type ProbeEvent struct {
	PC    int
	Cycle int
	Way   uint8
	Value uint8
	Arch  bool
}

// ForwardEvent records a store-to-load forwarding decision.
type ForwardEvent struct {
	LoadPC      int
	Cycle       int
	LoadAddr    uint16
	StoreAddr   uint16
	Low6Match   bool
	FullMatch   bool
	Forwarded   bool
	Speculative bool
	Deadline    int // last cycle at which the partial forward is visible
}

// Execution is the result of one input on one code image.
type Execution struct {
	Input Input

	ArchTrace Trace
	SpecTrace Trace
	// MicroTrace is the read-only sequence that survives rollback.  In this
	// single-window model it is the speculative observation sequence.
	MicroTrace Trace

	// Probes contains committed probe events first, followed by speculative
	// window events.  The two traces are separate simulations, so Cycle values
	// are not a global ordering.
	Probes   []ProbeEvent
	Forwards []ForwardEvent

	Steps              int
	ArchSteps          int
	Mispredictions     int
	MaxMispredictDepth int
	MaxStoreOccupancy  int
	PartialForward     bool
	RolledBack         bool
	RollbackReason     string
	Completed          bool
	Truncated          bool

	// Final* is the committed architectural state when produced by Execute or
	// RunArchitectural.  Direct RunSpeculative results expose the shadow state
	// for diagnostics; Execute is the authority for post-rollback state.
	FinalRegs     [RegisterCount]uint8
	FinalMemHash  uint64
	FinalPC       int
	FinalStoreBuf []StoreValue
}

// FirstDivergence returns the first index at which two traces differ, or -1
// when they are equal.  A strict prefix counts as a divergence at the shorter
// length.
func FirstDivergence(a, b Trace) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if (a[i] & 0x0f) != (b[i] & 0x0f) {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// Validate checks a host and program against the model limits.
func Validate(host Host, code []Instruction) error {
	return validateCode(host, code, MaxInstructions)
}

// ValidatePatched checks a host and an instrumented program.  Patched programs
// may exceed MaxInstructions by the inserted barriers, but remain bounded.
func ValidatePatched(host Host, code []Instruction) error {
	return validateCode(host, code, MaxPatchedInstructions)
}

func validateCode(host Host, code []Instruction, maxLen int) error {
	if host.High6 > 0x3f {
		return fmt.Errorf("high6 %#x exceeds six bits", host.High6)
	}
	if host.SecretSlot >= AddressSpace {
		return fmt.Errorf("secret slot %#x exceeds 12 bits", host.SecretSlot)
	}
	if host.PublicLowReg >= RegisterCount {
		return fmt.Errorf("public-low register r%d out of range", host.PublicLowReg)
	}
	if host.HasSecretReg && host.SecretReg >= RegisterCount {
		return fmt.Errorf("secret register r%d out of range", host.SecretReg)
	}
	if len(code) > maxLen {
		return fmt.Errorf("program has %d instructions; maximum is %d", len(code), maxLen)
	}
	for i, ins := range code {
		if ins.Reg >= RegisterCount {
			return fmt.Errorf("instruction %d: register r%d out of range", i, ins.Reg)
		}
		if ins.Src >= RegisterCount {
			return fmt.Errorf("instruction %d: source register r%d out of range", i, ins.Src)
		}
		if ins.Index >= RegisterCount {
			return fmt.Errorf("instruction %d: address-index register r%d out of range", i, ins.Index)
		}
		if ins.Addr >= AddressSpace {
			return fmt.Errorf("instruction %d: address %#x exceeds 12 bits", i, ins.Addr)
		}
		if ins.AddrMode > AddrIndexed {
			return fmt.Errorf("instruction %d: invalid address mode %d", i, ins.AddrMode)
		}
		if ins.Op == OpCmpBranch {
			if ins.Target < 0 || ins.Target > len(code) {
				return fmt.Errorf("instruction %d: branch target %d out of range [0,%d]", i, ins.Target, len(code))
			}
		}
		if ins.Op > OpBarrier {
			return fmt.Errorf("instruction %d: unknown opcode %d", i, ins.Op)
		}
	}
	return nil
}
