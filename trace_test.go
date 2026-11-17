package netsec

import (
	"context"
	"sync"
	"testing"
)

func TestDeterministicEvaluationProducesSameSeal(t *testing.T) {
	a := &countingCheck{fn: allowCheck("a")}
	b := &countingCheck{fn: denyCheck("b")}
	p := newTestProgram(t, "policy-1", And(RuleRef("a"), RuleRef("b")),
		testRule("a", a), testRule("b", b))

	first, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("first Evaluate: %v", err)
	}
	second, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if first.Seal != second.Seal {
		t.Fatalf("deterministic evaluations produced different seals: %s != %s", first.Seal, second.Seal)
	}
	if len(first.Steps) != len(second.Steps) {
		t.Fatalf("step counts differ: %d != %d", len(first.Steps), len(second.Steps))
	}
	for i := range first.Steps {
		if first.Steps[i] != second.Steps[i] {
			t.Fatalf("step %d differs: %+v != %+v", i, first.Steps[i], second.Steps[i])
		}
	}
}

func TestProgramIsSafeForConcurrentEvaluation(t *testing.T) {
	allow := CheckFunc(func(context.Context) (CheckResult, error) {
		return CheckResult{
			Verdict:  VerdictAllow,
			Evidence: NewEvidence([]byte("a"), "a"),
		}, nil
	})
	p := newTestProgram(t, "policy-1", And(RuleRef("a"), RuleRef("a")),
		testRule("a", allow))

	const goroutines = 32
	seals := make([]Digest, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			trace, err := p.Evaluate(context.Background())
			if err == nil {
				err = p.VerifyAllow(trace)
			}
			seals[i] = trace.Seal
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("evaluation %d: %v", i, errs[i])
		}
		if seals[i] != seals[0] {
			t.Fatalf("evaluation %d seal %s differs from %s", i, seals[i], seals[0])
		}
	}
}
