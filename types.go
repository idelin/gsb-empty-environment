package netsec

// FailurePolicy mirrors ValidatingAdmissionPolicySpec.failurePolicy.
type FailurePolicy string

const (
	// FailurePolicyFail rejects a request when an expression cannot be
	// evaluated.
	FailurePolicyFail FailurePolicy = "Fail"
	// FailurePolicyIgnore allows a request when an expression cannot be
	// evaluated.
	FailurePolicyIgnore FailurePolicy = "Ignore"
)

// ParameterNotFoundAction mirrors
// ValidatingAdmissionPolicyBindingSpec.parameterNotFoundAction.
type ParameterNotFoundAction string

const (
	// ParameterNotFoundDeny rejects the request when the referenced parameter
	// object does not exist.
	ParameterNotFoundDeny ParameterNotFoundAction = "Deny"
	// ParameterNotFoundAllow allows the request when the referenced parameter
	// object does not exist.
	ParameterNotFoundAllow ParameterNotFoundAction = "Allow"
)

// ValidationAction is one of the actions in
// ValidatingAdmissionPolicyBindingSpec.validationActions.
type ValidationAction string

const (
	ValidationActionDeny  ValidationAction = "Deny"
	ValidationActionWarn  ValidationAction = "Warn"
	ValidationActionAudit ValidationAction = "Audit"
)

// Policy contains the fields of a validating admission policy that determine
// whether an invalid or failing evaluation can let a request through.
type Policy struct {
	// Name is used to identify the policy in findings.
	Name string
	// FailurePolicy controls what happens when an expression fails to
	// evaluate. An empty value is treated as Fail.
	FailurePolicy FailurePolicy
	// Validations lists the policy's validation expressions.
	Validations []Validation
	// MatchConditions lists the expressions that decide whether the policy
	// applies to a request.
	MatchConditions []MatchCondition
}

// Validation describes one validation expression.
type Validation struct {
	Name string
	// Expression is the optional CEL source. It is informational and is never
	// evaluated by this package.
	Expression string
	// MayError is the static result of checking whether the expression can
	// fail to evaluate. It should include type errors, missing fields,
	// division by zero, and similar runtime failures.
	MayError bool
}

// MatchCondition describes one match condition expression.
type MatchCondition struct {
	Name       string
	Expression string
	// MayError reports whether evaluating this condition can fail.
	MayError bool
}

// ParamRef identifies the parameter object used by a binding. Exactly one of
// Name and Selector should be set. A zero-value ParamRef means that the
// binding does not reference a parameter object.
type ParamRef struct {
	Name     string
	Selector *LabelSelector
}

// LabelSelector is the subset of metav1.LabelSelector needed to determine
// whether a parameter object is selected.
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []LabelSelectorRequirement
}

// LabelSelectorOperator is a metav1.LabelSelectorOperator.
type LabelSelectorOperator string

const (
	LabelSelectorIn           LabelSelectorOperator = "In"
	LabelSelectorNotIn        LabelSelectorOperator = "NotIn"
	LabelSelectorExists       LabelSelectorOperator = "Exists"
	LabelSelectorDoesNotExist LabelSelectorOperator = "DoesNotExist"
)

// LabelSelectorRequirement is one requirement in a label selector.
type LabelSelectorRequirement struct {
	Key      string
	Operator LabelSelectorOperator
	Values   []string
}

// Binding contains the fields of a validating admission policy binding that
// affect enforcement.
type Binding struct {
	Name                    string
	ParamRef                ParamRef
	ParameterNotFoundAction ParameterNotFoundAction
	ValidationActions       []ValidationAction
}

// ParamObject is an existing parameter resource. The package only needs its
// identity and labels.
type ParamObject struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// Subject describes a principal and its delete permissions for parameter
// objects.
type Subject struct {
	Name string
	// Restricted marks principals that are not trusted cluster administrators.
	// The caller is responsible for this classification.
	Restricted bool
	// CanDeleteParams lists parameter object names the subject may delete.
	// The value "*" grants permission for every parameter object.
	CanDeleteParams []string
}

// CanDelete reports whether the subject is allowed to delete the named
// parameter object.
func (s Subject) CanDelete(parameterName string) bool {
	for _, name := range s.CanDeleteParams {
		if name == "*" || name == parameterName {
			return true
		}
	}
	return false
}

// FindingCode identifies a configuration error, an allow path, or a
// bypassability finding.
type FindingCode string

const (
	// FindingParamRefConflict means that paramRef sets both name and selector.
	FindingParamRefConflict FindingCode = "paramref_conflict"
	// FindingParameterMissingAllowed means that a named parameter is absent
	// and parameterNotFoundAction is Allow.
	FindingParameterMissingAllowed FindingCode = "parameter_missing_allowed"
	// FindingSelectorMatchedNothingAllowed means that a selector matched no
	// parameter objects and parameterNotFoundAction is Allow.
	FindingSelectorMatchedNothingAllowed FindingCode = "selector_matched_nothing_allowed"
	// FindingExpressionErrorAllowed means that failurePolicy is Ignore and an
	// expression may fail to evaluate.
	FindingExpressionErrorAllowed FindingCode = "expression_error_allowed"
	// FindingNoDenyAction means that validationActions does not contain Deny,
	// so the binding cannot reject a request.
	FindingNoDenyAction FindingCode = "no_deny_action"
	// FindingParamDeletableByRestricted means that an existing parameter
	// object can be deleted by a restricted subject.
	FindingParamDeletableByRestricted FindingCode = "param_deletable_by_restricted"
)

// Finding is one result of the analysis.
type Finding struct {
	Code         FindingCode
	PolicyName   string
	BindingName  string
	BindingIndex int
	SubjectName  string
	// ParameterName is set when the finding concerns an existing parameter
	// object.
	ParameterName string
	// ExpressionName is set for expression-error findings.
	ExpressionName string
	Message        string
}

// Report is the result of analyzing one or more bindings.
type Report struct {
	// Rejected is true when the policy or binding must not be saved. Currently
	// this means that a paramRef sets both name and selector.
	Rejected bool
	// Allowed is true when at least one condition lets a request through
	// without the policy rejecting it.
	Allowed bool
	// Bypassable is true when a restricted subject can delete an existing
	// parameter object and that deletion would trigger an allow path.
	Bypassable bool
	Findings   []Finding
}

// Has reports whether the report contains a finding with the given code.
func (r Report) Has(code FindingCode) bool {
	for _, finding := range r.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
