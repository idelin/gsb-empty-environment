package netsec

import (
	"errors"
	"strconv"
)

// ReasonCode classifies why an admission decision was denied.
type ReasonCode string

const (
	// ReasonBudget means the worst-case cost upper bound exceeds Budget.
	ReasonBudget ReasonCode = "BUDGET_EXCEEDED"
	// ReasonPermission means a field requires a scope the caller lacks.
	ReasonPermission ReasonCode = "PERMISSION_DENIED"
	// ReasonValidation means the query is structurally invalid against the
	// finite schema (unknown fields, fragment cycles, conflicts, etc.).
	ReasonValidation ReasonCode = "VALIDATION_ERROR"
)

// AdmissionError carries a machine-readable code together with a human message
// and the AST/schema path that triggered it.
type AdmissionError struct {
	Code ReasonCode
	// Message describes the failure.
	Message string
	// SchemaPath is the real schema path (field names, never aliases) when
	// the failure is attached to a specific field.
	SchemaPath []string
}

func (e *AdmissionError) Error() string {
	if len(e.SchemaPath) == 0 {
		return string(e.Code) + ": " + e.Message
	}
	return string(e.Code) + ": " + e.Message + " at " + joinPath(e.SchemaPath)
}

// Is reports errors with the same reason code so errors.Is works.
func (e *AdmissionError) Is(target error) bool {
	var other *AdmissionError
	if errors.As(target, &other) {
		return other.Code == e.Code
	}
	return false
}

// Options configures an admission decision.
type Options struct {
	// Schema is the finite schema the query is validated against.
	Schema *Schema
	// Document is the parsed query AST.
	Document *Document
	// OperationName selects the operation to admit. Empty is required when
	// the document contains exactly one operation.
	OperationName string
	// Variables holds the request variables used for directives and
	// argument-based conflict checks.
	Variables map[string]any
	// GrantedScopes is the set of permission scopes held by the caller.
	GrantedScopes map[string]bool
	// Budget is the inclusive worst-case cost budget. An upper bound greater
	// than Budget is denied. Negative budgets are treated as zero.
	Budget int64
}

// Contribution explains the cost charged for one merged field group at one
// concrete object type.
type Contribution struct {
	// ResponsePath is the path of response keys (aliases honored) in the
	// response document.
	ResponsePath []string
	// SchemaPath is the path of real schema field names. Aliases never
	// appear here, so it is suitable for audit and permission decisions.
	SchemaPath []string
	// ObjectType is the concrete object type that owns the field group.
	ObjectType string
	// Field is the real schema field name.
	Field string
	// Multiplier is the product of the enclosing list maximum lengths.
	Multiplier int64
	// Invocations is the worst-case number of resolver invocations after
	// response-key merging: Multiplier times the field's own list
	// dimensionality product.
	Invocations int64
	// UnitCost is the field's per-instance cost.
	UnitCost int64
	// Cost is the total cost charged: Invocations * UnitCost, saturated at
	// the capped sentinel on overflow.
	Cost int64
}

// Report is the admission decision. It is returned for every query that can
// be structurally evaluated; validation failures return an error instead.
type Report struct {
	// Admitted reports whether the query is allowed to execute.
	Admitted bool
	// Reason is empty when Admitted is true.
	Reason ReasonCode
	// Message details a denial.
	Message string
	// UpperBound is the exact worst-case cost upper bound.
	UpperBound int64
	// Budget is the effective budget applied.
	Budget int64
	// Contributions breaks UpperBound down by merged field group in
	// deterministic traversal order.
	Contributions []*Contribution
}

// Admit computes the exact worst-case cost upper bound of the selected
// operation and decides whether it fits the caller's budget and permissions.
//
// Structurally invalid queries (unknown fields, fragment cycles, response-key
// conflicts, malformed directives, fragment type mismatches) return a
// VALIDATION_ERROR AdmissionError. Permission failures return a
// PERMISSION_DENIED error. Budget failures return a Report with
// Admitted == false and Reason == ReasonBudget.
func Admit(opts Options) (*Report, error) {
	a, err := newAdmitter(opts)
	if err != nil {
		return nil, err
	}
	return a.admit()
}

// maxCost is the saturation sentinel used when integer arithmetic overflows.
// It is deliberately far above any realistic budget.
const maxCost = int64(1<<62 - 1)

func satMul(x, y int64) int64 {
	if x <= 0 || y <= 0 {
		return 0
	}
	if x > maxCost/y {
		return maxCost
	}
	return x * y
}

func satAdd(x, y int64) int64 {
	if x > maxCost-y {
		return maxCost
	}
	return x + y
}

func joinPath(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
