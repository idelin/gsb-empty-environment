package relexec

import (
	"math/big"
	"testing"
)

func halt() Instr { return Instr{Op: OpHalt} }

func TestVerifyEquivalentIdenticalPrograms(t *testing.T) {
	prog := &Program{Code: []Instr{
		{Op: OpAdd, Dst: 0, Src1: 0, Src2: 1},
		{Op: OpConst, Dst: 2, Imm: 7},
		{Op: OpMul, Dst: 0, Src1: 0, Src2: 2},
		halt(),
	}}
	res, err := Verify(prog, prog, Config{InputLen: 2, ArrayLen: 4, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictEquivalent || res.Reason != ReasonNone {
		t.Fatalf("verdict: got %s/%s want equivalent", res.Verdict, res.Reason)
	}
	if res.Checked.Cmp(res.Total) != 0 || res.Total.Cmp(big.NewInt(65536)) != 0 {
		t.Fatalf("coverage: checked=%s total=%s", res.Checked, res.Total)
	}
	if res.Counterexample != nil {
		t.Fatalf("equivalent run must not carry a counterexample")
	}
}

// earlyRejectPrograms builds the classic patch-review pair. The old program
// stores value 9 at the input index unconditionally and returns 1. The
// patched program first checks index < limit; rejected inputs jump to an
// explicit out-of-bounds store, modeling an early reject/trap.
//
// Register convention: r3 holds the input index and r4 the stored value, so
// neither is ever clobbered by the guard constants.
func earlyRejectPrograms(limit uint8) (oldP, newP *Program) {
	oldP = &Program{Code: []Instr{
		{Op: OpMov, Dst: 3, Src1: 0},
		{Op: OpConst, Dst: 4, Imm: 9},
		{Op: OpStore, Src1: 3, Src2: 4}, // traps when r0 >= arrayLen
		{Op: OpConst, Dst: 0, Imm: 1},
		halt(),
	}}
	newP = &Program{Code: []Instr{
		{Op: OpMov, Dst: 3, Src1: 0},  // pc 0: preserve input index
		{Op: OpConst, Dst: 4, Imm: 9}, // pc 1: stored value
		{Op: OpConst, Dst: 1, Imm: limit},
		{Op: OpCmpLtu, Dst: 2, Src1: 3, Src2: 1}, // pc 3: r2 = index < limit
		{Op: OpJz, Src1: 2, Target: 10},          // pc 4: reject path
		{Op: OpStore, Src1: 3, Src2: 4},          // pc 5: guarded store
		{Op: OpConst, Dst: 0, Imm: 1},
		halt(),                                  // pc 7: normal completion
		halt(),                                  // pc 8
		halt(),                                  // pc 9: early-rejection entry
		{Op: OpConst, Dst: 5, Imm: MaxArrayLen}, // pc 10: always-OOB index
		{Op: OpStore, Src1: 5, Src2: 4},         // pc 11: guaranteed trap
	}}
	return oldP, newP
}

func TestVerifyEarlyRejectionEquivalentWhenGuardMatches(t *testing.T) {
	oldP, newP := earlyRejectPrograms(4)
	res, err := Verify(oldP, newP, Config{InputLen: 1, ArrayLen: 4, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictEquivalent {
		t.Fatalf("got %s (%s), want equivalent; ce=%v", res.Verdict, res.Reason, res.Counterexample)
	}
	if res.Checked.Uint64() != 256 {
		t.Fatalf("checked %d inputs, want 256", res.Checked.Uint64())
	}
}

func TestVerifyEarlyRejectionCounterexampleWhenCheckTooTight(t *testing.T) {
	oldP, newP := earlyRejectPrograms(4)
	// The guard rejects index >= 4, but the array has 16 bytes: inputs
	// 4..15 are legal for the old program, so the early rejection is a real
	// regression rather than a justified out-of-bounds exemption.
	res, err := Verify(oldP, newP, Config{InputLen: 1, ArrayLen: 16, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictCounterexample || res.Reason != ReasonNewOOB {
		t.Fatalf("got %s/%s", res.Verdict, res.Reason)
	}
	if got := res.Counterexample.Input; len(got) != 1 || got[0] != 4 {
		t.Fatalf("lex-min counterexample: got %v want [4]", got)
	}
	if !res.Counterexample.New.OOB || res.Counterexample.Old.OOB {
		t.Fatalf("old must be in bounds while new traps: old=%+v new=%+v",
			res.Counterexample.Old, res.Counterexample.New)
	}
}

// divergentBranchPrograms return r0=9 when the input is zero, r0=7 otherwise.
// The new program flips the zero-branch return value, so the two sides take
// different branches with different observable results.
func divergentBranchPrograms() (oldP, newP *Program) {
	oldP = &Program{Code: []Instr{
		{Op: OpJz, Src1: 0, Target: 3},
		{Op: OpConst, Dst: 0, Imm: 7},
		{Op: OpJmp, Target: 4},
		{Op: OpConst, Dst: 0, Imm: 9}, // pc 3
		halt(),                        // pc 4
	}}
	patched := *oldP
	patched.Code = append([]Instr(nil), oldP.Code...)
	patched.Code[3].Imm = 8 // zero branch now returns 8
	return oldP, &patched
}

func TestVerifyBranchDivergenceReturnMismatch(t *testing.T) {
	oldP, newP := divergentBranchPrograms()
	res, err := Verify(oldP, newP, Config{InputLen: 1, ArrayLen: 4, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictCounterexample || res.Reason != ReasonReturnMismatch {
		t.Fatalf("got %s/%s", res.Verdict, res.Reason)
	}
	if got := res.Counterexample.Input; got[0] != 0 {
		t.Fatalf("lex-min counterexample: got %v want [0]", got)
	}
	if res.Counterexample.Old.Return != 9 || res.Counterexample.New.Return != 8 {
		t.Fatalf("returns old=%d new=%d", res.Counterexample.Old.Return, res.Counterexample.New.Return)
	}
}

func TestVerifyWriteTraceMismatch(t *testing.T) {
	oldP := &Program{Code: []Instr{
		{Op: OpStore, Src1: 0, Src2: 1},
		halt(),
	}}
	newP := &Program{Code: []Instr{
		{Op: OpConst, Dst: 2, Imm: 0x80},
		{Op: OpXor, Dst: 1, Src1: 1, Src2: 2}, // flips value written when r1 set
		{Op: OpStore, Src1: 0, Src2: 1},
		halt(),
	}}
	res, err := Verify(oldP, newP, Config{InputLen: 2, ArrayLen: 4, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictCounterexample || res.Reason != ReasonWriteMismatch {
		t.Fatalf("got %s/%s", res.Verdict, res.Reason)
	}
	// r0 is the index and must stay in bounds. On input [0 0] the new
	// program flips r1 from 0 to 0x80, so the very first input already
	// diverges in its write value.
	if got := res.Counterexample.Input; got[0] != 0 || got[1] != 0 {
		t.Fatalf("lex-min counterexample: got %v want [0 0]", got)
	}
}

func TestVerifyUnknownWhenBudgetRunsOut(t *testing.T) {
	oldP, newP := divergentBranchPrograms()
	// A budget below the steps needed for even one complete old-program
	// execution must surface as unknown rather than a premature verdict.
	res, err := Verify(oldP, newP, Config{InputLen: 1, ArrayLen: 4, Budget: 3})
	if err != nil {
		t.Fatalf("budget exhaustion is a verdict, not an error: %v", err)
	}
	if res.Verdict != VerdictUnknown {
		t.Fatalf("got %s want unknown", res.Verdict)
	}
	if res.Checked.Cmp(res.Total) >= 0 {
		t.Fatalf("exhausted run must not cover the whole space")
	}
	if res.Counterexample != nil {
		t.Fatalf("unknown verdict must not carry a counterexample")
	}
}

func TestVerifyZeroInputLength(t *testing.T) {
	prog := &Program{Code: []Instr{
		{Op: OpConst, Dst: 0, Imm: 42},
		halt(),
	}}
	res, err := Verify(prog, prog, Config{InputLen: 0, ArrayLen: 2, Budget: UnlimitedBudget})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictEquivalent || res.Total.Sign() != 1 || res.Checked.Cmp(res.Total) != 0 {
		t.Fatalf("zero-length input space: %+v", res)
	}
}

func TestVerifyRejectsInvalidConfigAndPrograms(t *testing.T) {
	good := &Program{Code: []Instr{halt()}}
	bad := &Program{Code: []Instr{{Op: OpJmp, Target: 5}}}
	if _, err := Verify(bad, good, Config{InputLen: 1, ArrayLen: 4, Budget: 1}); err == nil {
		t.Error("bad old program must error")
	}
	if _, err := Verify(good, bad, Config{InputLen: 1, ArrayLen: 4, Budget: 1}); err == nil {
		t.Error("bad new program must error")
	}
	if _, err := Verify(good, good, Config{InputLen: MaxInputLen + 1, ArrayLen: 4, Budget: 1}); err == nil {
		t.Error("InputLen above max must error")
	}
	if _, err := Verify(good, good, Config{InputLen: 1, ArrayLen: 17, Budget: 1}); err == nil {
		t.Error("ArrayLen above max must error")
	}
	if _, err := Verify(good, good, Config{InputLen: 1, ArrayLen: 4, Budget: 0}); err == nil {
		t.Error("zero budget must error")
	}
}
