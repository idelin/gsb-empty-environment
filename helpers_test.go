package netsec

import (
	"context"
	"errors"
	"testing"
)

type countingCheck struct {
	calls int
	fn    func(context.Context) (CheckResult, error)
}

func (c *countingCheck) Evaluate(ctx context.Context) (CheckResult, error) {
	c.calls++
	return c.fn(ctx)
}

func allowCheck(label string) func(context.Context) (CheckResult, error) {
	return func(context.Context) (CheckResult, error) {
		return CheckResult{
			Verdict:  VerdictAllow,
			Evidence: NewEvidence([]byte(label), label),
		}, nil
	}
}

func denyCheck(label string) func(context.Context) (CheckResult, error) {
	return func(context.Context) (CheckResult, error) {
		return CheckResult{
			Verdict:  VerdictDeny,
			Evidence: NewEvidence([]byte(label), label),
		}, nil
	}
}

func testRule(id string, check Check) Rule {
	return Rule{ID: id, Version: "v1", Check: check}
}

func newTestProgram(t *testing.T, version string, root *Expr, rules ...Rule) *Program {
	t.Helper()
	p, err := NewProgram(version, root, rules)
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	return p
}

func reseal(p *Program, trace Trace) Trace {
	trace.Seal = sealTrace(p.Digest(), trace)
	return trace
}

func requireVerifyCode(t *testing.T, err error, want VerifyCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected verification error %q, got nil", want)
	}
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("error %v does not wrap ErrVerification", err)
	}
	var verifyErr *VerificationError
	if !errors.As(err, &verifyErr) {
		t.Fatalf("error %v is not *VerificationError", err)
	}
	if verifyErr.Code != want {
		t.Fatalf("verification code = %q, want %q (error: %v)", verifyErr.Code, want, err)
	}
}

func stepFor(site, ruleID string, verdict Verdict, evidenceLabel string) Step {
	return Step{
		Node:        site,
		RuleID:      ruleID,
		RuleVersion: "v1",
		Status:      StepOK,
		Verdict:     verdict,
		Evidence:    NewEvidence([]byte(evidenceLabel), evidenceLabel),
	}
}
