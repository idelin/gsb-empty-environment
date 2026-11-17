package netsec

import (
	"context"
	"testing"
)

func TestNewProgramValidation(t *testing.T) {
	check := CheckFunc(func(context.Context) (CheckResult, error) {
		return CheckResult{Verdict: VerdictAllow, Evidence: NewEvidence([]byte("x"), "x")}, nil
	})
	validRule := Rule{ID: "a", Version: "v1", Check: check}

	tests := []struct {
		name    string
		version string
		root    *Expr
		rules   []Rule
	}{
		{name: "empty version", version: "", root: RuleRef("a"), rules: []Rule{validRule}},
		{name: "nil root", version: "p1", root: nil, rules: []Rule{validRule}},
		{name: "empty rule id", version: "p1", root: RuleRef("a"), rules: []Rule{{ID: "", Version: "v1", Check: check}}},
		{name: "empty rule version", version: "p1", root: RuleRef("a"), rules: []Rule{{ID: "a", Version: "", Check: check}}},
		{name: "nil check", version: "p1", root: RuleRef("a"), rules: []Rule{{ID: "a", Version: "v1"}}},
		{
			name:    "duplicate rule",
			version: "p1",
			root:    RuleRef("a"),
			rules:   []Rule{validRule, validRule},
		},
		{name: "unknown rule", version: "p1", root: RuleRef("missing"), rules: []Rule{validRule}},
		{name: "empty and", version: "p1", root: And(), rules: []Rule{validRule}},
		{name: "empty or", version: "p1", root: Or(), rules: []Rule{validRule}},
		{name: "nil child", version: "p1", root: And(RuleRef("a"), nil), rules: []Rule{validRule}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewProgram(tt.version, tt.root, tt.rules); err == nil {
				t.Fatal("NewProgram accepted invalid policy")
			}
		})
	}
}

func TestProgramAllowsExtraRegisteredRules(t *testing.T) {
	a := CheckFunc(func(context.Context) (CheckResult, error) {
		return CheckResult{Verdict: VerdictAllow, Evidence: NewEvidence([]byte("a"), "a")}, nil
	})
	b := CheckFunc(func(context.Context) (CheckResult, error) {
		return CheckResult{Verdict: VerdictDeny, Evidence: NewEvidence([]byte("b"), "b")}, nil
	})

	p := newTestProgram(t, "policy-1", RuleRef("a"),
		testRule("a", a), testRule("b", b))
	trace, err := p.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if trace.Verdict != VerdictAllow {
		t.Fatalf("verdict = %s, want allow", trace.Verdict)
	}
	if err := p.VerifyAllow(trace); err != nil {
		t.Fatalf("VerifyAllow: %v", err)
	}
}
