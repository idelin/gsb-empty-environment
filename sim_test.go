package netsec

import (
	"testing"
)

func TestRollbackKeepsSecretOutOfArchitecturalRegisters(t *testing.T) {
	program := Program{
		Code: []Instruction{
			Const(0, 0xFE),
			JumpEq(0, 0, 4), // always taken to the safe path
			Load(1, 0),      // speculative load reads the secret byte
			Probe(1),        // speculative probe reveals the loaded secret
			Probe(2),
			Halt(),
		},
		Inputs: []InputSlot{
			SecretMemory(0xFE),
		},
	}
	assignment := Assignment{Public: []uint8{}, Secret: 0x42}

	arch, err := Run(program, assignment, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Run(program, assignment, &MispredictSpec{BranchOccurrence: 0})
	if err != nil {
		t.Fatal(err)
	}
	if spec.OverLimit {
		t.Fatal("run exceeded the step limit")
	}
	if spec.FinalState.Regs != arch.FinalState.Regs || spec.FinalState.Mem != arch.FinalState.Mem {
		t.Fatalf("state was not rolled back:\narch=%v\nspec=%v", arch.FinalState, spec.FinalState)
	}
	if spec.FinalState.Regs[1] != 0 {
		t.Fatalf("secret remained in R1 after rollback: %#x", spec.FinalState.Regs[1])
	}
	if spec.Misprediction == nil || spec.Misprediction.EndReason != "halt" {
		t.Fatalf("misprediction = %+v", spec.Misprediction)
	}
	if spec.Misprediction.SpeculativeSteps != 4 {
		t.Fatalf("speculative steps = %d, want 4", spec.Misprediction.SpeculativeSteps)
	}
	if len(spec.SpecProbes) != 2 {
		t.Fatalf("speculative probes = %d, want 2", len(spec.SpecProbes))
	}
	if spec.SpecProbes[0].Line != 0x42/ProbeBlock {
		t.Fatalf("speculative probe line = %d, want %d", spec.SpecProbes[0].Line, 0x42/ProbeBlock)
	}
	if !sameProbeLines(spec.ArchProbes, arch.ArchProbes) {
		t.Fatal("architectural probes were not preserved")
	}

	var load *TraceStep
	for i := range spec.Trace {
		if spec.Trace[i].PC == 2 && spec.Trace[i].Instruction.Op == OpLoad {
			load = &spec.Trace[i]
		}
	}
	if load == nil {
		t.Fatal("speculative load is absent from the trace")
	}
	if !load.Speculative || load.LoadAddress != 0xFE || load.LoadValue != 0x42 {
		t.Fatalf("load trace = %+v", load)
	}
	if len(spec.Rollbacks) != 1 || spec.Rollbacks[0].Reason != "halt" {
		t.Fatalf("rollbacks = %+v", spec.Rollbacks)
	}
}

func TestSpeculationCappedAtEightSteps(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 0, 3), // actual path halts, wrong path enters a loop
			Probe(1),
			JumpEq(1, 1, 1), // a secondary branch follows its real condition
			Halt(),
		},
		Inputs: []InputSlot{
			SecretRegister(1),
		},
	}
	assignment := Assignment{Public: []uint8{}, Secret: 0x55}
	arch, err := Run(program, assignment, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := Run(program, assignment, &MispredictSpec{BranchOccurrence: 0})
	if err != nil {
		t.Fatal(err)
	}
	if run.OverLimit {
		t.Fatal("run exceeded the 64-step limit")
	}
	if run.Misprediction == nil {
		t.Fatal("missing misprediction record")
	}
	if run.Misprediction.SpeculativeSteps != MaxSpecSteps {
		t.Fatalf("speculative steps = %d, want %d", run.Misprediction.SpeculativeSteps, MaxSpecSteps)
	}
	if run.Misprediction.EndReason != "speculation-step-limit" {
		t.Fatalf("end reason = %q", run.Misprediction.EndReason)
	}
	if len(run.SpecProbes) != 4 {
		t.Fatalf("speculative probes = %d, want 4", len(run.SpecProbes))
	}
	if run.FinalState.Regs != arch.FinalState.Regs {
		t.Fatalf("registers were not restored: spec=%v arch=%v", run.FinalState.Regs, arch.FinalState.Regs)
	}
}

func TestMispredictionCanChooseTheNotTakenTarget(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 1, 3), // R0=0 and R1=1, so the real branch is not taken
			Probe(2),
			Halt(),
			Probe(3),
			Halt(),
		},
		Inputs: []InputSlot{
			PublicRegister(1),
			SecretRegister(2),
		},
	}
	assignment := Assignment{Public: []uint8{1}, Secret: 0x21}
	run, err := Run(program, assignment, &MispredictSpec{BranchOccurrence: 0})
	if err != nil {
		t.Fatal(err)
	}
	if run.Misprediction == nil {
		t.Fatal("missing misprediction record")
	}
	if run.Misprediction.ActualTaken {
		t.Fatal("branch was reported as taken")
	}
	if run.Misprediction.ActualTarget != 1 || run.Misprediction.PredictedTarget != 3 {
		t.Fatalf("actual/predicted targets = %d/%d, want 1/3",
			run.Misprediction.ActualTarget, run.Misprediction.PredictedTarget)
	}
	if len(run.SpecProbes) != 1 || run.SpecProbes[0].PC != 3 {
		t.Fatalf("speculative probes = %+v, want one probe at PC 3", run.SpecProbes)
	}
	if len(run.Trace) < 4 || run.Trace[3].PC != 1 || run.Trace[3].Speculative {
		t.Fatalf("trace did not resume at the correct fallthrough: %+v", run.Trace)
	}
}

func TestMispredictionUsesDynamicBranchOccurrence(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 0, 2), // architectural occurrence 0
			Probe(1),        // never architectural
			JumpEq(0, 0, 4), // architectural occurrence 1
			Probe(1),        // wrong path after mispredicting occurrence 1
			Halt(),
		},
		Inputs: []InputSlot{
			SecretRegister(1),
		},
	}
	assignment := Assignment{Public: []uint8{}, Secret: 0x11}

	arch, err := Run(program, assignment, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(arch.Branches) != 2 {
		t.Fatalf("architectural branches = %d, want 2", len(arch.Branches))
	}
	if arch.Branches[1].Occurrence != 1 || arch.Branches[1].PC != 2 {
		t.Fatalf("second branch = %+v", arch.Branches[1])
	}

	run, err := Run(program, assignment, &MispredictSpec{BranchOccurrence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if run.Misprediction == nil || run.Misprediction.PC != 2 || run.Misprediction.BranchOccurrence != 1 {
		t.Fatalf("misprediction = %+v", run.Misprediction)
	}
	if len(run.SpecProbes) != 1 || run.SpecProbes[0].PC != 3 {
		t.Fatalf("speculative probes = %+v", run.SpecProbes)
	}
	if run.FinalState.Regs != arch.FinalState.Regs {
		t.Fatal("registers were not restored")
	}
}

func TestWrongSuccessorPastCodeRollsBackWithNoSpeculation(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 1, 3), // target 3 is past the end; real branch is not taken
			Halt(),
			Halt(),
		},
		Inputs: []InputSlot{
			PublicRegister(1),
			SecretRegister(2),
		},
	}
	assignment := Assignment{Public: []uint8{1}, Secret: 0x33}
	run, err := Run(program, assignment, &MispredictSpec{BranchOccurrence: 0})
	if err != nil {
		t.Fatal(err)
	}
	if run.Misprediction == nil || run.Misprediction.PredictedTarget != len(program.Code) {
		t.Fatalf("misprediction = %+v", run.Misprediction)
	}
	if run.Misprediction.SpeculativeSteps != 0 || run.Misprediction.EndReason != "end-of-code" {
		t.Fatalf("speculation = %+v", run.Misprediction)
	}
	if len(run.SpecProbes) != 0 {
		t.Fatalf("speculative probes = %+v", run.SpecProbes)
	}
	if run.FinalState.PC != 1 || run.FinalState.Halted == false {
		t.Fatalf("final state = %+v", run.FinalState)
	}
}

func TestStepLimitIsUnknownAndNotHalting(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 0, 0), // architectural infinite loop
		},
		Inputs: []InputSlot{
			SecretRegister(1),
		},
	}
	run, err := Run(program, Assignment{Public: []uint8{}, Secret: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !run.OverLimit {
		t.Fatal("expected the step limit to be hit")
	}
	if run.Steps != MaxSteps {
		t.Fatalf("steps = %d, want %d", run.Steps, MaxSteps)
	}
	if run.Halted {
		t.Fatal("step-limited run was reported as halted")
	}
}
