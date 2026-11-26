package seccomp

import (
	"errors"
	"strings"
	"testing"
)

func validationIssues(t *testing.T, insns []Instruction) *ValidationError {
	t.Helper()
	err := Validate(insns)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %T is not *ValidationError", err)
	}
	return ve
}

func TestValidateAcceptsFixture(t *testing.T) {
	if err := Validate(nr59OnlyFilter()); err != nil {
		t.Fatalf("nr59 filter should validate: %v", err)
	}
}

func TestValidateEmpty(t *testing.T) {
	ve := validationIssues(t, nil)
	if !strings.Contains(ve.Error(), "empty") {
		t.Fatalf("want empty program error, got %v", ve)
	}
}

func TestValidateTooManyInstructions(t *testing.T) {
	insns := make([]Instruction, MaxInsns+1)
	for i := range insns {
		insns[i] = Stmt(opRetK, RetAllow)
	}
	ve := validationIssues(t, insns)
	found := false
	for _, is := range ve.Issues {
		if is.Subject == "length" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected length issue, got %+v", ve.Issues)
	}
}

func TestValidateJumpOutOfBounds(t *testing.T) {
	insns := []Instruction{
		Stmt(opLdNR, 0),
		Jump(JmpJEQ|SrcK|ClassJMP, 59, 200, 0), // jt target way past end
		Stmt(opRetK, RetAllow),
	}
	ve := validationIssues(t, insns)
	found := false
	for _, is := range ve.Issues {
		if is.Subject == "jump" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected jump issue, got %+v", ve.Issues)
	}
}

func TestValidateMemAndOffsetBounds(t *testing.T) {
	badMem := []Instruction{
		Stmt(ClassST, MemWords),
		Stmt(opRetK, RetAllow),
	}
	if !hasIssue(validationIssues(t, badMem), "mem") {
		t.Fatal("expected mem index issue")
	}

	badABS := []Instruction{
		Stmt(ClassLD|SizeW|ModeABS, SeccompDataSize),
		Stmt(opRetK, RetAllow),
	}
	if !hasIssue(validationIssues(t, badABS), "offset") {
		t.Fatal("expected ABS offset issue")
	}
}

func TestValidateIllegalOpcodeAndAction(t *testing.T) {
	badOp := []Instruction{
		{Code: 0xff},
		Stmt(opRetK, RetAllow),
	}
	if !hasIssue(validationIssues(t, badOp), "opcode") {
		t.Fatal("expected opcode issue")
	}

	badAction := []Instruction{
		Stmt(opRetK, 0x12340000),
	}
	if !hasIssue(validationIssues(t, badAction), "action") {
		t.Fatal("expected action issue")
	}
}

func TestValidateLastMustReturn(t *testing.T) {
	insns := []Instruction{Stmt(opLdNR, 0)}
	if !hasIssue(validationIssues(t, insns), "termination") {
		t.Fatal("expected termination issue")
	}
}

func TestValidateDivZeroConstant(t *testing.T) {
	insns := []Instruction{
		Stmt(ClassALU|AluDIV|SrcK, 0),
		Stmt(opRetK, RetAllow),
	}
	if !hasIssue(validationIssues(t, insns), "alu") {
		t.Fatal("expected alu divide-zero issue")
	}
}

func hasIssue(ve *ValidationError, subject string) bool {
	for _, is := range ve.Issues {
		if is.Subject == subject {
			return true
		}
	}
	return false
}
