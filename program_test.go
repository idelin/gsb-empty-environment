package netsec

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAndShortCircuitsAndTraceBindsOrder(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: denyCheck("b")}
	c := &countingCheck{fn: allowCheck("c")}

	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if trace.Status != TraceCompleted || trace.Verdict != VerdictDeny {
		t.Fatalf("trace status/verdict = %s/%s, want completed/deny", trace.Status, trace.Verdict)
	}
	if c.calls != 0 {
		t.Fatalf("c calls = %d, want 0 after AND short-circuit", c.calls)
	}
	if got, want := trace.ExecutedRules(), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed rules = %v, want %v", got, want)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(trace.Steps))
	}
	if trace.Steps[0].Node != "r/and/0" || trace.Steps[1].Node != "r/and/1" {
		t.Fatalf("unexpected sites: %q, %q", trace.Steps[0].Node, trace.Steps[1].Node)
	}
	for i, step := range trace.Steps {
		if step.Seq != i {
			t.Fatalf("step %d has sequence %d", i, step.Seq)
		}
		if !step.Invoked() {
			t.Fatalf("step %d should have been invoked", i)
		}
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	requireVerifyCode(t, p.VerifyAllow(trace), VerifyNotAllow)

	extra := trace.Clone()
	extra.Steps = append(extra.Steps, stepFor("r/and/2", "c", VerdictAllow, "c"))
	extra.Steps[len(extra.Steps)-1].Seq = len(extra.Steps) - 1
	extra = reseal(p, extra)
	requireVerifyCode(t, p.Verify(extra), VerifyExtraStep)

	missing := trace.Clone()
	missing.Steps = missing.Steps[:1]
	missing = reseal(p, missing)
	requireVerifyCode(t, p.Verify(missing), VerifyMissingStep)

	reordered := trace.Clone()
	reordered.Steps[0], reordered.Steps[1] = reordered.Steps[1], reordered.Steps[0]
	for i := range reordered.Steps {
		reordered.Steps[i].Seq = i
	}
	reordered = reseal(p, reordered)
	requireVerifyCode(t, p.Verify(reordered), VerifyStepMismatch)
}

func TestOrShortCircuitsAndAllows(t *testing.T) {
	a := &countingCheck{fn: denyCheck("a")}
	b := &countingCheck{fn: allowCheck("b")}
	c := &countingCheck{fn: denyCheck("c")}

	p := newTestProgram(t, "policy-1",
		Or(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if trace.Verdict != VerdictAllow {
		t.Fatalf("verdict = %s, want allow", trace.Verdict)
	}
	if c.calls != 0 {
		t.Fatalf("c calls = %d, want 0 after OR short-circuit", c.calls)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(trace.Steps))
	}
	if err := p.VerifyAllow(trace); err != nil {
		t.Fatalf("VerifyAllow: %v", err)
	}

	extra := trace.Clone()
	extra.Steps = append(extra.Steps, stepFor("r/or/2", "c", VerdictDeny, "c"))
	extra.Steps[len(extra.Steps)-1].Seq = len(extra.Steps) - 1
	extra = reseal(p, extra)
	requireVerifyCode(t, p.Verify(extra), VerifyExtraStep)
}

func TestNotInvertsAndRecordsOnlyItsChild(t *testing.T) {
	a := &countingCheck{fn: denyCheck("a")}
	p := newTestProgram(t, "policy-1", Not(RuleRef("a")), testRule("a", a))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if trace.Verdict != VerdictAllow {
		t.Fatalf("verdict = %s, want allow", trace.Verdict)
	}
	if len(trace.Steps) != 1 || trace.Steps[0].Node != "r/not" {
		t.Fatalf("unexpected steps: %+v", trace.Steps)
	}
	if err := p.VerifyAllow(trace); err != nil {
		t.Fatalf("VerifyAllow: %v", err)
	}
}

func TestPanicAbortsAndCannotProveLaterRules(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		panic("boom")
	}}
	c := &countingCheck{fn: allowCheck("c")}

	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(context.Background())
	if err == nil {
		t.Fatal("expected panic error")
	}
	if !errors.Is(err, ErrCheckPanicked) {
		t.Fatalf("error %v does not wrap ErrCheckPanicked", err)
	}
	var panicErr *PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("error %v is not *PanicError", err)
	}
	if trace.Status != TracePanicked || trace.Verdict != VerdictInvalid {
		t.Fatalf("trace status/verdict = %s/%s, want panicked/invalid", trace.Status, trace.Verdict)
	}
	if c.calls != 0 {
		t.Fatalf("c calls = %d, want 0 after panic", c.calls)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(trace.Steps))
	}
	panicStep := trace.Steps[1]
	if panicStep.Status != StepPanicked || panicStep.Verdict != VerdictInvalid ||
		panicStep.Evidence != (Evidence{}) {
		t.Fatalf("panic step claims evidence or a verdict: %+v", panicStep)
	}
	if !panicStep.Invoked() {
		t.Fatal("panicking check was invoked but step says otherwise")
	}
	if got, want := trace.ExecutedRules(), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed rules = %v, want %v", got, want)
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify aborted trace: %v", err)
	}
	requireVerifyCode(t, p.VerifyAllow(trace), VerifyNotCompleted)

	extra := trace.Clone()
	extra.Steps = append(extra.Steps, stepFor("r/and/2", "c", VerdictAllow, "c"))
	extra.Steps[len(extra.Steps)-1].Seq = len(extra.Steps) - 1
	extra = reseal(p, extra)
	requireVerifyCode(t, p.Verify(extra), VerifyExtraStep)
}

func TestCheckErrorAbortsAndRecordsNoVerdict(t *testing.T) {
	probeErr := errors.New("probe unavailable")
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		return CheckResult{Verdict: VerdictAllow, Evidence: NewEvidence([]byte("b"), "b")}, probeErr
	}}
	c := &countingCheck{fn: allowCheck("c")}

	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(context.Background())
	if !errors.Is(err, probeErr) {
		t.Fatalf("error = %v, want probe error", err)
	}
	var checkErr *CheckError
	if !errors.As(err, &checkErr) {
		t.Fatalf("error = %v, want *CheckError", err)
	}
	if trace.Status != TraceCheckFailed || trace.Verdict != VerdictInvalid {
		t.Fatalf("trace status/verdict = %s/%s, want check-failed/invalid", trace.Status, trace.Verdict)
	}
	if c.calls != 0 {
		t.Fatalf("c calls = %d, want 0", c.calls)
	}
	if len(trace.Steps) != 2 || trace.Steps[1].Status != StepCheckError ||
		trace.Steps[1].Verdict != VerdictInvalid || trace.Steps[1].Evidence != (Evidence{}) {
		t.Fatalf("unexpected error step: %+v", trace.Steps)
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify error trace: %v", err)
	}
}

func TestCancellationBeforeNextRuleRecordsStopWithoutEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &countingCheck{fn: func(context.Context) (CheckResult, error) {
		cancel()
		return allowCheck("a")(context.Background())
	}}
	b := &countingCheck{fn: allowCheck("b")}
	c := &countingCheck{fn: allowCheck("c")}

	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if trace.Status != TraceCanceled || trace.Verdict != VerdictInvalid {
		t.Fatalf("trace status/verdict = %s/%s, want canceled/invalid", trace.Status, trace.Verdict)
	}
	if b.calls != 0 || c.calls != 0 {
		t.Fatalf("unexecuted rule calls = b:%d c:%d, want 0/0", b.calls, c.calls)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(trace.Steps))
	}
	stop := trace.Steps[1]
	if stop.Status != StepCanceled || stop.Invoked() ||
		stop.Verdict != VerdictInvalid || stop.Evidence != (Evidence{}) {
		t.Fatalf("canceled stop marker is not evidence-free: %+v", stop)
	}
	if stop.Node != "r/and/1" || stop.RuleID != "b" {
		t.Fatalf("canceled at %s/%s, want r/and/1/b", stop.Node, stop.RuleID)
	}
	if got, want := trace.ExecutedRules(), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed rules = %v, want %v", got, want)
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify canceled trace: %v", err)
	}
	requireVerifyCode(t, p.VerifyAllow(trace), VerifyNotCompleted)

	extra := trace.Clone()
	extra.Steps = append(extra.Steps, stepFor("r/and/2", "c", VerdictAllow, "c"))
	extra.Steps[len(extra.Steps)-1].Seq = len(extra.Steps) - 1
	extra = reseal(p, extra)
	requireVerifyCode(t, p.Verify(extra), VerifyExtraStep)
}

func TestCheckReportsCancellationAfterInvocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: func(ctx context.Context) (CheckResult, error) {
		cancel()
		return CheckResult{}, ctx.Err()
	}}
	c := &countingCheck{fn: allowCheck("c")}

	p := newTestProgram(t, "policy-1",
		And(RuleRef("a"), RuleRef("b"), RuleRef("c")),
		testRule("a", a), testRule("b", b), testRule("c", c))

	trace, err := p.Evaluate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if b.calls != 1 || c.calls != 0 {
		t.Fatalf("rule calls = b:%d c:%d, want 1/0", b.calls, c.calls)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(trace.Steps))
	}
	step := trace.Steps[1]
	if step.Status != StepCheckCanceled || !step.Invoked() ||
		step.Verdict != VerdictInvalid || step.Evidence != (Evidence{}) {
		t.Fatalf("unexpected canceled check step: %+v", step)
	}
	if got, want := trace.ExecutedRules(), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed rules = %v, want %v", got, want)
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify canceled check trace: %v", err)
	}
}

func TestCancellationBeforeFirstRuleInvokesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := &countingCheck{fn: allowCheck("a")}
	p := newTestProgram(t, "policy-1", And(RuleRef("a"), RuleRef("b")),
		testRule("a", a), testRule("b", a))

	trace, err := p.Evaluate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if a.calls != 0 {
		t.Fatalf("check calls = %d, want 0", a.calls)
	}
	if len(trace.Steps) != 1 || trace.Steps[0].Status != StepCanceled ||
		trace.Steps[0].Invoked() || trace.Steps[0].Evidence != (Evidence{}) {
		t.Fatalf("unexpected initial cancellation trace: %+v", trace.Steps)
	}
	if err := p.Verify(trace); err != nil {
		t.Fatalf("Verify initial cancellation trace: %v", err)
	}
}

func TestInvalidCheckResultsAbortWithoutEvidence(t *testing.T) {
	tests := []struct {
		name   string
		result CheckResult
	}{
		{
			name:   "zero evidence",
			result: CheckResult{Verdict: VerdictAllow},
		},
		{
			name: "invalid verdict",
			result: CheckResult{
				Verdict:  VerdictInvalid,
				Evidence: NewEvidence([]byte("a"), "a"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &countingCheck{fn: func(context.Context) (CheckResult, error) {
				return tt.result, nil
			}}
			p := newTestProgram(t, "policy-1", RuleRef("a"), testRule("a", a))
			trace, err := p.Evaluate(context.Background())
			if !errors.Is(err, ErrInvalidCheckResult) {
				t.Fatalf("error = %v, want ErrInvalidCheckResult", err)
			}
			if trace.Status != TraceCheckFailed || trace.Verdict != VerdictInvalid {
				t.Fatalf("trace status/verdict = %s/%s", trace.Status, trace.Verdict)
			}
			if len(trace.Steps) != 1 || trace.Steps[0].Status != StepCheckError ||
				trace.Steps[0].Invoked() == false || trace.Steps[0].Evidence != (Evidence{}) {
				t.Fatalf("unexpected step: %+v", trace.Steps)
			}
			if err := p.Verify(trace); err != nil {
				t.Fatalf("Verify aborted trace: %v", err)
			}
		})
	}
}
