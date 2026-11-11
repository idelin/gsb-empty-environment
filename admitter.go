package netsec

import (
	"fmt"
)

// collectedField is one member of a response-key group.
type collectedField struct {
	field      *Field
	parentType *Type
}

// fieldGroup merges all field AST nodes that share a response key at one
// parent object type.
type fieldGroup struct {
	key        string
	parentType *Type
	members    []*collectedField
}

type admitter struct {
	schema        *Schema
	doc           *Document
	operationName string
	variables     map[string]any
	scopes        map[string]bool
	budget        int64
}

func newAdmitter(opts Options) (*admitter, error) {
	if opts.Schema == nil {
		return nil, &AdmissionError{Code: ReasonValidation, Message: "schema is required"}
	}
	if opts.Document == nil {
		return nil, &AdmissionError{Code: ReasonValidation, Message: "document is required"}
	}
	budget := opts.Budget
	if budget < 0 {
		budget = 0
	}
	scopes := opts.GrantedScopes
	if scopes == nil {
		scopes = map[string]bool{}
	}
	return &admitter{
		schema:        opts.Schema,
		doc:           opts.Document,
		operationName: opts.OperationName,
		variables:     opts.Variables,
		scopes:        scopes,
		budget:        budget,
	}, nil
}

func (a *admitter) admit() (*Report, error) {
	op, err := a.selectOperation()
	if err != nil {
		return nil, err
	}
	if err := a.validateFragments(); err != nil {
		return nil, err
	}

	rootTypeName, err := a.rootTypeName(op.Type)
	if err != nil {
		return nil, err
	}
	rootType, ok := a.schema.Types[rootTypeName]
	if !ok {
		return nil, &AdmissionError{
			Code:    ReasonValidation,
			Message: fmt.Sprintf("root type %q is not in the schema", rootTypeName),
		}
	}

	report := &Report{Admitted: true, Budget: a.budget}
	var contribs []*Contribution
	bound, err := a.evaluateType(rootType, op.SelectionSet, 1, nil, nil, &contribs)
	if err != nil {
		return nil, err
	}
	report.Contributions = contribs
	report.UpperBound = bound
	if bound > a.budget {
		report.Admitted = false
		report.Reason = ReasonBudget
		report.Message = fmt.Sprintf(
			"worst-case cost %s exceeds budget %s",
			itoa(bound), itoa(a.budget),
		)
	}
	return report, nil
}

func (a *admitter) selectOperation() (*Operation, error) {
	switch {
	case len(a.doc.Operations) == 0:
		return nil, &AdmissionError{Code: ReasonValidation, Message: "document contains no operation"}
	case a.operationName != "":
		for _, op := range a.doc.Operations {
			if op.Name == a.operationName {
				if op.Type == OpSubscription {
					return nil, &AdmissionError{
						Code:    ReasonValidation,
						Message: "subscription operations are not admitted",
					}
				}
				return op, nil
			}
		}
		return nil, &AdmissionError{
			Code:    ReasonValidation,
			Message: fmt.Sprintf("operation %q not found", a.operationName),
		}
	default:
		if len(a.doc.Operations) != 1 {
			return nil, &AdmissionError{
				Code:    ReasonValidation,
				Message: "operation name is required when the document has multiple operations",
			}
		}
		op := a.doc.Operations[0]
		if op.Type == OpSubscription {
			return nil, &AdmissionError{
				Code:    ReasonValidation,
				Message: "subscription operations are not admitted",
			}
		}
		return op, nil
	}
}

func (a *admitter) rootTypeName(kind OperationType) (string, error) {
	switch kind {
	case OpQuery:
		if a.schema.QueryType == "" {
			return "", &AdmissionError{Code: ReasonValidation, Message: "schema has no query root"}
		}
		return a.schema.QueryType, nil
	case OpMutation:
		if a.schema.MutationType == "" {
			return "", &AdmissionError{Code: ReasonValidation, Message: "schema has no mutation root"}
		}
		return a.schema.MutationType, nil
	default:
		return "", &AdmissionError{
			Code:    ReasonValidation,
			Message: fmt.Sprintf("unsupported operation type %q", kind),
		}
	}
}
