package netsec

import (
	"strings"
	"testing"
)

// leakyProgram reads secret[0] and candidate[0], then branches on their
// difference: equal inputs fall through to an extra read, different inputs
// skip it. The trace therefore leaks whether the first bytes match.
func leakyProgram() []Instruction {
	return []Instruction{
		{Op: OpRead, Dst: 1, SrcA: 0, Imm: SecretBase},     // R1 = secret[0]
		{Op: OpRead, Dst: 2, SrcA: 0, Imm: CandidateBase},  // R2 = candidate[0]
		{Op: OpXor, Dst: 1, SrcA: 1, SrcB: 2},              // R1 = diff
		{Op: OpBranch, SrcA: 1, Off: 2},                    // differ: skip next
		{Op: OpRead, Dst: 3, SrcA: 0, Imm: SecretBase + 1}, // equal: extra read
	}
}

func TestCheckPassOnCompareProgram(t *testing.T) {
	secret := testSecret()
	other := testSecret()
	other[9] ^= 0xFF
	cand := testSecret()
	cand[31] ^= 0x01

	rep := Check(CompareProgram(), [][]byte{secret, other}, [][]byte{secret, cand}, compareSteps)
	if rep.Verdict != VerdictPass {
		t.Fatalf("verdict = %v (%s), want pass", rep.Verdict, rep.Reason)
	}
	if rep.Witness != nil {
		t.Fatal("passing report must not carry a witness")
	}
	if rep.Inputs != 4 || rep.Pairs != 6 {
		t.Fatalf("inputs/pairs = %d/%d, want 4/6", rep.Inputs, rep.Pairs)
	}
	if rep.Reason != PassDisclaimer {
		t.Fatal("passing report must carry the finite-set disclaimer")
	}
}

func TestCheckDetectsLeak(t *testing.T) {
	secret := testSecret()
	diff := testSecret()
	diff[0] ^= 0x40

	rep := Check(leakyProgram(), [][]byte{secret}, [][]byte{secret, diff}, 64)
	if rep.Verdict != VerdictFail {
		t.Fatalf("verdict = %v (%s), want fail", rep.Verdict, rep.Reason)
	}
	w := rep.Witness
	if w == nil {
		t.Fatal("failing report must carry a witness")
	}
	if len(w.SecretA) != SecretSize || len(w.CandidateA) != CandidateSize ||
		len(w.SecretB) != SecretSize || len(w.CandidateB) != CandidateSize {
		t.Fatal("witness must contain both complete inputs")
	}
	if w.DivergeAt < 0 || w.DivergeAt >= max(len(w.TraceA), len(w.TraceB)) {
		t.Fatalf("invalid divergence index %d", w.DivergeAt)
	}
	// The witness traces must genuinely differ at the reported index.
	shorter := min(len(w.TraceA), len(w.TraceB))
	if w.DivergeAt < shorter && w.TraceA[w.DivergeAt] == w.TraceB[w.DivergeAt] {
		t.Fatalf("witness traces agree at reported divergence %d", w.DivergeAt)
	}
}

func TestCheckShortestDifference(t *testing.T) {
	// Branch on candidate[0]: candidate 0 takes the branch, candidate 1 does
	// not. The first trace event already diverges, so DivergeAt must be 0.
	prog := []Instruction{
		{Op: OpRead, Dst: 1, SrcA: 0, Imm: CandidateBase},
		{Op: OpBranch, SrcA: 1, Off: 2},
		{Op: OpRead, Dst: 2, SrcA: 0, Imm: SecretBase},
	}
	secret := testSecret()
	c0 := make([]byte, CandidateSize)
	c1 := make([]byte, CandidateSize)
	c1[0] = 1

	rep := Check(prog, [][]byte{secret}, [][]byte{c0, c1}, 64)
	if rep.Verdict != VerdictFail {
		t.Fatalf("verdict = %v, want fail", rep.Verdict)
	}
	if rep.Witness.DivergeAt != 1 {
		t.Fatalf("DivergeAt = %d, want 1 (branch outcome event)", rep.Witness.DivergeAt)
	}
}

func TestCheckUnknownWithoutBudget(t *testing.T) {
	secret := testSecret()
	rep := Check(CompareProgram(), [][]byte{secret}, [][]byte{secret}, 0)
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %v, want unknown: zero budget must never pass", rep.Verdict)
	}
}

func TestCheckUnknownWhenBudgetExhausted(t *testing.T) {
	// 1 + 1000 steps of work, budget only 10.
	prog := []Instruction{
		{Op: OpLoop, Imm: 200, Aux: 1},
		{Op: OpInc, Dst: 0, SrcA: 0},
	}
	secret := testSecret()
	rep := Check(prog, [][]byte{secret}, [][]byte{secret}, 10)
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %v, want unknown when the budget is exhausted", rep.Verdict)
	}
}

func TestCheckRejectsOversizedProgram(t *testing.T) {
	prog := make([]Instruction, MaxProgramLen+1)
	for i := range prog {
		prog[i] = Instruction{Op: OpInc, Dst: 0, SrcA: 0}
	}
	secret := testSecret()
	rep := Check(prog, [][]byte{secret}, [][]byte{secret}, 1<<20)
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %v, want unknown for oversized program", rep.Verdict)
	}
	if !strings.Contains(rep.Reason, "too large") {
		t.Fatalf("reason %q should mention the size limit", rep.Reason)
	}
}

func TestCheckRejectsBadInputLength(t *testing.T) {
	secret := testSecret()
	rep := Check(CompareProgram(), [][]byte{secret[:31]}, [][]byte{secret}, compareSteps)
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %v, want unknown for malformed secret", rep.Verdict)
	}
}

func TestCheckUnknownWithSingleInput(t *testing.T) {
	secret := testSecret()
	rep := Check(CompareProgram(), [][]byte{secret}, [][]byte{secret}, compareSteps)
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("verdict = %v, want unknown: one input cannot be compared pairwise", rep.Verdict)
	}
}
