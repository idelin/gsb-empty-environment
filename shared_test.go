package netsec

import (
	"context"
	"reflect"
	"testing"
)

func TestSharedNodeSitesAreSeparateEvidenceObligations(t *testing.T) {
	shared := RuleRef("a")
	root := Or(shared, And(Not(shared), RuleRef("b")))

	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: allowCheck("b")}
	p := newTestProgram(t, "policy-1", root, testRule("a", a), testRule("b", b))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if a.calls != 1 || b.calls != 0 {
		t.Fatalf("rule calls = a:%d b:%d, want 1/0", a.calls, b.calls)
	}
	if len(trace.Steps) != 1 || trace.Steps[0].Node != "r/or/0" {
		t.Fatalf("unexpected steps: %+v", trace.Steps)
	}
	if err := p.VerifyAllow(trace); err != nil {
		t.Fatalf("VerifyAllow: %v", err)
	}

	forged := trace.Clone()
	second := stepFor("r/or/1/and/0/not", "a", VerdictAllow, "a")
	second.Seq = 1
	forged.Steps = append(forged.Steps, second)
	forged = reseal(p, forged)
	requireVerifyCode(t, p.Verify(forged), VerifyExtraStep)
}

func TestSharedNodeMissingSecondOccurrenceIsRejected(t *testing.T) {
	shared := RuleRef("a")
	root := Or(shared, And(Not(shared), RuleRef("b")))

	a := &countingCheck{fn: denyCheck("a")}
	b := &countingCheck{fn: allowCheck("b")}
	p := newTestProgram(t, "policy-1", root, testRule("a", a), testRule("b", b))

	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if a.calls != 2 {
		t.Fatalf("shared rule calls = %d, want 2 (one per reached site)", a.calls)
	}
	if got, want := trace.ExecutedRules(), []string{"a", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed rules = %v, want %v", got, want)
	}
	if len(trace.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(trace.Steps))
	}
	wantSites := []string{"r/or/0", "r/or/1/and/0/not", "r/or/1/and/1"}
	for i, want := range wantSites {
		if trace.Steps[i].Node != want {
			t.Fatalf("step %d node = %q, want %q", i, trace.Steps[i].Node, want)
		}
	}
	if err := p.VerifyAllow(trace); err != nil {
		t.Fatalf("VerifyAllow: %v", err)
	}

	missing := trace.Clone()
	missing.Steps = append(missing.Steps[:1], missing.Steps[2:]...)
	for i := range missing.Steps {
		missing.Steps[i].Seq = i
	}
	missing = reseal(p, missing)
	requireVerifyCode(t, p.Verify(missing), VerifyStepMismatch)
}
