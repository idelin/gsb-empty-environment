package plancache

import "fmt"

// checkBindings enforces structured, positional parameter binding. Parameter
// values are checked at execution time and never enter the CacheKey.
func checkBindings(plan *Plan, bindings []any) error {
	want := len(plan.Key.ParamTypes)
	if len(bindings) != want {
		return fmt.Errorf("%w: got %d values, want %d", ErrBinding, len(bindings), want)
	}
	for i, value := range bindings {
		if !bindingMatches(plan.Key.ParamTypes[i], value) {
			return fmt.Errorf("%w: param %d expected %s got %T",
				ErrBinding, i, plan.Key.ParamTypes[i], value)
		}
	}
	return nil
}

func bindingMatches(paramType string, value any) bool {
	if value == nil {
		return true // NULL is structurally valid for any type
	}
	switch paramType {
	case "int", "int64":
		_, ok := value.(int64)
		return ok
	case "text":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "float64":
		_, ok := value.(float64)
		return ok
	default:
		// Unknown declared types accept any value at this layer; host
		// adapters plug in richer type systems.
		return true
	}
}
