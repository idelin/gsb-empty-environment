package netsec

import "testing"

func basePolicy() Policy {
	return Policy{
		Name:          "guard",
		FailurePolicy: FailurePolicyFail,
		Validations: []Validation{
			{Name: "required-labels", Expression: "object.metadata.labels != null"},
		},
		MatchConditions: []MatchCondition{
			{Name: "match-deployments", Expression: "object.kind == 'Deployment'"},
		},
	}
}

func denyBinding() Binding {
	return Binding{
		Name:                    "guard-binding",
		ParamRef:                ParamRef{Name: "guard-config"},
		ParameterNotFoundAction: ParameterNotFoundDeny,
		ValidationActions:       []ValidationAction{ValidationActionDeny},
	}
}

func parameter(name string, labels map[string]string) ParamObject {
	return ParamObject{Name: name, Labels: labels}
}

func TestCompliantBindingHasNoFindings(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	params := []ParamObject{parameter("guard-config", map[string]string{"tier": "prod"})}
	subjects := []Subject{
		{Name: "deploy-bot", Restricted: true},
		{Name: "platform-admin", Restricted: false, CanDeleteParams: []string{"*"}},
	}

	report := Analyze(policy, binding, params, subjects)

	if report.Rejected {
		t.Fatal("compliant binding was rejected")
	}
	if report.Allowed {
		t.Fatal("compliant binding has an allow path")
	}
	if report.Bypassable {
		t.Fatal("compliant binding was marked bypassable")
	}
	if len(report.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestRestrictedSubjectCanDeleteParameter(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ParameterNotFoundAction = ParameterNotFoundAllow
	params := []ParamObject{parameter("guard-config", nil)}
	subjects := []Subject{
		{Name: "deploy-bot", Restricted: true, CanDeleteParams: []string{"guard-config"}},
		{Name: "read-only", Restricted: true},
	}

	report := Analyze(policy, binding, params, subjects)

	if !report.Bypassable {
		t.Fatal("parameter deletion by a restricted subject was not marked as bypassable")
	}
	if !report.Has(FindingParamDeletableByRestricted) {
		t.Fatal("missing parameter-deletion finding")
	}
	if report.Has(FindingParameterMissingAllowed) {
		t.Fatal("existing parameter was reported as missing")
	}
}

func TestParameterDeletionIsNotBypassWhenMissingParameterIsDenied(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	params := []ParamObject{parameter("guard-config", nil)}
	subjects := []Subject{
		{Name: "deploy-bot", Restricted: true, CanDeleteParams: []string{"guard-config"}},
	}

	report := Analyze(policy, binding, params, subjects)

	if report.Bypassable {
		t.Fatal("deleting a parameter behind parameterNotFoundAction Deny does not bypass the policy")
	}
}

func TestParameterDeletedIsAllowedWhenActionIsAllow(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ParameterNotFoundAction = ParameterNotFoundAllow

	report := Analyze(policy, binding, nil, nil)

	if !report.Allowed {
		t.Fatal("missing parameter with parameterNotFoundAction Allow did not allow the request")
	}
	if !report.Has(FindingParameterMissingAllowed) {
		t.Fatal("missing parameter allow finding")
	}
	if report.Bypassable {
		t.Fatal("a parameter that no longer exists cannot be deleted")
	}
}

func TestMissingParameterIsDeniedWhenActionIsDeny(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()

	report := Analyze(policy, binding, nil, nil)

	if report.Allowed {
		t.Fatal("missing parameter with parameterNotFoundAction Deny must not allow the request")
	}
}

func TestSelectorMatchingNothingIsAllowedWhenActionIsAllow(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ParamRef = ParamRef{
		Selector: &LabelSelector{MatchLabels: map[string]string{"role": "guard"}},
	}
	binding.ParameterNotFoundAction = ParameterNotFoundAllow
	params := []ParamObject{parameter("other", map[string]string{"role": "worker"})}

	report := Analyze(policy, binding, params, nil)

	if !report.Allowed {
		t.Fatal("zero-match selector with parameterNotFoundAction Allow did not allow the request")
	}
	if !report.Has(FindingSelectorMatchedNothingAllowed) {
		t.Fatal("missing zero-match selector allow finding")
	}
}

func TestSelectorMatchingNothingIsDeniedWhenActionIsDeny(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ParamRef = ParamRef{
		Selector: &LabelSelector{MatchLabels: map[string]string{"role": "guard"}},
	}
	params := []ParamObject{parameter("other", map[string]string{"role": "worker"})}

	report := Analyze(policy, binding, params, nil)

	if report.Allowed {
		t.Fatal("zero-match selector with parameterNotFoundAction Deny must not allow the request")
	}
}

func TestIgnoreExpressionErrorIsAllowed(t *testing.T) {
	policy := basePolicy()
	policy.FailurePolicy = FailurePolicyIgnore
	policy.Validations[0].MayError = true
	binding := denyBinding()
	params := []ParamObject{parameter("guard-config", nil)}

	report := Analyze(policy, binding, params, nil)

	if !report.Allowed {
		t.Fatal("Ignore with an error-prone expression did not allow the request")
	}
	if !report.Has(FindingExpressionErrorAllowed) {
		t.Fatal("missing expression error allow finding")
	}
}

func TestFailExpressionErrorIsNotAllowed(t *testing.T) {
	policy := basePolicy()
	policy.Validations[0].MayError = true
	binding := denyBinding()
	params := []ParamObject{parameter("guard-config", nil)}

	report := Analyze(policy, binding, params, nil)

	if report.Allowed {
		t.Fatal("Fail with an error-prone expression must not allow the request")
	}
}

func TestWarnOnlyBindingCannotDeny(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ValidationActions = []ValidationAction{ValidationActionWarn, ValidationActionAudit}
	params := []ParamObject{parameter("guard-config", nil)}

	report := Analyze(policy, binding, params, nil)

	if !report.Allowed {
		t.Fatal("binding without Deny must not be able to reject the request")
	}
	if !report.Has(FindingNoDenyAction) {
		t.Fatal("missing no-Deny-action finding")
	}
}

func TestParamRefWithNameAndSelectorIsRejected(t *testing.T) {
	policy := basePolicy()
	binding := denyBinding()
	binding.ParamRef.Selector = &LabelSelector{MatchLabels: map[string]string{"tier": "prod"}}

	report := AnalyzeBindings(policy, []Binding{binding}, nil, nil)

	if !report.Rejected {
		t.Fatal("paramRef with both name and selector was not rejected")
	}
	if !report.Has(FindingParamRefConflict) {
		t.Fatal("missing paramRef conflict finding")
	}
	if report.Allowed || report.Bypassable {
		t.Fatal("rejected binding must not report an allow path")
	}
}

func TestSelectorExpressions(t *testing.T) {
	selector := &LabelSelector{
		MatchLabels: map[string]string{"tier": "prod"},
		MatchExpressions: []LabelSelectorRequirement{
			{Key: "role", Operator: LabelSelectorIn, Values: []string{"guard", "monitor"}},
			{Key: "retired", Operator: LabelSelectorDoesNotExist},
		},
	}
	labels := map[string]string{"tier": "prod", "role": "guard"}
	if !selector.Matches(labels) {
		t.Fatal("expected selector to match")
	}
	if selector.Matches(map[string]string{"tier": "dev", "role": "guard"}) {
		t.Fatal("selector matched a different tier")
	}
	if selector.Matches(map[string]string{"tier": "prod", "role": "worker"}) {
		t.Fatal("selector matched a value outside In")
	}
}

func TestMultipleBindingsAreReportedSeparately(t *testing.T) {
	policy := basePolicy()
	params := []ParamObject{parameter("guard-config", nil)}
	bindings := []Binding{
		denyBinding(),
		{
			Name:              "warn-binding",
			ParamRef:          ParamRef{Name: "other-config"},
			ValidationActions: []ValidationAction{ValidationActionWarn},
		},
	}

	report := AnalyzeBindings(policy, bindings, params, nil)

	if !report.Allowed {
		t.Fatal("Warn-only binding did not allow the request")
	}
	assertFindingBinding(t, report, FindingNoDenyAction, 1, "warn-binding")
}

func assertFindingBinding(t *testing.T, report Report, code FindingCode, index int, name string) {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.Code != code {
			continue
		}
		if finding.BindingIndex != index || finding.BindingName != name {
			t.Fatalf("finding %q reported for binding %d/%q, want %d/%q", code, finding.BindingIndex, finding.BindingName, index, name)
		}
		return
	}
	t.Fatalf("missing finding %q", code)
}
