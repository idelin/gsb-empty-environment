// Package vapfail analyzes ValidatingAdmissionPolicy and its bindings for
// failure paths that silently allow requests, before they are persisted.
package vapfail

// FailurePolicy mirrors the policy's failurePolicy field.
type FailurePolicy string

const (
	FailurePolicyFail   FailurePolicy = "Fail"
	FailurePolicyIgnore FailurePolicy = "Ignore"
)

// ParameterNotFoundAction mirrors the binding's parameterNotFoundAction field.
type ParameterNotFoundAction string

const (
	ParamNotFoundAllow ParameterNotFoundAction = "Allow"
	ParamNotFoundDeny  ParameterNotFoundAction = "Deny"
)

// ValidationAction mirrors entries of the binding's validationActions field.
type ValidationAction string

const (
	ActionDeny  ValidationAction = "Deny"
	ActionWarn  ValidationAction = "Warn"
	ActionAudit ValidationAction = "Audit"
)

// Validation is a single CEL validation expression of the policy.
type Validation struct {
	Name string
	// MayError is a static flag indicating the expression can fail at
	// runtime (e.g. unchecked map access, division, type conversion).
	MayError bool
}

// MatchConditions describes which requests the policy matches.
type MatchConditions struct {
	ResourceKinds []string
	Namespaces    []string
}

// Policy is the ValidatingAdmissionPolicy under analysis.
type Policy struct {
	Name          string
	FailurePolicy FailurePolicy
	Validations   []Validation
	Match         MatchConditions
}

// ParamRef is the binding's reference to the parameter resource.
type ParamRef struct {
	Name      string
	Namespace string
	Selector  map[string]string
}

// Binding is the ValidatingAdmissionPolicyBinding under analysis.
type Binding struct {
	Name                    string
	PolicyName              string
	ParamRef                ParamRef
	ParameterNotFoundAction ParameterNotFoundAction
	ValidationActions       []ValidationAction
}

// ParamObject is an existing object of the parameter resource.
type ParamObject struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// SubjectDeletePermission grants a subject delete access to parameter
// objects. Objects lists parameter object names; "*" matches all.
type SubjectDeletePermission struct {
	Subject string
	// Restricted marks subjects that must not be able to weaken admission
	// enforcement (non-admin workloads, service accounts, ...).
	Restricted bool
	Objects    []string
}

// FindingCode identifies a reported failure path.
type FindingCode string

const (
	// CodeParamRefAmbiguous: paramRef sets both name and selector; rejected.
	CodeParamRefAmbiguous FindingCode = "PARAM_REF_AMBIGUOUS"
	// CodeParamMissingAllow: named parameter is absent and the missing
	// parameter action is Allow, so requests pass unevaluated.
	CodeParamMissingAllow FindingCode = "PARAM_MISSING_ALLOW"
	// CodeSelectorZeroMatchAllow: the selector matches no existing object
	// and the missing parameter action is Allow.
	CodeSelectorZeroMatchAllow FindingCode = "SELECTOR_ZERO_MATCH_ALLOW"
	// CodeIgnoreExpressionError: failurePolicy Ignore plus an expression
	// that may error lets failing evaluations pass.
	CodeIgnoreExpressionError FindingCode = "IGNORE_EXPRESSION_ERROR"
	// CodeNoDenyAction: validationActions lack Deny, so violations are
	// never enforced.
	CodeNoDenyAction FindingCode = "NO_DENY_ACTION"
	// CodeParamDeletableBypass: a restricted subject can delete a parameter
	// object the binding depends on, turning the policy into a no-op.
	CodeParamDeletableBypass FindingCode = "PARAM_DELETABLE_BYPASS"
)

// Finding is a single reported failure path.
type Finding struct {
	Code    FindingCode
	Message string
}

// Report is the outcome of Analyze.
type Report struct {
	// Rejected is true when the binding is structurally invalid
	// (paramRef sets both name and selector) and must not be saved.
	Rejected bool
	// Bypassable is true when a restricted subject can delete a parameter
	// object the binding relies on.
	Bypassable bool
	Findings   []Finding
}

// Has reports whether a finding with the given code is present.
func (r Report) Has(code FindingCode) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
