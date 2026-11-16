package netsec

import "fmt"

// ViolationError reports a query that could override or strip the enforced
// tenant label and must therefore be rejected wholesale.
type ViolationError struct {
	Reason string
}

func (e *ViolationError) Error() string {
	return "tenant rewrite refused: " + e.Reason
}

// Rewrite enforces tenantID on every vector selector in expr.
//
// Every existing matcher on the tenant label is removed and replaced by a
// single tenant="tenantID" equality matcher, including selectors nested in
// function arguments, range selectors and subqueries. Queries without any
// vector selector are returned unchanged.
//
// The query is rejected when it can mutate or strip the tenant label:
// label_replace/label_join writing tenant, or tenant appearing in any by,
// without, on, ignoring, group_left or group_right label list. Validation
// runs before mutation, so a rejected tree is never partially rewritten.
func Rewrite(expr Expr, tenantID string) (Expr, error) {
	if expr == nil {
		return nil, nil
	}
	if tenantID == "" {
		return nil, &ViolationError{Reason: "empty tenant identifier"}
	}
	if err := validate(expr); err != nil {
		return nil, err
	}
	enforce(expr, tenantID)
	return expr, nil
}

// walk visits every expression reachable from expr.
func walk(expr Expr, visit func(Expr)) {
	if expr == nil {
		return
	}
	visit(expr)
	switch node := expr.(type) {
	case *VectorSelector:
		// Leaf: matchers are not expressions.
	case *RangeSelector:
		walk(node.Vector, visit)
	case *SubqueryExpr:
		walk(node.Expr, visit)
	case *NumberLiteral, *StringLiteral:
		// Leaf.
	case *Call:
		for _, arg := range node.Args {
			walk(arg, visit)
		}
	case *AggregateExpr:
		walk(node.Param, visit)
		walk(node.Expr, visit)
	case *BinaryExpr:
		walk(node.LHS, visit)
		walk(node.RHS, visit)
	default:
		panic(fmt.Sprintf("netsec: unsupported expression type %T", expr))
	}
}

func validate(expr Expr) error {
	var violation error
	walk(expr, func(node Expr) {
		if violation != nil {
			return
		}
		switch current := node.(type) {
		case *AggregateExpr:
			for _, label := range current.Grouping {
				if label == TenantLabel {
					clause := "by"
					if current.Without {
						clause = "without"
					}
					violation = &ViolationError{
						Reason: fmt.Sprintf("aggregation %s clause references %s", clause, TenantLabel),
					}
					return
				}
			}
		case *BinaryExpr:
			if current.Matching == nil {
				return
			}
			if hasTenantLabel(current.Matching.On) {
				violation = &ViolationError{Reason: "on clause references " + TenantLabel}
				return
			}
			if hasTenantLabel(current.Matching.Ignoring) {
				violation = &ViolationError{Reason: "ignoring clause references " + TenantLabel}
				return
			}
			if hasTenantLabel(current.Matching.GroupLeft) {
				violation = &ViolationError{Reason: "group_left clause references " + TenantLabel}
				return
			}
			if hasTenantLabel(current.Matching.GroupRight) {
				violation = &ViolationError{Reason: "group_right clause references " + TenantLabel}
				return
			}
		case *Call:
			switch current.Func {
			case "label_replace", "label_join":
				if len(current.Args) > 1 {
					if literal, ok := current.Args[1].(*StringLiteral); ok && literal.Value == TenantLabel {
						violation = &ViolationError{
							Reason: current.Func + " writes destination label " + TenantLabel,
						}
					}
				}
			}
		}
	})
	return violation
}

func hasTenantLabel(labels []string) bool {
	for _, label := range labels {
		if label == TenantLabel {
			return true
		}
	}
	return false
}

func enforce(expr Expr, tenantID string) {
	walk(expr, func(node Expr) {
		selector, ok := node.(*VectorSelector)
		if !ok {
			return
		}
		kept := make([]*Matcher, 0, len(selector.Matchers)+1)
		for _, matcher := range selector.Matchers {
			if matcher == nil || matcher.Label != TenantLabel {
				kept = append(kept, matcher)
			}
		}
		selector.Matchers = append(kept, &Matcher{
			Label: TenantLabel,
			Op:    OpEqual,
			Value: tenantID,
		})
	})
}
