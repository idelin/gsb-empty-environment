package netsec

import (
	"fmt"
	"reflect"
)

// resolveValue substitutes variables and returns plain Go values
// (int64, float64, string, bool, nil, []any, map[string]any).
func resolveValue(v Value, variables map[string]any) (any, error) {
	switch node := v.(type) {
	case nil:
		return nil, nil
	case *Variable:
		val, ok := variables[node.Name]
		if !ok {
			return nil, &AdmissionError{
				Code:    ReasonValidation,
				Message: fmt.Sprintf("variable $%s is not defined", node.Name),
			}
		}
		return val, nil
	case *IntValue:
		return node.Value, nil
	case *FloatValue:
		return node.Value, nil
	case *StringValue:
		return node.Value, nil
	case *BooleanValue:
		return node.Value, nil
	case *NullValue:
		return nil, nil
	case *EnumValue:
		return node.Value, nil
	case *ListValue:
		out := make([]any, 0, len(node.Values))
		for _, item := range node.Values {
			rv, err := resolveValue(item, variables)
			if err != nil {
				return nil, err
			}
			out = append(out, rv)
		}
		return out, nil
	case *ObjectValue:
		out := make(map[string]any, len(node.Fields))
		for _, field := range node.Fields {
			rv, err := resolveValue(field.Value, variables)
			if err != nil {
				return nil, err
			}
			out[field.Name] = rv
		}
		return out, nil
	default:
		return nil, &AdmissionError{
			Code:    ReasonValidation,
			Message: fmt.Sprintf("unsupported value node %T", v),
		}
	}
}

// sameArguments compares two argument lists after variable substitution.
// GraphQL fields sharing a response key must have identical arguments.
func sameArguments(a, b []*Argument, variables map[string]any) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	av := make(map[string]any, len(a))
	bv := make(map[string]any, len(b))
	for _, arg := range a {
		v, err := resolveValue(arg.Value, variables)
		if err != nil {
			return false, err
		}
		av[arg.Name] = v
	}
	for _, arg := range b {
		v, err := resolveValue(arg.Value, variables)
		if err != nil {
			return false, err
		}
		bv[arg.Name] = v
	}
	return reflect.DeepEqual(av, bv), nil
}

// directiveIncluded applies the GraphQL @skip and @include semantics.
// Unknown directives are ignored, matching execution behavior. Duplicate
// applications of @skip or @include are invalid per spec.
func directiveIncluded(directives []*Directive, variables map[string]any) (bool, error) {
	seen := map[string]bool{}
	included := true
	for _, dir := range directives {
		switch dir.Name {
		case "skip", "include":
			if seen[dir.Name] {
				return false, &AdmissionError{
					Code:    ReasonValidation,
					Message: fmt.Sprintf("directive @%s may only be used once", dir.Name),
				}
			}
			seen[dir.Name] = true
			flag, err := boolIfArg(dir, variables)
			if err != nil {
				return false, err
			}
			if dir.Name == "skip" && flag {
				included = false
			}
			if dir.Name == "include" && !flag {
				included = false
			}
		default:
			// Non-built-in directives do not affect inclusion.
		}
	}
	return included, nil
}

func boolIfArg(dir *Directive, variables map[string]any) (bool, error) {
	for _, arg := range dir.Arguments {
		if arg.Name != "if" {
			continue
		}
		v, err := resolveValue(arg.Value, variables)
		if err != nil {
			return false, err
		}
		b, ok := v.(bool)
		if !ok {
			return false, &AdmissionError{
				Code: ReasonValidation,
				Message: fmt.Sprintf(
					"directive @%s argument \"if\" must be a boolean",
					dir.Name,
				),
			}
		}
		return b, nil
	}
	return false, &AdmissionError{
		Code:    ReasonValidation,
		Message: fmt.Sprintf("directive @%s requires an \"if\" argument", dir.Name),
	}
}
