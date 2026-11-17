package vapfail

import "fmt"

// Analyze inspects a policy/binding pair before it is saved and reports the
// conditions under which matching requests would be allowed without
// enforcement. params are the currently existing objects of the parameter
// resource; deletePerms describe which subjects may delete them.
func Analyze(policy Policy, binding Binding, params []ParamObject, deletePerms []SubjectDeletePermission) Report {
	var report Report

	ref := binding.ParamRef
	hasName := ref.Name != ""
	hasSelector := len(ref.Selector) > 0

	if hasName && hasSelector {
		report.Rejected = true
		report.Findings = append(report.Findings, Finding{
			Code:    CodeParamRefAmbiguous,
			Message: fmt.Sprintf("binding %q: paramRef sets both name %q and a selector; refused", binding.Name, ref.Name),
		})
		return report
	}

	if !hasDeny(binding.ValidationActions) {
		report.Findings = append(report.Findings, Finding{
			Code:    CodeNoDenyAction,
			Message: fmt.Sprintf("binding %q: validationActions %v do not include Deny; violations are never enforced", binding.Name, binding.ValidationActions),
		})
	}

	var dependent []ParamObject
	switch {
	case hasName:
		obj, found := findParam(params, ref.Name, ref.Namespace)
		if !found {
			if binding.ParameterNotFoundAction == ParamNotFoundAllow {
				report.Findings = append(report.Findings, Finding{
					Code:    CodeParamMissingAllow,
					Message: fmt.Sprintf("binding %q: parameter %q not found and parameterNotFoundAction is Allow; requests pass unevaluated", binding.Name, ref.Name),
				})
			}
		} else {
			dependent = append(dependent, obj)
		}
	case hasSelector:
		dependent = selectParams(params, ref.Selector, ref.Namespace)
		if len(dependent) == 0 && binding.ParameterNotFoundAction == ParamNotFoundAllow {
			report.Findings = append(report.Findings, Finding{
				Code:    CodeSelectorZeroMatchAllow,
				Message: fmt.Sprintf("binding %q: paramRef selector matches zero parameter objects and parameterNotFoundAction is Allow; requests pass unevaluated", binding.Name),
			})
		}
	}

	if policy.FailurePolicy == FailurePolicyIgnore {
		for _, v := range policy.Validations {
			if v.MayError {
				report.Findings = append(report.Findings, Finding{
					Code:    CodeIgnoreExpressionError,
					Message: fmt.Sprintf("policy %q: validation %q may error and failurePolicy is Ignore; erroring evaluations are allowed", policy.Name, v.Name),
				})
			}
		}
	}

	for _, obj := range dependent {
		for _, subj := range restrictedDeleters(deletePerms, obj.Name) {
			report.Bypassable = true
			report.Findings = append(report.Findings, Finding{
				Code:    CodeParamDeletableBypass,
				Message: fmt.Sprintf("binding %q: restricted subject %q can delete parameter object %q; deleting it turns the policy into a no-op", binding.Name, subj, obj.Name),
			})
		}
	}

	return report
}

func hasDeny(actions []ValidationAction) bool {
	for _, a := range actions {
		if a == ActionDeny {
			return true
		}
	}
	return false
}

func findParam(params []ParamObject, name, namespace string) (ParamObject, bool) {
	for _, p := range params {
		if p.Name == name && p.Namespace == namespace {
			return p, true
		}
	}
	return ParamObject{}, false
}

func selectParams(params []ParamObject, selector map[string]string, namespace string) []ParamObject {
	var matched []ParamObject
	for _, p := range params {
		if p.Namespace != namespace {
			continue
		}
		if labelsMatch(p.Labels, selector) {
			matched = append(matched, p)
		}
	}
	return matched
}

func labelsMatch(labels, selector map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func restrictedDeleters(perms []SubjectDeletePermission, object string) []string {
	var subjects []string
	for _, p := range perms {
		if !p.Restricted {
			continue
		}
		for _, o := range p.Objects {
			if o == "*" || o == object {
				subjects = append(subjects, p.Subject)
				break
			}
		}
	}
	return subjects
}
