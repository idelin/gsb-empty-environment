package netsec

import (
	"errors"
	"testing"
)

func TestVMArithmeticAndBranchTrace(t *testing.T) {
	prog := []Instruction{
		{Op: OpRead, Dst: 1, SrcA: 0, Imm: SecretBase},    // R1 = secret[0] = 5
		{Op: OpRead, Dst: 2, SrcA: 0, Imm: CandidateBase}, // R2 = candidate[0] = 3
		{Op: OpXor, Dst: 3, SrcA: 1, SrcB: 2},             // R3 = 5 ^ 3 = 6
		{Op: OpOr, Dst: 4, SrcA: 1, SrcB: 2},              // R4 = 5 | 3 = 7
		{Op: OpInc, Dst: 5, SrcA: 4},                      // R5 = 8
		{Op: OpBranch, SrcA: 3, Off: 2},                   // R3 != 0: skip next
		{Op: OpInc, Dst: 6, SrcA: 0},                      // skipped
		{Op: OpBranch, SrcA: 0, Off: 1},                   // R0 == 0: not taken
	}
	var vm VM
	vm.Mem[SecretBase] = 5
	vm.Mem[CandidateBase] = 3
	vm.MaxSteps = 64
	if err := vm.Run(prog); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if vm.R[3] != 6 || vm.R[4] != 7 || vm.R[5] != 8 {
		t.Fatalf("R3/R4/R5 = %d/%d/%d, want 6/7/8", vm.R[3], vm.R[4], vm.R[5])
	}
	if vm.R[6] != 0 {
		t.Fatalf("R6 = %d, want 0: taken branch must skip the increment", vm.R[6])
	}
	want := []Event{
		{Kind: EventRead, Addr: SecretBase},
		{Kind: EventRead, Addr: CandidateBase},
		{Kind: EventBranch, Taken: true},
		{Kind: EventBranch, Taken: false},
	}
	if FirstDivergence(vm.Trace, want) != -1 {
		t.Fatalf("trace = %+v, want %+v", vm.Trace, want)
	}
}

func TestVMLoopAndNestedValidation(t *testing.T) {
	prog := []Instruction{
		{Op: OpLoop, Imm: 3, Aux: 3},
		{Op: OpLoop, Imm: 2, Aux: 1},
		{Op: OpInc, Dst: 0, SrcA: 0},
		{Op: OpInc, Dst: 1, SrcA: 1},
	}
	var vm VM
	vm.MaxSteps = 64
	if err := vm.Run(prog); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if vm.R[0] != 6 || vm.R[1] != 3 {
		t.Fatalf("R0/R1 = %d/%d, want 6/3", vm.R[0], vm.R[1])
	}
}

func TestVMBudgetExceeded(t *testing.T) {
	// The branch target is itself: while secret[0] != 0 it spins forever.
	prog := []Instruction{
		{Op: OpRead, Dst: 1, SrcA: 0, Imm: SecretBase},
		{Op: OpBranch, SrcA: 1, Off: 0},
	}
	var vm VM
	vm.Mem[SecretBase] = 1
	vm.MaxSteps = 100
	if err := vm.Run(prog); !errors.Is(err, ErrStepBudgetExceeded) {
		t.Fatalf("err = %v, want ErrStepBudgetExceeded", err)
	}
}

func TestVMReadOutOfRange(t *testing.T) {
	prog := []Instruction{{Op: OpRead, Dst: 0, SrcA: 0, Imm: MemSize}}
	var vm VM
	vm.MaxSteps = 10
	if err := vm.Run(prog); err == nil {
		t.Fatal("read outside the memory image must fail")
	}
}

func TestValidateRejectsEscapingBranch(t *testing.T) {
	prog := []Instruction{
		{Op: OpLoop, Imm: 1, Aux: 1},
		{Op: OpBranch, SrcA: 0, Off: 5}, // escapes the loop body
	}
	if err := Validate(prog); err == nil {
		t.Fatal("branch escaping its loop must be rejected")
	}
}
