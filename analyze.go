package netsec

import "fmt"

// Analyze analyzes a policy with a single binding.
func Analyze(policy Policy, binding Binding, params []ParamObject, subjects []Subject) Report {
	return AnalyzeBindings(policy, []Binding{binding}, params, subjects)
}

// AnalyzeBindings analyzes a policy with all of its bindings. Findings identify
// the binding that produced them by name and index.
func AnalyzeBindings(policy Policy, bindings []Binding, params []ParamObject, subjects []Subject) Report {
	var report Report
	for index, binding := range bindings {
		analyzeBinding(policy, binding, index, params, subjects, &report)
	}
	return report
}

func analyzeBinding(policy Policy, binding Binding, index int, params []ParamObject, subjects []Subject, report *Report) {
	finding := func(code FindingCode, message string) Finding {
		return Finding{
			Code:         code,
			PolicyName:   policy.Name,
			BindingName:  binding.Name,
			BindingIndex: index,
			Message:      message,
		}
	}

	if binding.ParamRef.Name != "" && binding.ParamRef.Selector != nil {
		f := finding(
			FindingParamRefConflict,
			"paramRef must not set both name and selector",
		)
		report.Findings = append(report.Findings, f)
		report.Rejected = true
		return
	}

	if !containsAction(binding.ValidationActions, ValidationActionDeny) {
		report.Findings = append(report.Findings, finding(
			FindingNoDenyAction,
			"validationActions does not contain Deny, so the binding cannot reject requests",
		))
		report.Allowed = true
	}

	if policy.FailurePolicy == FailurePolicyIgnore {
		for _, validation := range policy.Validations {
			if !validation.MayError {
				continue
			}
			f := finding(
				FindingExpressionErrorAllowed,
				"failurePolicy is Ignore and validation expression may fail to evaluate",
			)
			f.ExpressionName = validation.Name
			report.Findings = append(report.Findings, f)
			report.Allowed = true
		}
		for _, condition := range policy.MatchConditions {
			if !condition.MayError {
				continue
			}
			f := finding(
				FindingExpressionErrorAllowed,
				"failurePolicy is Ignore and match condition may fail to evaluate",
			)
			f.ExpressionName = condition.Name
			report.Findings = append(report.Findings, f)
			report.Allowed = true
		}
	}

	var selected []ParamObject
	switch {
	case binding.ParamRef.Name != "":
		for _, param := range params {
			if param.Name == binding.ParamRef.Name {
				selected = append(selected, param)
			}
		}
		if len(selected) == 0 {
			if binding.ParameterNotFoundAction == ParameterNotFoundAllow {
				f := finding(
					FindingParameterMissingAllowed,
					"referenced parameter object is missing and parameterNotFoundAction is Allow",
				)
				f.ParameterName = binding.ParamRef.Name
				report.Findings = append(report.Findings, f)
				report.Allowed = true
			}
			return
		}
	case binding.ParamRef.Selector != nil:
		for _, param := range params {
			if binding.ParamRef.Selector.Matches(param.Labels) {
				selected = append(selected, param)
			}
		}
		if len(selected) == 0 {
			if binding.ParameterNotFoundAction == ParameterNotFoundAllow {
				report.Findings = append(report.Findings, finding(
					FindingSelectorMatchedNothingAllowed,
					"paramRef selector matched zero parameter objects and parameterNotFoundAction is Allow",
				))
				report.Allowed = true
			}
			return
		}
	default:
		return
	}

	if binding.ParameterNotFoundAction != ParameterNotFoundAllow {
		return
	}

	for _, param := range selected {
		for _, subject := range subjects {
			if !subject.Restricted || !subject.CanDelete(param.Name) {
				continue
			}
			f := finding(
				FindingParamDeletableByRestricted,
				fmt.Sprintf(
					"restricted subject %q can delete parameter object %q, which makes parameterNotFoundAction Allow reachable",
					subject.Name,
					param.Name,
				),
			)
			f.SubjectName = subject.Name
			f.ParameterName = param.Name
			report.Findings = append(report.Findings, f)
			report.Bypassable = true
		}
	}
}

func containsAction(actions []ValidationAction, wanted ValidationAction) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
