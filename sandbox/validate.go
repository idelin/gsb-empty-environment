package sandbox

import "fmt"

// validate statically walks the AST before any evaluation happens:
//   - every function call must be the built-in "eval" or on the tenant whitelist
//   - every identifier must be a declared variable (or the reserved "self")
//   - nesting depth and total node count are bounded
func validate(n *node, funcs, vars map[string]bool, limits Limits) error {
	count := 0
	return validateNode(n, funcs, vars, limits, 0, &count)
}

func validateNode(n *node, funcs, vars map[string]bool, limits Limits, depth int, count *int) error {
	if depth > limits.MaxDepth {
		return fmt.Errorf("%w: nesting deeper than %d levels", ErrValidation, limits.MaxDepth)
	}
	*count++
	if *count > limits.MaxNodes {
		return fmt.Errorf("%w: more than %d AST nodes", ErrValidation, limits.MaxNodes)
	}
	switch n.kind {
	case nCall:
		if n.val != "eval" && !funcs[n.val] {
			return fmt.Errorf("%w: %q", ErrUnauthorizedFunction, n.val)
		}
	case nIdent:
		if !vars[n.val] {
			return fmt.Errorf("%w: unknown variable %q", ErrValidation, n.val)
		}
	}
	for _, a := range n.args {
		if err := validateNode(a, funcs, vars, limits, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}
