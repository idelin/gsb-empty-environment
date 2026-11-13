package netsec

import "fmt"

// ReferenceHop and SafeHop are aliases for the secure model.
func ReferenceHop(req HopRequest) (*HopResult, error) { return ApplyHop(req) }
func SafeHop(req HopRequest) (*HopResult, error)      { return ApplyHop(req) }
func ModelHop(req HopRequest) (*HopResult, error)     { return ApplyHop(req) }

// AuditedApplyHop is an alias for the default audited implementation.
func AuditedApplyHop(req HopRequest) (*HopResult, error) { return AuditedHop(req) }

// FindWitness and SearchEscalation are aliases for FindEscalation.
func FindWitness(start any, candidates any, args ...any) (*Witness, error) {
	return FindEscalation(start, candidates, args...)
}

func SearchEscalation(start any, candidates any, args ...any) (*Witness, error) {
	return FindEscalation(start, candidates, args...)
}

func FindShortestEscalation(start any, candidates any, args ...any) (*Witness, error) {
	return FindEscalation(start, candidates, args...)
}

// SearchScenario runs the differential search over a Scenario's generated
// candidate space.
func SearchScenario(sc Scenario) (*Witness, error) {
	return FindEscalation(sc, sc)
}

// FindEscalationFromScenario is an alias for SearchScenario.
func FindEscalationFromScenario(sc Scenario) (*Witness, error) {
	return SearchScenario(sc)
}

// ApplyRequest accepts a HopRequest as an interface value, which is useful for
// table-driven callers that also pass richer input forms.
func ApplyRequest(v any) (*HopResult, error) {
	switch req := v.(type) {
	case HopRequest:
		return ApplyHop(req)
	case *HopRequest:
		if req == nil {
			return nil, fmt.Errorf("%w: nil request", ErrInvalidInput)
		}
		return ApplyHop(*req)
	default:
		return nil, fmt.Errorf("%w: unsupported request type %T", ErrInvalidInput, v)
	}
}
