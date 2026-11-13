package netsec

import (
	"errors"
	"testing"
)

func TestConcreteArithmeticWrapsModulo256(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{
		{Register: 0, Value: 250},
		{Register: 1, Value: 10},
	}
	p.AddNode(NewAdd(2, 0, 1), 1)
	p.AddNode(NewPublicWrite(2), 2)
	p.AddNode(NewReturn())

	run := RunConcrete(p, nil)
	if run.Unknown || run.Termination != TerminationReturn {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Trace) != 1 || run.Trace[0].Value != 4 {
		t.Fatalf("trace = %v, want [4]", run.Trace)
	}
}

func TestConcreteAbnormalExit(t *testing.T) {
	p := NewProgram()
	p.AddNode(NewPublicWrite(0), 1)
	p.AddNode(NewAbort())
	run := RunConcrete(p, nil)
	if run.Termination != TerminationAbnormal || run.Unknown {
		t.Fatalf("run = %+v, want abnormal exit", run)
	}
}

func TestConcreteBudgetIsUnknown(t *testing.T) {
	p := NewProgram()
	p.AddNode(Instruction{Op: OpNop}, 0)
	run := RunConcrete(p, nil, ExecConfig{StepLimit: 3})
	if !run.Unknown || run.Termination != TerminationBudget || !errors.Is(run.Err, ErrExecutionBudget) {
		t.Fatalf("run = %+v, want budget unknown", run)
	}
}

func TestCrossCheckRejectsMoreThanThreeSecrets(t *testing.T) {
	p := NewProgram()
	for reg := Register(0); reg < 4; reg++ {
		p.Inputs = append(p.Inputs, Input{Register: reg, Labels: Labels(Label("s" + string(rune('0'+reg))))})
	}
	p.AddNode(NewReturn())
	report := CrossCheck(p)
	if !errors.Is(report.Err, ErrTooManySecrets) || !report.Unknown {
		t.Fatalf("report = %+v, want too-many-secrets", report)
	}
}

func TestThreeSecretsEnumerateEightAssignments(t *testing.T) {
	p := NewProgram()
	for reg := Register(0); reg < 3; reg++ {
		p.Inputs = append(p.Inputs, Input{Register: reg, Labels: Labels(Label("s" + string(rune('0'+reg))))})
	}
	p.AddNode(NewReturn())
	report := CrossCheck(p)
	if len(report.Assignments) != 8 || report.Executions != 8 || report.Pairs != 28 {
		t.Fatalf("assignments=%d executions=%d pairs=%d, want 8/8/28",
			len(report.Assignments), report.Executions, report.Pairs)
	}
	if !report.Sound() {
		t.Fatalf("report = %+v, want sound", report)
	}
}
