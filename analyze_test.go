package netsec

import (
	"reflect"
	"testing"
)

// guardedUnlock is a program that unlocks only when the verification
// result bit is true; skipping the NOT or the CJmp bypasses the check.
func guardedUnlock() Prog {
	return Prog{Code: []Instr{
		ReadVer(0), // 0
		Not(0),     // 1
		CJmp(0, 4), // 2: if !ver -> halt
		Unlock(),   // 3
		Halt(),     // 4
	}}
}

func TestFaultWitnessAndMinimalPatches(t *testing.T) {
	rep, err := Analyze(guardedUnlock(), []int{3, 1, 0, 2})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.Verified {
		t.Fatal("report not independently verified")
	}
	want := &Witness{Input: 0, Skip: 1, UnlockStep: 3, PreExisting: false}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	wantPatches := [][]int{{0}, {1}, {2}, {3}}
	if !reflect.DeepEqual(rep.Patches, wantPatches) {
		t.Fatalf("patches = %v, want %v", rep.Patches, wantPatches)
	}
	if rep.NoSolution {
		t.Fatal("unexpected no-solution")
	}
}

func TestInsertGuardsRemapsJumpTargets(t *testing.T) {
	p := guardedUnlock()
	got := InsertGuards(p.Code, []int{3})
	want := []Instr{
		ReadVer(0),
		Not(0),
		CJmp(0, 5), // target 4 shifted past the inserted guard
		Guard(),
		Unlock(),
		Halt(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InsertGuards = %v, want %v", got, want)
	}

	// A jump target past the last instruction (halt) stays past the end.
	code := []Instr{CJmp(0, 2), Unlock()}
	got = InsertGuards(code, []int{0})
	want = []Instr{Guard(), CJmp(0, 3), Unlock()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InsertGuards past-end = %v, want %v", got, want)
	}
}

func TestPreExistingVulnerabilityNotAttributable(t *testing.T) {
	p := Prog{Code: []Instr{Unlock(), Halt()}}
	rep, err := Analyze(p, []int{0})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := &Witness{Input: 0, Skip: -1, UnlockStep: 0, PreExisting: true}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	// One guard can always be the single skipped instruction, so a
	// single candidate cannot fix a pre-existing unlock.
	if !rep.NoSolution || len(rep.Patches) != 0 {
		t.Fatalf("expected no solution, got patches %v", rep.Patches)
	}
	if !rep.Verified {
		t.Fatal("no-solution conclusion not independently verified")
	}
}

func TestGuardItselfSkippableNeedsTwoGuards(t *testing.T) {
	// The unlock is reachable without any fault (pre-existing), and any
	// single guard can itself be the one skipped instruction, so only a
	// pair of consecutive guards before the read closes every fault.
	p := Prog{Code: []Instr{ReadVer(0), Unlock(), Halt()}}
	rep, err := Analyze(p, []int{0, 1, 2})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := &Witness{Input: 0, Skip: -1, UnlockStep: 1, PreExisting: true}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	wantPatches := [][]int{{0, 1}}
	if !reflect.DeepEqual(rep.Patches, wantPatches) {
		t.Fatalf("patches = %v, want %v", rep.Patches, wantPatches)
	}
	if !rep.Verified {
		t.Fatal("report not independently verified")
	}
}

// spinOnFalseVer loops forever when verification fails; the loop exceeds
// the step bound, so only patch pairs that stop the spin before the
// CJmp (even when one guard is skipped) are valid.
func spinOnFalseVer() Prog {
	return Prog{Code: []Instr{
		ReadVer(0), // 0
		Not(0),     // 1
		CJmp(0, 2), // 2: spin while !ver
		Unlock(),   // 3
		Halt(),     // 4
	}}
}

func TestLoopOverStepBoundCannotBeSafe(t *testing.T) {
	rep, err := Analyze(spinOnFalseVer(), []int{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := &Witness{Input: 0, Skip: 1, UnlockStep: 3, PreExisting: false}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	// Equal-size patches sorted by position sequence.
	wantPatches := [][]int{{0, 1}, {0, 2}, {1, 2}}
	if !reflect.DeepEqual(rep.Patches, wantPatches) {
		t.Fatalf("patches = %v, want %v", rep.Patches, wantPatches)
	}
	if !rep.Verified {
		t.Fatal("report not independently verified")
	}
}

func TestValidInputSpinForcesNoSolution(t *testing.T) {
	p := Prog{Code: []Instr{
		ReadVer(0), // 0
		CJmp(0, 1), // 1: spin while ver
		Unlock(),   // 2
		Halt(),     // 3
	}}
	rep, err := Analyze(p, []int{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// ver=false falls through to unlock without any fault.
	want := &Witness{Input: 0, Skip: -1, UnlockStep: 2, PreExisting: true}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	// The ver=true baseline never halts, so unlock-count equivalence
	// cannot be established and nothing may be reported safe.
	if !rep.NoSolution {
		t.Fatalf("expected no solution, got patches %v", rep.Patches)
	}
	if !rep.Verified {
		t.Fatal("no-solution conclusion not independently verified")
	}
}

func TestPublicInputBits(t *testing.T) {
	p := Prog{
		NumPub: 1,
		Code: []Instr{
			ReadPub(0, 0), // 0
			ReadVer(1),    // 1
			And(1, 0),     // 2: r1 = ver && pub0
			Not(1),        // 3
			CJmp(1, 6),    // 4: if !(ver && pub0) -> halt
			Unlock(),      // 5
			Halt(),        // 6
		},
	}
	rep, err := Analyze(p, []int{5})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := &Witness{Input: 0, Skip: 3, UnlockStep: 5, PreExisting: false}
	if rep.Witness == nil || *rep.Witness != *want {
		t.Fatalf("witness = %+v, want %+v", rep.Witness, want)
	}
	wantPatches := [][]int{{5}}
	if !reflect.DeepEqual(rep.Patches, wantPatches) {
		t.Fatalf("patches = %v, want %v", rep.Patches, wantPatches)
	}
}

func TestValidationRejectsIllegalPrograms(t *testing.T) {
	cases := []struct {
		name string
		prog Prog
	}{
		{"empty", Prog{}},
		{"too long", Prog{Code: make([]Instr, MaxInstrs+1)}},
		{"too many public bits", Prog{Code: []Instr{Halt()}, NumPub: MaxPubBits + 1}},
		{"illegal register", Prog{Code: []Instr{ReadVer(NumRegs)}}},
		{"illegal and source", Prog{Code: []Instr{And(0, NumRegs)}}},
		{"illegal public bit", Prog{NumPub: 2, Code: []Instr{ReadPub(0, 2)}}},
		{"illegal jump target", Prog{Code: []Instr{CJmp(0, 3), Halt()}}},
		{"negative jump target", Prog{Code: []Instr{CJmp(0, -1), Halt()}}},
		{"guard in host program", Prog{Code: []Instr{Guard()}}},
		{"unknown opcode", Prog{Code: []Instr{{Op: Op(42)}}}},
	}
	for _, tc := range cases {
		if err := tc.prog.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
	// Jumping to len(code) is legal: control flow past the end halts.
	ok := Prog{Code: []Instr{CJmp(0, 2), Halt()}}
	if err := ok.Validate(); err != nil {
		t.Errorf("past-end jump target should be legal: %v", err)
	}
}

func TestCandidateValidation(t *testing.T) {
	p := guardedUnlock()
	if _, err := Analyze(p, []int{0, 0}); err == nil {
		t.Error("duplicate candidates should be rejected")
	}
	if _, err := Analyze(p, []int{6}); err == nil {
		t.Error("out-of-range candidate should be rejected")
	}
	if _, err := Analyze(p, make([]int, MaxCandidates+1)); err == nil {
		t.Error("too many candidates should be rejected")
	}
}

func TestIndependentVerificationDetectsTampering(t *testing.T) {
	p := guardedUnlock()
	cands := []int{0, 1, 2, 3}
	rep, err := Analyze(p, cands)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	tampered := *rep
	w := *rep.Witness
	w.Skip = 2
	tampered.Witness = &w
	if err := VerifyReport(p, cands, &tampered); err == nil {
		t.Error("tampered witness not detected")
	}

	tampered = *rep
	tampered.Patches = [][]int{{0}}
	if err := VerifyReport(p, cands, &tampered); err == nil {
		t.Error("incomplete patch set not detected")
	}

	tampered = *rep
	tampered.Patches = nil
	tampered.NoSolution = true
	if err := VerifyReport(p, cands, &tampered); err == nil {
		t.Error("false no-solution not detected")
	}

	if err := VerifyReport(p, cands, rep); err != nil {
		t.Errorf("genuine report rejected: %v", err)
	}
}

func TestNoUnlockProgramNeedsNoGuard(t *testing.T) {
	// A program with no unlock can never produce a witness, and the
	// empty patch is already safe.
	p := Prog{Code: []Instr{ReadVer(0), Halt()}}
	rep, err := Analyze(p, []int{0})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Witness != nil {
		t.Fatalf("unexpected witness %+v", rep.Witness)
	}
	if !reflect.DeepEqual(rep.Patches, [][]int{{}}) {
		t.Fatalf("patches = %v, want [[]]", rep.Patches)
	}
	if rep.NoSolution {
		t.Fatal("empty patch should solve a program with no unlock")
	}
	if !rep.Verified {
		t.Fatal("report not independently verified")
	}
}
