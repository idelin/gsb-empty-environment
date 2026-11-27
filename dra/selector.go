package dra

import "fmt"

// deviceView couples a device with the pool coordinates it came from.
type deviceView struct {
	driver string
	pool   string
	device Device
}

func (d deviceView) result() DeviceResult {
	return DeviceResult{Driver: d.driver, Pool: d.pool, Device: d.device.Name}
}

// deviceMatches reports whether a device satisfies every selector (AND).
func deviceMatches(dev Device, selectors []Selector) (bool, error) {
	for _, sel := range selectors {
		ok, err := selectorMatches(dev, sel)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func selectorMatches(dev Device, sel Selector) (bool, error) {
	for _, req := range sel.AttributeRequirements {
		got, ok := dev.Attributes[req.Attribute]
		if !ok {
			return false, nil
		}
		ok, err := attributeMatches(got, req)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	for _, req := range sel.CapacityRequirements {
		got, ok := dev.Capacities[req.Name]
		if !ok {
			return false, nil
		}
		ok, err := capacityMatches(got, req)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func attributeMatches(devVal AttributeValue, req AttributeRequirement) (bool, error) {
	switch req.Op {
	case OpEqual, OpNotEqual:
		eq, err := valuesEqual(devVal, req.Value)
		if err != nil {
			return false, err
		}
		if req.Op == OpEqual {
			return eq, nil
		}
		return !eq, nil
	case OpIn, OpNotIn:
		if devVal.kind != KindString || req.Value.kind != KindString {
			return false, fmt.Errorf("%w: %s requires string operands", ErrInvalidSpec, req.Op)
		}
		found := false
		for _, member := range splitMembers(req.Value.stringV) {
			if member == devVal.stringV {
				found = true
				break
			}
		}
		if req.Op == OpIn {
			return found, nil
		}
		return !found, nil
	case OpLessThan, OpLessEqual, OpGreaterThan, OpGreaterEqual:
		cmp, err := compareValues(devVal, req.Value)
		if err != nil {
			return false, err
		}
		switch req.Op {
		case OpLessThan:
			return cmp < 0, nil
		case OpLessEqual:
			return cmp <= 0, nil
		case OpGreaterThan:
			return cmp > 0, nil
		default:
			return cmp >= 0, nil
		}
	case OpSemverCompatible:
		if devVal.kind != KindVersion || req.Value.kind != KindVersion {
			return false, fmt.Errorf("%w: %s requires version operands", ErrInvalidSpec, req.Op)
		}
		dev, err := parseSemver(devVal.stringV)
		if err != nil {
			return false, err
		}
		want, err := parseSemver(req.Value.stringV)
		if err != nil {
			return false, err
		}
		return semverCompatible(dev, want), nil
	default:
		return false, fmt.Errorf("%w: unsupported attribute operator %q", ErrInvalidSpec, req.Op)
	}
}

func valuesEqual(a, b AttributeValue) (bool, error) {
	if a.kind != b.kind {
		return false, fmt.Errorf("%w: cannot compare %v with %v", ErrInvalidSpec, a.kind, b.kind)
	}
	switch a.kind {
	case KindBool:
		return a.boolV == b.boolV, nil
	case KindInt64:
		return a.intV == b.intV, nil
	default:
		if a.kind == KindVersion {
			av, err := parseSemver(a.stringV)
			if err != nil {
				return false, err
			}
			bv, err := parseSemver(b.stringV)
			if err != nil {
				return false, err
			}
			return compareSemver(av, bv) == 0, nil
		}
		return a.stringV == b.stringV, nil
	}
}

func compareValues(a, b AttributeValue) (int, error) {
	if a.kind != b.kind {
		return 0, fmt.Errorf("%w: cannot order %v against %v", ErrInvalidSpec, a.kind, b.kind)
	}
	switch a.kind {
	case KindInt64:
		switch {
		case a.intV < b.intV:
			return -1, nil
		case a.intV > b.intV:
			return 1, nil
		default:
			return 0, nil
		}
	case KindVersion:
		av, err := parseSemver(a.stringV)
		if err != nil {
			return 0, err
		}
		bv, err := parseSemver(b.stringV)
		if err != nil {
			return 0, err
		}
		return compareSemver(av, bv), nil
	default:
		return 0, fmt.Errorf("%w: operator not defined for kind %v", ErrInvalidSpec, a.kind)
	}
}

func capacityMatches(got int64, req CapacityRequirement) (bool, error) {
	switch req.Op {
	case OpCapacityGte:
		return got >= req.Value, nil
	case OpEqual:
		return got == req.Value, nil
	case OpNotEqual:
		return got != req.Value, nil
	case OpLessThan:
		return got < req.Value, nil
	case OpLessEqual:
		return got <= req.Value, nil
	case OpGreaterThan:
		return got > req.Value, nil
	case OpGreaterEqual:
		return got >= req.Value, nil
	default:
		return false, fmt.Errorf("%w: unsupported capacity operator %q", ErrInvalidSpec, req.Op)
	}
}

// splitMembers splits an In/NotIn value list encoded as "a,b,c".
func splitMembers(raw string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	return out
}
