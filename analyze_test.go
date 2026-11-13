package netsec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func leakProgram() Program {
	return Program{
		Code: []Instruction{
			Const(2, 0x90),  // out-of-bounds table region
			JumpEq(0, 0, 5), // always taken to the safe path
			Add(2, 1),       // speculative: address = base + secret
			Load(3, 2),      // speculative out-of-bounds load
			Probe(2),        // speculative secret-dependent probe
			Probe(0),        // architectural probe of public input
			Halt(),          // ends speculation, then rollback
		},
		Inputs: []InputSlot{
			PublicRegister(0),
			PublicRegister(7),
			SecretRegister(1),
		},
	}
}

func barrierProgram() Program {
	return Program{
		Code: []Instruction{
			Const(2, 0x90),
			JumpEq(2, 2, 6), // always taken to the safe path
			Barrier(),       // stops the wrong-path window immediately
			Add(2, 1),
			Load(3, 2),
			Probe(2),
			Probe(0),
			Halt(),
		},
		Inputs: []InputSlot{
			SecretRegister(1),
		},
	}
}

func TestAnalyzerFindsMispredictionHiddenTableLeak(t *testing.T) {
	program := leakProgram()
	result, err := Analyze(program, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusWitnessFound {
		t.Fatalf("status = %s, reason = %s", result.Status, result.Reason)
	}
	w := result.Witness
	if w == nil {
		t.Fatal("witness is nil")
	}
	if len(w.Public) != 2 {
		t.Fatalf("public length = %d", len(w.Public))
	}
	if !reflect.DeepEqual(w.Public, w.RunA.Assignment.Public) || !reflect.DeepEqual(w.Public, w.RunB.Assignment.Public) {
		t.Fatal("witness runs do not share the witness public input")
	}
	if w.SecretA == w.SecretB {
		t.Fatalf("secrets are equal: %d", w.SecretA)
	}
	if !sameProbeLines(w.RunA.ArchProbes, w.RunB.ArchProbes) {
		t.Fatal("architectural probe records differ")
	}
	if sameProbeLines(w.RunA.SpecProbes, w.RunB.SpecProbes) {
		t.Fatal("speculative probe records are identical")
	}
	if w.RunA.Misprediction == nil || w.RunB.Misprediction == nil {
		t.Fatal("witness run has no misprediction")
	}
	if w.RunA.Misprediction.PC != 1 || w.RunB.Misprediction.PC != 1 {
		t.Fatalf("misprediction PCs = %d, %d; want 1", w.RunA.Misprediction.PC, w.RunB.Misprediction.PC)
	}
	if w.RunA.Misprediction.EndReason != "halt" || w.RunB.Misprediction.EndReason != "halt" {
		t.Fatalf("unexpected speculation end reasons: %q, %q",
			w.RunA.Misprediction.EndReason, w.RunB.Misprediction.EndReason)
	}
	if err := VerifyWitness(program, *w); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Witness
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWitness(program, decoded); err != nil {
		t.Fatalf("decoded witness did not replay: %v", err)
	}

	foundMisprediction := false
	rolledBack := false
	for _, step := range w.RunB.Trace {
		foundMisprediction = foundMisprediction || step.MispredictedHere
		rolledBack = rolledBack || step.RolledBack
	}
	if !foundMisprediction || !rolledBack {
		t.Fatalf("trace does not show misprediction and rollback: %+v", w.RunB.Trace)
	}
}

func TestBarrierFixRemovesWitness(t *testing.T) {
	program := barrierProgram()
	result, err := Analyze(program, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoWitnessBounded {
		t.Fatalf("status = %s, reason = %s", result.Status, result.Reason)
	}
	if !result.Exhausted {
		t.Fatal("search reported no witness without being exhaustive")
	}
	if result.Witness != nil {
		t.Fatal("barrier-fixed program produced a witness")
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
	if len(spec.SpecProbes) != 0 {
		t.Fatalf("barrier run retained %d speculative probes", len(spec.SpecProbes))
	}
	if len(spec.Rollbacks) != 1 || spec.Rollbacks[0].Reason != "barrier" {
		t.Fatalf("rollback = %+v, want one barrier rollback", spec.Rollbacks)
	}
	if spec.Misprediction == nil || spec.Misprediction.SpeculativeSteps != 1 {
		t.Fatalf("misprediction = %+v", spec.Misprediction)
	}
	if spec.FinalState.Regs != arch.FinalState.Regs || spec.FinalState.Mem != arch.FinalState.Mem {
		t.Fatal("barrier run did not finish in the architectural state")
	}
}

func TestDirectArchitecturalLeakIsNotSpeculativeWitness(t *testing.T) {
	program := Program{
		Code: []Instruction{
			Probe(0),
			Halt(),
		},
		Inputs: []InputSlot{
			SecretRegister(0),
		},
	}
	result, err := Analyze(program, 400)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoWitnessBounded {
		t.Fatalf("status = %s, reason = %s", result.Status, result.Reason)
	}
	if result.Witness != nil {
		t.Fatal("direct architectural leakage was reported as a speculative witness")
	}

	// The direct leak is real, but it is architectural: two different secrets
	// produce different architectural probe records.
	a := Assignment{Public: []uint8{}, Secret: 0}
	b := Assignment{Public: []uint8{}, Secret: 0x20}
	left, err := Run(program, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Run(program, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sameProbeLines(left.ArchProbes, right.ArchProbes) {
		t.Fatal("expected the direct architectural leak to differ")
	}
}

func TestPublicOnlySpeculationIsNotWitness(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 0, 2), // real path goes to halt
			Probe(0),        // wrong path probes only the public byte
			Halt(),
		},
		Inputs: []InputSlot{
			PublicRegister(0),
			SecretRegister(1),
		},
	}
	result, err := Analyze(program, 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoWitnessBounded {
		t.Fatalf("status = %s, reason = %s", result.Status, result.Reason)
	}
	if !result.Exhausted || result.Witness != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestAnalyzeHonorsRunBudget(t *testing.T) {
	program := Program{
		Code: []Instruction{
			JumpEq(0, 0, 2), // actual path goes to halt at 2
			Probe(1),        // wrong path probes the public byte
			Halt(),
		},
		Inputs: []InputSlot{
			PublicRegister(0),
			PublicRegister(1),
			SecretRegister(2),
		},
	}
	const budget = 7
	result, err := Analyze(program, budget)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("status = %s, want unknown (reason = %s)", result.Status, result.Reason)
	}
	if result.Runs != budget {
		t.Fatalf("runs = %d, want %d", result.Runs, budget)
	}
	if result.Exhausted {
		t.Fatal("budget-limited search claimed to be exhaustive")
	}
	if result.InputCandidates != 256*256*256 {
		t.Fatalf("input candidates = %d", result.InputCandidates)
	}
}

func TestProgramValidationRejectsConflictingInputs(t *testing.T) {
	tests := []struct {
		name    string
		program Program
	}{
		{
			name: "overlap",
			program: Program{
				Code:   []Instruction{Halt()},
				Inputs: []InputSlot{PublicRegister(0), SecretRegister(0)},
			},
		},
		{
			name: "too many public bytes",
			program: Program{
				Code: []Instruction{Halt()},
				Inputs: []InputSlot{
					PublicMemory(0),
					PublicMemory(1),
					PublicMemory(2),
					SecretRegister(0),
				},
			},
		},
		{
			name: "missing secret",
			program: Program{
				Code:   []Instruction{Halt()},
				Inputs: []InputSlot{PublicRegister(0)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.program.Validate(); err == nil {
				t.Fatal("Validate accepted an invalid program")
			}
		})
	}
}
