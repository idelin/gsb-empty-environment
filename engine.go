package stlf

import "fmt"

// RunOptions controls bounded execution.
type RunOptions struct {
	// MaxSteps is the maximum number of dynamic instructions.  Zero selects a
	// safe default which is large enough for all straight-line 20-instruction
	// programs while still bounding loops.
	MaxSteps int
}

func (o RunOptions) steps() int {
	if o.MaxSteps <= 0 {
		return 4*MaxInstructions + 64
	}
	return o.MaxSteps
}

func firstRunOptions(opts []RunOptions) RunOptions {
	if len(opts) == 0 {
		return RunOptions{}
	}
	return opts[0]
}

func initialState(host Host, in Input) MachineState {
	var st MachineState
	st.Regs = host.InitialRegs
	st.Memory = host.PublicMemory
	st.Regs[host.PublicLowReg&0x0f] = in.PublicLow6 & 0x3f
	if host.HasSecretReg {
		st.Regs[host.SecretReg&0x0f] = in.Secret
	}
	st.Memory[int(host.SecretSlot)&(AddressSpace-1)] = in.Secret
	st.PC = 0
	return st
}

func effectiveAddress(ins Instruction, regs *[RegisterCount]uint8) uint16 {
	switch ins.AddrMode {
	case AddrRegister:
		return uint16(regs[ins.Index&0x0f]) & (AddressSpace - 1)
	case AddrIndexed:
		return (ins.Addr + uint16(regs[ins.Index&0x0f])) & (AddressSpace - 1)
	default:
		return ins.Addr & (AddressSpace - 1)
	}
}

func flushOldestStore(st *MachineState) {
	if len(st.StoreBuf) == 0 {
		return
	}
	e := st.StoreBuf[0]
	st.Memory[int(e.Addr)&(AddressSpace-1)] = e.Value
	copy(st.StoreBuf, st.StoreBuf[1:])
	st.StoreBuf = st.StoreBuf[:len(st.StoreBuf)-1]
}

func pushStore(st *MachineState, e StoreEntry) {
	if len(st.StoreBuf) >= StoreBufferSize {
		flushOldestStore(st)
	}
	st.StoreBuf = append(st.StoreBuf, e)
}

func flushAllStores(st *MachineState) {
	for _, e := range st.StoreBuf {
		st.Memory[int(e.Addr)&(AddressSpace-1)] = e.Value
	}
	st.StoreBuf = nil
}

func architecturalLoad(st *MachineState, addr uint16) uint8 {
	addr &= AddressSpace - 1
	for i := len(st.StoreBuf) - 1; i >= 0; i-- {
		if st.StoreBuf[i].Addr == addr {
			return st.StoreBuf[i].Value
		}
	}
	return st.Memory[int(addr)]
}

func speculativeLoad(st *MachineState, addr uint16, cycle, pc int) (uint8, *ForwardEvent) {
	addr &= AddressSpace - 1
	for i := len(st.StoreBuf) - 1; i >= 0; i-- {
		e := st.StoreBuf[i]
		if (e.Addr & 0x3f) != (addr & 0x3f) {
			continue
		}
		ev := &ForwardEvent{
			LoadPC:      pc,
			Cycle:       cycle,
			LoadAddr:    addr,
			StoreAddr:   e.Addr,
			Low6Match:   true,
			FullMatch:   e.Addr == addr,
			Forwarded:   true,
			Speculative: true,
		}
		if !ev.FullMatch {
			ev.Deadline = cycle + ForwardingWindow
		}
		return e.Value, ev
	}
	return st.Memory[int(addr)], nil
}

func branchTaken(ins Instruction, regs *[RegisterCount]uint8) bool {
	rhs := uint8(ins.Imm)
	if ins.SrcIsReg {
		rhs = regs[ins.Src&0x0f]
	}
	return regs[ins.Reg&0x0f] == rhs
}

// RunArchitectural executes the precise, non-speculative reference semantics.
func RunArchitectural(host Host, code []Instruction, in Input, opts ...RunOptions) Execution {
	opt := firstRunOptions(opts)
	limit := opt.steps()
	st := initialState(host, in)
	ex := Execution{Input: in}
	var probes []ProbeEvent

	for steps := 0; ; steps++ {
		if st.PC < 0 || st.PC >= len(code) {
			st.Halted = true
			break
		}
		if steps >= limit {
			st.Truncated = true
			break
		}
		ex.ArchSteps = steps + 1
		st.Cycle++
		ins := code[st.PC]
		switch ins.Op {
		case OpStore:
			value := ins.Imm
			if !ins.StoreImm {
				value = st.Regs[ins.Reg&0x0f]
			}
			pushStore(&st, StoreEntry{
				Addr:  effectiveAddress(ins, &st.Regs),
				Value: value,
				PC:    st.PC,
				Seq:   uint64(st.Cycle),
			})
			if len(st.StoreBuf) > ex.MaxStoreOccupancy {
				ex.MaxStoreOccupancy = len(st.StoreBuf)
			}
			st.PC++
		case OpLoad:
			addr := effectiveAddress(ins, &st.Regs)
			st.Regs[ins.Reg&0x0f] = architecturalLoad(&st, addr)
			st.PC++
		case OpXor:
			rhs := ins.Imm
			if ins.SrcIsReg {
				rhs = st.Regs[ins.Src&0x0f]
			}
			st.Regs[ins.Reg&0x0f] ^= rhs
			st.PC++
		case OpCmpBranch:
			if branchTaken(ins, &st.Regs) {
				st.PC = ins.Target
			} else {
				st.PC++
			}
		case OpProbe:
			value := st.Regs[ins.Reg&0x0f]
			way := value & 0x0f
			ex.ArchTrace = append(ex.ArchTrace, way)
			probes = append(probes, ProbeEvent{PC: st.PC, Cycle: st.Cycle, Way: way, Value: value, Arch: true})
			st.PC++
		case OpBarrier:
			flushAllStores(&st)
			st.PC++
		}
	}
	if st.Halted {
		flushAllStores(&st)
	}

	ex.Steps = ex.ArchSteps
	ex.Completed = st.Halted && !st.Truncated
	ex.Truncated = st.Truncated
	ex.Probes = probes
	ex.FinalRegs = st.Regs
	ex.FinalMemHash = st.MemHash()
	ex.FinalPC = st.PC
	ex.FinalStoreBuf = st.StoreValues()
	return ex
}

// RunSpeculative executes one bounded speculative window.  Branch predictions
// are followed until the window is drained by a barrier, by the STLF
// four-cycle mismatch window, or by attempting a third nested misprediction.
func RunSpeculative(host Host, code []Instruction, in Input, opts ...RunOptions) Execution {
	opt := firstRunOptions(opts)
	limit := opt.steps()
	st := initialState(host, in)
	ex := Execution{Input: in}
	var probes []ProbeEvent
	depth := 0
	badDeadline := -1

	for steps := 0; ; steps++ {
		if st.PC < 0 || st.PC >= len(code) {
			ex.Completed = true
			break
		}
		if steps >= limit {
			st.Truncated = true
			ex.Truncated = true
			ex.RollbackReason = "step-limit"
			break
		}
		// The partial-forward value is visible through cycle load+4.
		if badDeadline >= 0 && st.Cycle+1 > badDeadline {
			ex.RolledBack = true
			ex.RollbackReason = "stlf-window"
			break
		}
		ex.Steps = steps + 1
		st.Cycle++
		ins := code[st.PC]
		switch ins.Op {
		case OpStore:
			value := ins.Imm
			if !ins.StoreImm {
				value = st.Regs[ins.Reg&0x0f]
			}
			pushStore(&st, StoreEntry{
				Addr:  effectiveAddress(ins, &st.Regs),
				Value: value,
				PC:    st.PC,
				Seq:   uint64(st.Cycle),
				Spec:  true,
			})
			if len(st.StoreBuf) > ex.MaxStoreOccupancy {
				ex.MaxStoreOccupancy = len(st.StoreBuf)
			}
			st.PC++
		case OpLoad:
			addr := effectiveAddress(ins, &st.Regs)
			value, fwd := speculativeLoad(&st, addr, st.Cycle, st.PC)
			if fwd != nil {
				ex.Forwards = append(ex.Forwards, *fwd)
				if fwd.Low6Match && !fwd.FullMatch {
					ex.PartialForward = true
					if badDeadline < 0 || fwd.Deadline < badDeadline {
						badDeadline = fwd.Deadline
					}
				}
			}
			st.Regs[ins.Reg&0x0f] = value
			st.PC++
		case OpXor:
			rhs := ins.Imm
			if ins.SrcIsReg {
				rhs = st.Regs[ins.Src&0x0f]
			}
			st.Regs[ins.Reg&0x0f] ^= rhs
			st.PC++
		case OpCmpBranch:
			actual := branchTaken(ins, &st.Regs)
			if actual != ins.PredictedTaken {
				ex.Mispredictions++
				if depth >= MaxMispredictDepth {
					ex.RolledBack = true
					ex.RollbackReason = "mispredict-depth"
					goto done
				}
				depth++
				if depth > ex.MaxMispredictDepth {
					ex.MaxMispredictDepth = depth
				}
			}
			if ins.PredictedTaken {
				st.PC = ins.Target
			} else {
				st.PC++
			}
		case OpProbe:
			value := st.Regs[ins.Reg&0x0f]
			way := value & 0x0f
			ex.SpecTrace = append(ex.SpecTrace, way)
			probes = append(probes, ProbeEvent{PC: st.PC, Cycle: st.Cycle, Way: way, Value: value})
			st.PC++
		case OpBarrier:
			flushAllStores(&st)
			ex.RolledBack = true
			ex.RollbackReason = "barrier"
			goto done
		}
	}

done:
	ex.MicroTrace = append(Trace(nil), ex.SpecTrace...)
	ex.Probes = probes
	ex.FinalRegs = st.Regs
	ex.FinalMemHash = st.MemHash()
	ex.FinalPC = st.PC
	ex.FinalStoreBuf = st.StoreValues()
	return ex
}

// Execute returns both the precise architectural execution and the bounded
// speculative-window execution for one input.
func Execute(host Host, code []Instruction, in Input, opts ...RunOptions) Execution {
	opt := firstRunOptions(opts)
	arch := RunArchitectural(host, code, in, opt)
	spec := RunSpeculative(host, code, in, opt)

	// Authoritative final state is the architecture-visible state.
	spec.ArchTrace = arch.ArchTrace
	spec.ArchSteps = arch.ArchSteps
	spec.FinalRegs = arch.FinalRegs
	spec.FinalMemHash = arch.FinalMemHash
	spec.FinalPC = arch.FinalPC
	spec.FinalStoreBuf = arch.FinalStoreBuf
	if arch.Truncated {
		spec.Truncated = true
	}
	spec.Probes = append(append([]ProbeEvent(nil), arch.Probes...), spec.Probes...)
	return spec
}

// ReferenceRun returns the complete architectural reference state, including
// registers, memory, store buffer, PC, and committed probe trace.
func ReferenceRun(host Host, code []Instruction, in Input, opts ...RunOptions) (MachineState, error) {
	if err := ValidatePatched(host, code); err != nil {
		return MachineState{}, err
	}
	opt := firstRunOptions(opts)
	limit := opt.steps()
	st := initialState(host, in)

	for steps := 0; ; steps++ {
		if st.PC < 0 || st.PC >= len(code) {
			st.Halted = true
			flushAllStores(&st)
			return st, nil
		}
		if steps >= limit {
			st.Truncated = true
			return st, fmt.Errorf("reference execution exceeded %d steps", limit)
		}
		st.Cycle++
		ins := code[st.PC]
		switch ins.Op {
		case OpStore:
			value := ins.Imm
			if !ins.StoreImm {
				value = st.Regs[ins.Reg&0x0f]
			}
			pushStore(&st, StoreEntry{Addr: effectiveAddress(ins, &st.Regs), Value: value, PC: st.PC, Seq: uint64(st.Cycle)})
			st.PC++
		case OpLoad:
			st.Regs[ins.Reg&0x0f] = architecturalLoad(&st, effectiveAddress(ins, &st.Regs))
			st.PC++
		case OpXor:
			rhs := ins.Imm
			if ins.SrcIsReg {
				rhs = st.Regs[ins.Src&0x0f]
			}
			st.Regs[ins.Reg&0x0f] ^= rhs
			st.PC++
		case OpCmpBranch:
			if branchTaken(ins, &st.Regs) {
				st.PC = ins.Target
			} else {
				st.PC++
			}
		case OpProbe:
			st.ArchTrace = append(st.ArchTrace, st.Regs[ins.Reg&0x0f]&0x0f)
			st.PC++
		case OpBarrier:
			flushAllStores(&st)
			st.PC++
		}
	}
}

// StatesEquivalent compares all architecture-visible final state.  Barrier
// insertion may change instruction indices and cycle counts, so those two
// fields are intentionally excluded.
func StatesEquivalent(a, b MachineState, codeA, codeB []Instruction) bool {
	if a.Regs != b.Regs {
		return false
	}
	if a.Memory != b.Memory {
		return false
	}
	if a.Halted != b.Halted || a.Truncated != b.Truncated {
		return false
	}
	if !a.ArchTrace.Equal(b.ArchTrace) {
		return false
	}
	// Store buffers are transient.  At a normal end both are flushed; if both
	// halted truncated, compare their architecturally relevant contents.
	sa, sb := a.StoreValues(), b.StoreValues()
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	// PC is meaningful only when execution stopped because of a truncation or
	// an explicit control-flow target; normalize "fell off the end".
	pa, pb := a.PC, b.PC
	if pa >= len(codeA) {
		pa = -1
	}
	if pb >= len(codeB) {
		pb = -1
	}
	return pa == pb
}
