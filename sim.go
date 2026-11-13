package netsec

import (
	"fmt"
)

// MispredictSpec selects one dynamically executed conditional jump. Occurrence
// zero is the first JEQ executed on the architectural path. A run has at most
// one such choice.
type MispredictSpec struct {
	BranchOccurrence int
}

// ProbeRecord is one probe operation. Line is Address/16 (integer division).
type ProbeRecord struct {
	Step        int
	PC          int
	Address     uint8
	Line        uint8
	Speculative bool
}

// BranchInfo describes a conditional jump executed on the architectural path.
// These are precisely the jumps that may be selected for a misprediction.
type BranchInfo struct {
	Occurrence   int
	Step         int
	PC           int
	ActualTaken  bool
	ActualTarget int
	Fallthrough  int
	StaticTarget int
	Degenerate   bool
}

// TraceStep is one executed instruction. When RolledBack is true, RegsAfter
// shows the speculative register state immediately after the instruction, and
// the next trace step starts from the restored architectural state.
type TraceStep struct {
	Index            int
	PC               int
	Instruction      Instruction
	Speculative      bool
	RegsBefore       [NumRegisters]uint8
	RegsAfter        [NumRegisters]uint8
	PCNext           int
	ProbeLine        int
	LoadAddress      int
	LoadValue        int
	IsBranch         bool
	BranchEqual      bool
	MispredictedHere bool
	RolledBack       bool
	Note             string
}

// RollbackRecord records the end of a speculative window.
type RollbackRecord struct {
	AtStep           int
	Reason           string
	SpeculativeSteps int
}

// MispredictionInfo describes the one deliberately mispredicted jump in a run.
type MispredictionInfo struct {
	BranchOccurrence int
	PC               int
	ActualTaken      bool
	ActualTarget     int
	PredictedTarget  int
	SpeculativeSteps int
	EndReason        string
}

// RunResult is a deterministic, replayable execution record.
type RunResult struct {
	Assignment             Assignment
	RequestedMisprediction *MispredictSpec
	Misprediction          *MispredictionInfo
	Trace                  []TraceStep
	Branches               []BranchInfo
	ArchProbes             []ProbeRecord
	SpecProbes             []ProbeRecord
	Rollbacks              []RollbackRecord
	FinalState             State
	Steps                  int
	Halted                 bool
	OverLimit              bool
}

type simulator struct {
	code  []Instruction
	regs  [NumRegisters]uint8
	mem   [MemorySize]uint8
	pc    int
	steps int

	inSpec    bool
	specSteps int

	archBranchCount int
	req             *MispredictSpec
	mispredInfo     *MispredictionInfo
	correctPC       int
	snapRegs        [NumRegisters]uint8
	snapMem         [MemorySize]uint8

	trace      []TraceStep
	branches   []BranchInfo
	archProbes []ProbeRecord
	specProbes []ProbeRecord
	rollbacks  []RollbackRecord

	halted    bool
	done      bool
	overLimit bool
	err       error
}

// Run executes a program once. If spec is nil the run is purely architectural.
// Otherwise exactly one JEQ occurrence is made to choose the opposite
// successor, speculative execution is capped at MaxSpecSteps, and state is
// rolled back while probe records are retained.
func Run(p Program, a Assignment, spec *MispredictSpec) (RunResult, error) {
	if err := p.Validate(); err != nil {
		return RunResult{}, err
	}
	return run(p, a, spec)
}

func run(p Program, a Assignment, spec *MispredictSpec) (RunResult, error) {
	if spec != nil && spec.BranchOccurrence < 0 {
		return RunResult{}, fmt.Errorf("%w: negative branch occurrence %d", ErrInvalidInput, spec.BranchOccurrence)
	}
	st, err := p.initialState(a)
	if err != nil {
		return RunResult{}, err
	}

	s := &simulator{
		code: p.Code,
		regs: st.Regs,
		mem:  st.Mem,
	}
	s.req = nil
	var requested *MispredictSpec
	if spec != nil {
		copySpec := *spec
		requested = &copySpec
		s.req = requested
	}

	s.loop()
	if s.err != nil {
		return RunResult{}, s.err
	}
	if requested != nil && s.mispredInfo == nil {
		return RunResult{}, fmt.Errorf("%w: occurrence %d", ErrMispredictionUnreached, requested.BranchOccurrence)
	}

	res := RunResult{
		Assignment:             a,
		RequestedMisprediction: requested,
		Misprediction:          s.mispredInfo,
		Trace:                  s.trace,
		Branches:               s.branches,
		ArchProbes:             s.archProbes,
		SpecProbes:             s.specProbes,
		Rollbacks:              s.rollbacks,
		FinalState: State{
			PC:     s.pc,
			Regs:   s.regs,
			Mem:    s.mem,
			Halted: s.halted,
			Steps:  s.steps,
		},
		Steps:     s.steps,
		Halted:    s.halted,
		OverLimit: s.overLimit,
	}
	res.Assignment.Public = clonePublic(a.Public)
	return res, nil
}

func (s *simulator) loop() {
	for !s.done {
		if s.pc < 0 || s.pc >= len(s.code) {
			if s.inSpec {
				lastStep := -1
				if len(s.trace) > 0 {
					lastStep = s.trace[len(s.trace)-1].Index
				}
				s.rollback("end-of-code", lastStep)
				continue
			}
			s.halted = true
			s.done = true
			return
		}
		if s.steps >= MaxSteps {
			s.overLimit = true
			s.done = true
			return
		}
		s.execute()
	}
}

func (s *simulator) execute() {
	wasSpec := s.inSpec
	idx := s.steps
	pc := s.pc
	in := s.code[pc]
	next := pc + 1
	pcSet := false

	rec := TraceStep{
		Index:       idx,
		PC:          pc,
		Instruction: in,
		Speculative: wasSpec,
		RegsBefore:  s.regs,
		ProbeLine:   -1,
		LoadAddress: -1,
		LoadValue:   -1,
		PCNext:      next,
	}
	s.steps++

	switch in.Op {
	case OpConst:
		s.regs[in.Dst] = in.Imm
	case OpAdd:
		s.regs[in.Dst] = uint8(uint16(s.regs[in.Dst]) + uint16(s.regs[in.Src]))
	case OpAddImm:
		s.regs[in.Dst] = uint8(uint16(s.regs[in.Dst]) + uint16(in.Imm))
	case OpXor:
		s.regs[in.Dst] ^= s.regs[in.Src]
	case OpXorImm:
		s.regs[in.Dst] ^= in.Imm
	case OpLoad:
		addr := s.regs[in.Src]
		value := s.mem[addr]
		rec.LoadAddress = int(addr)
		rec.LoadValue = int(value)
		s.regs[in.Dst] = value
	case OpProbe:
		addr := s.regs[in.Src]
		line := addr / ProbeBlock
		rec.ProbeLine = int(line)
		probe := ProbeRecord{
			Step:        idx,
			PC:          pc,
			Address:     addr,
			Line:        line,
			Speculative: wasSpec,
		}
		if wasSpec {
			s.specProbes = append(s.specProbes, probe)
		} else {
			s.archProbes = append(s.archProbes, probe)
		}
	case OpJeq:
		equal := s.regs[in.Dst] == s.regs[in.Src]
		rec.IsBranch = true
		rec.BranchEqual = equal
		if wasSpec {
			// Secondary conditional jumps in speculation always follow the
			// real condition.
			actual := next
			if equal {
				actual = in.Target
			}
			s.pc = actual
			pcSet = true
			rec.PCNext = actual
		} else {
			occurrence := s.archBranchCount
			s.archBranchCount++

			actual := next
			predicted := next
			if equal {
				actual = in.Target
			} else {
				predicted = in.Target
			}
			degenerate := in.Target == next
			s.branches = append(s.branches, BranchInfo{
				Occurrence:   occurrence,
				Step:         idx,
				PC:           pc,
				ActualTaken:  equal,
				ActualTarget: actual,
				Fallthrough:  next,
				StaticTarget: in.Target,
				Degenerate:   degenerate,
			})

			if s.req != nil && occurrence == s.req.BranchOccurrence {
				if degenerate {
					s.err = fmt.Errorf("%w: instruction %d, occurrence %d", ErrDegenerateBranch, pc, occurrence)
					s.done = true
					break
				}
				s.snapRegs = s.regs
				s.snapMem = s.mem
				s.correctPC = actual
				s.inSpec = true
				s.specSteps = 0
				s.mispredInfo = &MispredictionInfo{
					BranchOccurrence: occurrence,
					PC:               pc,
					ActualTaken:      equal,
					ActualTarget:     actual,
					PredictedTarget:  predicted,
				}
				rec.MispredictedHere = true
				rec.Note = "mispredicted branch; entering speculative path"
				s.pc = predicted
				pcSet = true
				rec.PCNext = predicted
			} else {
				s.pc = actual
				pcSet = true
				rec.PCNext = actual
			}
		}
	case OpBarrier:
		// Architectural barrier is a no-op. A speculative barrier is
		// processed below.
	case OpHalt:
		if !wasSpec {
			s.halted = true
			s.done = true
			rec.PCNext = -1
		}
	default:
		s.err = fmt.Errorf("%w: unknown opcode %d at instruction %d", ErrInvalidProgram, uint8(in.Op), pc)
		s.done = true
	}

	if !pcSet && !s.done {
		s.pc = next
		if !wasSpec && s.pc >= len(s.code) {
			s.halted = true
			s.done = true
		}
	}
	rec.RegsAfter = s.regs

	if wasSpec && s.err == nil {
		rollbackReason := ""
		switch in.Op {
		case OpBarrier:
			s.specSteps++
			rollbackReason = "barrier"
			rec.Note = "barrier ends speculation; rolling back"
		case OpHalt:
			s.specSteps++
			rollbackReason = "halt"
			rec.Note = "halt ends speculation; rolling back"
		default:
			s.specSteps++
			if s.specSteps >= MaxSpecSteps {
				rollbackReason = "speculation-step-limit"
				rec.Note = "speculation step limit reached; rolling back"
			}
		}
		if rollbackReason != "" {
			s.rollback(rollbackReason, idx)
			rec.RolledBack = true
			rec.PCNext = s.pc
		}
	}

	s.trace = append(s.trace, rec)
}

func (s *simulator) rollback(reason string, atStep int) {
	s.regs = s.snapRegs
	s.mem = s.snapMem
	s.pc = s.correctPC
	s.inSpec = false
	if s.mispredInfo != nil {
		s.mispredInfo.SpeculativeSteps = s.specSteps
		s.mispredInfo.EndReason = reason
	}
	s.rollbacks = append(s.rollbacks, RollbackRecord{
		AtStep:           atStep,
		Reason:           reason,
		SpeculativeSteps: s.specSteps,
	})
	s.specSteps = 0
}
