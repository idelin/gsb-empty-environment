package netsec

import (
	"errors"
	"testing"
)

// divisionProgram divides the public divisor into the secret numerator:
//
//	r0 = secret / public; return r0
//
// A zero public divisor always raises ExcDivByZero regardless of the secret,
// which must therefore be classified SecretIndependent.
func divisionProgram() *Program {
	return &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 4},
			{Name: "divisor", Visibility: Public, Bits: 4, Init: 1},
			{Name: "out", Visibility: Public, Bits: 4},
		},
		Code: []Instr{
			{Op: OpDiv, Dst: 2, Src: 0, Src2: 1},
			{Op: OpReturn, Src: 2},
		},
	}
}

func TestRunArithmeticAndReturn(t *testing.T) {
	p := divisionProgram()
	tr, err := Run(p, Input{1: 3}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// secret defaults to Init 0, so 0/3 == 0.
	if tr.Exc != ExcNone || tr.Return != 0 {
		t.Fatalf("unexpected terminal state exc=%v return=%d", tr.Exc, tr.Return)
	}
	if len(tr.Steps) != 2 || tr.Steps[0].State.PC != 0 || tr.Steps[1].State.PC != 1 {
		t.Fatalf("trace does not record both steps: %+v", tr.Steps)
	}
}

func TestRunTypedExceptions(t *testing.T) {
	cases := []struct {
		name string
		ins  Instr
		want ExcCode
		a    uint64
		b    uint64
	}{
		{"overflow", Instr{Op: OpAdd, Dst: 2, Src: 0, Src2: 1}, ExcOverflow, ^uint64(0), 1},
		{"underflow", Instr{Op: OpSub, Dst: 2, Src: 0, Src2: 1}, ExcUnderflow, 3, 4},
		{"divbyzero", Instr{Op: OpDiv, Dst: 2, Src: 0, Src2: 1}, ExcDivByZero, 9, 0},
		{"assertion", Instr{Op: OpAssert, Src: 2}, ExcAssertion, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Program{
				Vars: []Variable{
					{Name: "a", Visibility: Secret, Bits: 64, Init: tc.a},
					{Name: "b", Visibility: Public, Bits: 64, Init: tc.b},
					{Name: "c", Visibility: Public, Bits: 8, Init: 0},
				},
				Code: []Instr{tc.ins, {Op: OpReturn, Src: 2}},
			}
			tr, err := Run(p, nil, nil)
			if err != nil {
				t.Fatalf("Run error: %v", err)
			}
			if tr.Exc != tc.want || tr.ExcPC != 0 {
				t.Fatalf("got exc=%v pc=%d, want %v", tr.Exc, tr.ExcPC, tc.want)
			}
		})
	}
}

func TestRunBranchAndComparison(t *testing.T) {
	// r1 = (secret < 4); if r1 goto pc3; assert r2 (fails); return secret
	p := &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 8},
			{Name: "flag", Visibility: Public, Bits: 8},
			{Name: "zero", Visibility: Public, Bits: 8, Init: 0},
		},
		Code: []Instr{
			{Op: OpCmp, Dst: 1, Src: 0, Src2: 1},
			{Op: OpBranch, Src: 1, Target: 3},
			{Op: OpAssert, Src: 2},
			{Op: OpReturn, Src: 0},
		},
	}
	// Overwrite the comparison constant via a dedicated constant register.
	p.Vars[1].Init = 4

	tr, err := Run(p, nil, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// secret Init is 0 < 4, so the branch skips the failing assertion.
	if tr.Exc != ExcNone || tr.Return != 0 {
		t.Fatalf("expected clean branch, got exc=%v return=%d", tr.Exc, tr.Return)
	}
}

func TestRunNoReturn(t *testing.T) {
	p := &Program{
		Vars: []Variable{{Name: "x", Visibility: Public, Bits: 8}},
		Code: []Instr{{Op: OpConst, Dst: 0, Immediate: 1}, {Op: OpReturn, Src: 0}},
	}
	tr, _ := Run(p, nil, nil)
	if tr.Exc != ExcNone {
		t.Fatalf("unexpected exception %v", tr.Exc)
	}

	bad := &Program{
		Vars: p.Vars,
		Code: []Instr{{Op: OpConst, Dst: 0, Immediate: 1}},
	}
	if err := Validate(bad); err == nil {
		t.Fatal("expected validation error for program without OpReturn")
	}
}

func TestRunHostCallsAndFault(t *testing.T) {
	p := &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 8},
			{Name: "out", Visibility: Public, Bits: 8},
		},
		Code: []Instr{
			{Op: OpHost, Dst: 1, Src: 0, HostID: 7},
			{Op: OpReturn, Src: 1},
		},
	}
	host := HostFunc(func(id uint8, arg uint64) (uint64, bool) {
		if id != 7 {
			t.Errorf("unexpected host id %d", id)
		}
		if arg == 5 {
			return 0, true
		}
		return arg + 1, false
	})
	tr, err := Run(p, nil, host)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if tr.Exc != ExcNone || tr.Return != 1 {
		t.Fatalf("secret=0 expected return 1, got exc=%v return=%d", tr.Exc, tr.Return)
	}
	if len(tr.Observe().Calls) != 1 {
		t.Fatalf("expected one recorded host call")
	}

	p.Vars[0].Init = 5
	tr, _ = Run(p, nil, host)
	if tr.Exc != ExcHost || !tr.Steps[0].Call.Fault {
		t.Fatalf("expected ExcHost with recorded fault, got %v", tr.Exc)
	}
}

func TestValidateRejectsOversizedProgram(t *testing.T) {
	p := &Program{
		Vars: []Variable{{Name: "x", Visibility: Public, Bits: 8}},
	}
	for i := 0; i < MaxInstr+1; i++ {
		p.Code = append(p.Code, Instr{Op: OpConst, Dst: 0, Immediate: 0})
	}
	p.Code = append(p.Code, Instr{Op: OpReturn, Src: 0})
	if err := Validate(p); err == nil {
		t.Fatal("expected MaxInstr validation failure")
	}
}

func TestReplayReproducesTrace(t *testing.T) {
	p := divisionProgram()
	tr, err := Run(p, Input{1: 2}, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	replayed, err := Replay(p, tr)
	if err != nil {
		t.Fatalf("Replay error: %v", err)
	}
	if !tracesEqual(replayed, tr) {
		t.Fatal("replayed trace differs from original")
	}

	// Tampering with a recorded initial value must be detected.
	tampered := *tr
	tampered.Initial = append([]uint64(nil), tr.Initial...)
	tampered.Initial[0] = 9
	if _, err := Replay(p, &tampered); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("expected ErrReplayMismatch, got %v", err)
	}
}
