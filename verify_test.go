package netsec

import (
	"context"
	"testing"
)

type cancelRunContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelRunContext) CancelRun() {
	c.cancel()
}

func TestSealRejectsFieldTampering(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	p := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	tamperedVerdict := trace.Clone()
	tamperedVerdict.Steps[0].Verdict = VerdictDeny
	requireVerifyCode(t, p.Verify(tamperedVerdict), VerifySealMismatch)

	tamperedEvidence := trace.Clone()
	tamperedEvidence.Steps[0].Evidence.Digest = DigestOf([]byte("other"))
	requireVerifyCode(t, p.Verify(tamperedEvidence), VerifySealMismatch)

	tamperedSummary := trace.Clone()
	tamperedSummary.Steps[0].Evidence.Summary = "other"
	requireVerifyCode(t, p.Verify(tamperedSummary), VerifySealMismatch)

	tamperedOrder := trace.Clone()
	tamperedOrder.Steps[0].Seq = 7
	requireVerifyCode(t, p.Verify(tamperedOrder), VerifySealMismatch)

	tamperedOutcome := trace.Clone()
	tamperedOutcome.Verdict = VerdictDeny
	requireVerifyCode(t, p.Verify(tamperedOutcome), VerifySealMismatch)
}

func TestVersionBindingRejectsDifferentPolicyAndRuleRevision(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	p1 := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))
	trace, err := p1.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	p2 := newTestProgram(t, "policy-2", RuleRef("a"), testRule("a", a))
	requireVerifyCode(t, p2.Verify(trace), VerifyProgramMismatch)

	changedRule := Rule{ID: "a", Version: "v2", Check: a}
	p3 := newTestProgram(t, "policy-1", RuleRef("a"), changedRule)
	requireVerifyCode(t, p3.Verify(trace), VerifyProgramMismatch)

	changedStep := trace.Clone()
	changedStep.Steps[0].RuleVersion = "v2"
	changedStep = reseal(p1, changedStep)
	requireVerifyCode(t, p1.Verify(changedStep), VerifyStepMismatch)

	changedRuleID := trace.Clone()
	changedRuleID.Steps[0].RuleID = "b"
	changedRuleID = reseal(p1, changedRuleID)
	requireVerifyCode(t, p1.Verify(changedRuleID), VerifyStepMismatch)

	changedSite := trace.Clone()
	changedSite.Steps[0].Node = "r/elsewhere"
	changedSite = reseal(p1, changedSite)
	requireVerifyCode(t, p1.Verify(changedSite), VerifyStepMismatch)
}

func TestVerifyLiveReproducesEvidenceAndRejectsForgery(t *testing.T) {
	a := &countingCheck{fn: denyCheck("a")}
	p := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))

	actual, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := p.VerifyLive(context.Background(), actual); err != nil {
		t.Fatalf("VerifyLive actual trace: %v", err)
	}

	forged := actual.Clone()
	forged.Steps[0].Verdict = VerdictAllow
	forged.Steps[0].Evidence = NewEvidence([]byte("forged"), "forged")
	forged.Verdict = VerdictAllow
	forged = reseal(p, forged)
	if err := p.Verify(forged); err != nil {
		t.Fatalf("structural replay should accept an internally consistent but forged trace: %v", err)
	}
	requireVerifyCode(t, p.VerifyLive(context.Background(), forged), VerifyLiveMismatch)
}

func TestAbortedTraceCannotBeLiveVerified(t *testing.T) {
	a := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		panic("boom")
	}}
	p := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))
	trace, _ := p.Evaluate(context.Background())

	requireVerifyCode(t, p.VerifyLive(context.Background(), trace), VerifyNotCompleted)
}

func TestPanicForgedAsSuccessIsRejectedByLiveReplay(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		panic("boom")
	}}
	c := &countingCheck{fn: allowCheck("c")}
	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	actual, _ := p.Evaluate(context.Background())
	forged := actual.Clone()
	forged.Steps[1] = stepFor("r/and/1", "b", VerdictAllow, "forged-b")
	forged.Steps[1].Seq = 1
	forged.Steps = append(forged.Steps, stepFor("r/and/2", "c", VerdictAllow, "forged-c"))
	forged.Steps[len(forged.Steps)-1].Seq = len(forged.Steps) - 1
	forged.Status = TraceCompleted
	forged.Verdict = VerdictAllow
	forged = reseal(p, forged)

	if err := p.VerifyAllow(forged); err != nil {
		t.Fatalf("structural replay should accept the internally consistent forged trace: %v", err)
	}
	requireVerifyCode(t, p.VerifyLive(context.Background(), forged), VerifyLiveFailed)
}

func TestCancellationForgedAsSuccessIsRejectedByLiveReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &countingCheck{fn: func(ctx context.Context) (CheckResult, error) {
		if cancelable, ok := ctx.(interface{ CancelRun() }); ok {
			cancelable.CancelRun()
		}
		return allowCheck("a")(context.Background())
	}}
	b := &countingCheck{fn: allowCheck("b")}
	p := newTestProgram(t, "policy-1", And(RuleRef("a"), RuleRef("b")),
		testRule("a", a), testRule("b", b))

	actual, _ := p.Evaluate(cancelRunContext{Context: ctx, cancel: cancel})
	forged := actual.Clone()
	forged.Steps[1] = stepFor("r/and/1", "b", VerdictAllow, "forged-b")
	forged.Steps[1].Seq = 1
	forged.Status = TraceCompleted
	forged.Verdict = VerdictAllow
	forged = reseal(p, forged)

	if err := p.VerifyAllow(forged); err != nil {
		t.Fatalf("structural replay should accept the internally consistent forged trace: %v", err)
	}
	liveCtx, liveCancel := context.WithCancel(context.Background())
	requireVerifyCode(t, p.VerifyLive(cancelRunContext{Context: liveCtx, cancel: liveCancel}, forged), VerifyLiveFailed)
}

func TestReplayerRejectsEvidenceOnNonOKStep(t *testing.T) {
	a := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		panic("boom")
	}}
	p := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))
	trace, _ := p.Evaluate(context.Background())

	forged := trace.Clone()
	forged.Steps[0].Verdict = VerdictAllow
	forged.Steps[0].Evidence = NewEvidence([]byte("a"), "a")
	forged = reseal(p, forged)
	requireVerifyCode(t, p.Verify(forged), VerifyStepState)
}
