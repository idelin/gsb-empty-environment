package seccomp

import (
	"errors"
	"testing"
)

func TestVerifierAndAliases(t *testing.T) {
	pol := NewPolicy(Deny("execve", []Arch{ArchI386}, []uint32{11}))
	v := NewVerifier(pol)
	rep, err := v.Verify(nr59OnlyFilter())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe || rep.Minimal.Tuple.Arch != ArchI386 {
		t.Fatalf("verifier got safe=%v minimal=%v", rep.Safe, rep.Minimal)
	}

	st, err := v.VerifyStack([][]Instruction{nr59OnlyFilter()})
	if err != nil || st.Safe {
		t.Fatalf("stack via verifier safe=%v err=%v", st.Safe, err)
	}

	if _, err := Parse(EncodeAll(nr59OnlyFilter())); err != nil {
		t.Fatal(err)
	}
}

func TestStrictValidationDynamicReturn(t *testing.T) {
	prog := []Instruction{
		Stmt(ClassLD|SizeW|ModeIMM, RetAllow),
		retA(),
	}
	if err := Validate(prog); err != nil {
		t.Fatalf("default validate should accept RET A structurally: %v", err)
	}
	if err := ValidateStrict(prog); err == nil {
		t.Fatal("strict validation should reject dynamic RET A")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) || !hasIssue(ve, "action") {
			t.Fatalf("expected action issue, got %v", err)
		}
	}
}

func TestSymbolicDynamicReturnResolved(t *testing.T) {
	// A is loaded with ALLOW then returned via RET A: verifier should still
	// classify the lax outcome.
	prog := []Instruction{
		Stmt(ClassLD|SizeW|ModeIMM, RetAllow),
		retA(),
	}
	pol := NewPolicy(Deny("x", []Arch{ArchX8664}, []uint32{59}))
	rep, err := Verify(prog, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("RET A of constant ALLOW should be found as lax")
	}
	if rep.Minimal.Action&RetActionFull != RetAllow {
		t.Fatalf("action=%s", ActionName(rep.Minimal.Action))
	}
}

func TestLaxLogOption(t *testing.T) {
	prog := []Instruction{Stmt(opRetK, RetLog)}
	pol := NewPolicy(Deny("x", []Arch{ArchX8664}, []uint32{59}))

	rep, err := Verify(prog, pol)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Safe {
		t.Fatal("default lax set is ALLOW only; RET LOG must not count as bypass")
	}

	rep2, err := Verify(prog, pol, WithLogAsLax())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Safe || rep2.Minimal.Action&RetActionFull != RetLog {
		t.Fatalf("WithLogAsLax should flag LOG, safe=%v", rep2.Safe)
	}
}
