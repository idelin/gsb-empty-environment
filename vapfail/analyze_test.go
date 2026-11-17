package vapfail

import "testing"

func basePolicy() Policy {
	return Policy{
		Name:          "require-team-label",
		FailurePolicy: FailurePolicyFail,
		Validations: []Validation{
			{Name: "team-label-present", MayError: false},
		},
		Match: MatchConditions{ResourceKinds: []string{"Deployment"}},
	}
}

func baseBinding() Binding {
	return Binding{
		Name:       "require-team-label-binding",
		PolicyName: "require-team-label",
		ParamRef: ParamRef{
			Name:      "team-label-params",
			Namespace: "policy-params",
		},
		ParameterNotFoundAction: ParamNotFoundDeny,
		ValidationActions:       []ValidationAction{ActionDeny, ActionAudit},
	}
}

func existingParam() ParamObject {
	return ParamObject{
		Name:      "team-label-params",
		Namespace: "policy-params",
		Labels:    map[string]string{"env": "prod"},
	}
}

func TestCompliantBinding(t *testing.T) {
	report := Analyze(basePolicy(), baseBinding(), []ParamObject{existingParam()}, []SubjectDeletePermission{
		{Subject: "cluster-admin", Restricted: false, Objects: []string{"*"}},
	})
	if report.Rejected {
		t.Fatalf("compliant binding rejected: %+v", report.Findings)
	}
	if report.Bypassable {
		t.Fatalf("compliant binding marked bypassable: %+v", report.Findings)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", report.Findings)
	}
}

func TestDeletedParameterWithAllow(t *testing.T) {
	binding := baseBinding()
	binding.ParameterNotFoundAction = ParamNotFoundAllow

	// The parameter object was deleted: it no longer exists, and a
	// restricted subject holds delete permission on it.
	report := Analyze(basePolicy(), binding, nil, []SubjectDeletePermission{
		{Subject: "system:serviceaccount:default:builder", Restricted: true, Objects: []string{"team-label-params"}},
	})
	if !report.Has(CodeParamMissingAllow) {
		t.Fatalf("expected PARAM_MISSING_ALLOW, got %+v", report.Findings)
	}

	// While the object still exists, a restricted deleter makes the
	// binding bypassable.
	report = Analyze(basePolicy(), binding, []ParamObject{existingParam()}, []SubjectDeletePermission{
		{Subject: "system:serviceaccount:default:builder", Restricted: true, Objects: []string{"team-label-params"}},
	})
	if !report.Bypassable || !report.Has(CodeParamDeletableBypass) {
		t.Fatalf("expected PARAM_DELETABLE_BYPASS, got %+v", report.Findings)
	}
}

func TestZeroMatchSelectorWithAllow(t *testing.T) {
	binding := baseBinding()
	binding.ParamRef = ParamRef{
		Namespace: "policy-params",
		Selector:  map[string]string{"env": "staging"},
	}
	binding.ParameterNotFoundAction = ParamNotFoundAllow

	report := Analyze(basePolicy(), binding, []ParamObject{existingParam()}, nil)
	if !report.Has(CodeSelectorZeroMatchAllow) {
		t.Fatalf("expected SELECTOR_ZERO_MATCH_ALLOW, got %+v", report.Findings)
	}

	// With Deny as the not-found action the zero match is not a bypass.
	binding.ParameterNotFoundAction = ParamNotFoundDeny
	report = Analyze(basePolicy(), binding, []ParamObject{existingParam()}, nil)
	if report.Has(CodeSelectorZeroMatchAllow) {
		t.Fatalf("unexpected SELECTOR_ZERO_MATCH_ALLOW with Deny action: %+v", report.Findings)
	}
}

func TestIgnoreFailurePolicyWithErroringExpression(t *testing.T) {
	policy := basePolicy()
	policy.FailurePolicy = FailurePolicyIgnore
	policy.Validations = []Validation{
		{Name: "safe-check", MayError: false},
		{Name: "crds-spec-lookup", MayError: true},
	}

	report := Analyze(policy, baseBinding(), []ParamObject{existingParam()}, nil)
	if !report.Has(CodeIgnoreExpressionError) {
		t.Fatalf("expected IGNORE_EXPRESSION_ERROR, got %+v", report.Findings)
	}

	// Fail policy with the same expression must not be flagged.
	policy.FailurePolicy = FailurePolicyFail
	report = Analyze(policy, baseBinding(), []ParamObject{existingParam()}, nil)
	if report.Has(CodeIgnoreExpressionError) {
		t.Fatalf("unexpected IGNORE_EXPRESSION_ERROR with Fail policy: %+v", report.Findings)
	}
}

func TestWarnOnlyActions(t *testing.T) {
	binding := baseBinding()
	binding.ValidationActions = []ValidationAction{ActionWarn, ActionAudit}

	report := Analyze(basePolicy(), binding, []ParamObject{existingParam()}, nil)
	if !report.Has(CodeNoDenyAction) {
		t.Fatalf("expected NO_DENY_ACTION, got %+v", report.Findings)
	}
}

func TestAmbiguousParamRefRejected(t *testing.T) {
	binding := baseBinding()
	binding.ParamRef = ParamRef{
		Name:      "team-label-params",
		Namespace: "policy-params",
		Selector:  map[string]string{"env": "prod"},
	}

	report := Analyze(basePolicy(), binding, []ParamObject{existingParam()}, nil)
	if !report.Rejected || !report.Has(CodeParamRefAmbiguous) {
		t.Fatalf("expected rejection with PARAM_REF_AMBIGUOUS, got %+v", report.Findings)
	}
}
