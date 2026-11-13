package netsec

import (
	"fmt"
	"reflect"
)

// BoundValue is one structurally typed bind value. Values are supplied at
// execution time only and never participate in plan identity.
type BoundValue struct {
	Type  ParamType
	Value any
}

// BoundParameters is an immutable list of typed values. The slice is copied on
// construction and on access.
type BoundParameters struct {
	values []BoundValue
}

// NewBoundParameters builds parameters from explicit typed values.
func NewBoundParameters(values ...BoundValue) BoundParameters {
	return BoundParameters{values: append([]BoundValue(nil), values...)}
}

// BindParameters binds untyped values to the declared wire types. Every value
// is checked immediately; execution therefore always uses typed binding.
func BindParameters(types []ParamType, values []any) (BoundParameters, error) {
	if len(types) != len(values) {
		return BoundParameters{}, &ParameterError{
			Index:  minInt(len(types), len(values)),
			Reason: fmt.Sprintf("declared %d types for %d values", len(types), len(values)),
		}
	}
	out := make([]BoundValue, len(values))
	for i := range values {
		if err := validateBindValue(types[i], values[i]); err != nil {
			return BoundParameters{}, &ParameterError{Index: i, Expected: types[i], Reason: err.Error()}
		}
		out[i] = BoundValue{Type: types[i], Value: values[i]}
	}
	return BoundParameters{values: out}, nil
}

// Bind is a variadic convenience wrapper around BindParameters.
func Bind(types []ParamType, values ...any) (BoundParameters, error) {
	return BindParameters(types, values)
}

// Values returns a copy of the typed bind list.
func (p BoundParameters) Values() []BoundValue {
	return append([]BoundValue(nil), p.values...)
}

// Len returns the number of bind parameters.
func (p BoundParameters) Len() int { return len(p.values) }

// Validate checks this bind list against the type list that was compiled.
func (p BoundParameters) Validate(types []ParamType) error {
	if len(p.values) != len(types) {
		return &ParameterError{
			Index:  minInt(len(types), len(p.values)),
			Reason: fmt.Sprintf("plan expects %d parameters, got %d", len(types), len(p.values)),
		}
	}
	for i := range types {
		actual := p.values[i].Type
		if actual != "" && !paramTypeCompatible(types[i], actual) {
			return &ParameterError{Index: i, Expected: types[i], Actual: actual, Reason: "bound type is incompatible"}
		}
		if err := validateBindValue(types[i], p.values[i].Value); err != nil {
			return &ParameterError{Index: i, Expected: types[i], Reason: err.Error()}
		}
	}
	return nil
}

func validateBindValue(typ ParamType, value any) error {
	if value == nil {
		if typ == ParamAny {
			return nil
		}
		return fmt.Errorf("nil value")
	}
	rv := reflect.ValueOf(value)
	switch typ {
	case ParamAny:
		return nil
	case ParamInt, ParamInt32, ParamInt64:
		if rv.Kind() >= reflect.Int && rv.Kind() <= reflect.Int64 {
			if rv.Type().Bits() < 8 {
				return fmt.Errorf("signed integer required")
			}
			return nil
		}
		return fmt.Errorf("signed integer required")
	case ParamUint:
		if rv.Kind() >= reflect.Uint && rv.Kind() <= reflect.Uint64 {
			return nil
		}
		return fmt.Errorf("unsigned integer required")
	case ParamFloat64:
		if rv.Kind() == reflect.Float32 || rv.Kind() == reflect.Float64 {
			return nil
		}
		return fmt.Errorf("floating point required")
	case ParamText, ParamString:
		if rv.Kind() == reflect.String {
			return nil
		}
		return fmt.Errorf("string required")
	case ParamBool:
		if rv.Kind() == reflect.Bool {
			return nil
		}
		return fmt.Errorf("bool required")
	case ParamBytes:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		return fmt.Errorf("byte slice required")
	default:
		// Extension wire types are still structurally typed. The declared
		// type name remains part of the plan's parameter type digest.
		return nil
	}
}

func paramTypeCompatible(expected, actual ParamType) bool {
	if expected == actual || expected == ParamAny {
		return true
	}
	if expected == ParamText && actual == ParamString ||
		expected == ParamString && actual == ParamText {
		return true
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
