package netsec

import (
	"fmt"
	"sort"
)

// Transition is a field-oriented facade for one secure hop.  The set
// arguments accept []string, map[string]bool, or map[string]struct{}.
func Transition(
	currentLabels map[string]string,
	currentTransitive any,
	requestedLabels map[string]string,
	requestedTransitive any,
	fixedLabels map[string]string,
	overrideKeys any,
	depth int,
	roleID string,
) (*HopResult, error) {
	curSet, _, err := keyListFromAny(currentTransitive)
	if err != nil {
		return nil, err
	}
	reqSet, _, err := keyListFromAny(requestedTransitive)
	if err != nil {
		return nil, err
	}
	override, _, err := keyListFromAny(overrideKeys)
	if err != nil {
		return nil, err
	}
	return ApplyHop(HopRequest{
		CurrentLabels:       currentLabels,
		CurrentTransitive:   curSet,
		RequestedLabels:     requestedLabels,
		RequestedTransitive: reqSet,
		FixedLabels:         fixedLabels,
		OverrideKeys:        override,
		Depth:               depth,
		RoleID:              roleID,
	})
}

// ApplyFields is an alias for Transition.
func ApplyFields(
	currentLabels map[string]string,
	currentTransitive any,
	requestedLabels map[string]string,
	requestedTransitive any,
	fixedLabels map[string]string,
	overrideKeys any,
	depth int,
	roleID string,
) (*HopResult, error) {
	return Transition(currentLabels, currentTransitive, requestedLabels, requestedTransitive, fixedLabels, overrideKeys, depth, roleID)
}

// Advance is the session-returning form of Transition.
func Advance(
	currentLabels map[string]string,
	currentTransitive any,
	requestedLabels map[string]string,
	requestedTransitive any,
	fixedLabels map[string]string,
	overrideKeys any,
	depth int,
	roleID string,
) (*Session, error) {
	res, err := Transition(currentLabels, currentTransitive, requestedLabels, requestedTransitive, fixedLabels, overrideKeys, depth, roleID)
	if err != nil {
		return nil, err
	}
	return res.Session, nil
}

// NextState is an alias for Advance.
func NextState(
	currentLabels map[string]string,
	currentTransitive any,
	requestedLabels map[string]string,
	requestedTransitive any,
	fixedLabels map[string]string,
	overrideKeys any,
	depth int,
	roleID string,
) (*Session, error) {
	return Advance(currentLabels, currentTransitive, requestedLabels, requestedTransitive, fixedLabels, overrideKeys, depth, roleID)
}

func keyListFromAny(v any) ([]string, bool, error) {
	switch keys := v.(type) {
	case nil:
		return nil, false, nil
	case []string:
		return keys, true, nil
	case []any:
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			s, ok := k.(string)
			if !ok {
				return nil, false, fmt.Errorf("%w: set element %T is not a string", ErrInvalidInput, k)
			}
			out = append(out, s)
		}
		return out, true, nil
	case map[string]bool:
		return listFromSet(keys, false), true, nil
	case map[string]struct{}:
		out := make([]string, 0, len(keys))
		for k := range keys {
			out = append(out, k)
		}
		sort.Strings(out)
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("%w: unsupported set type %T", ErrInvalidInput, v)
	}
}
