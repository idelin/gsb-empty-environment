package msglang

// traceToDerivation converts an automaton-local proof trace into a parsing
// derivation with postorder reduction steps.
func traceToDerivation(a *Automaton, tr *trace) *Derivation {
	steps := make([]Step, 0, tr.cost)
	var walk func(*trace)
	walk = func(current *trace) {
		if current.leaf {
			rule := current.leafRule
			steps = append(steps, Step{
				NodeLabel: rule.Label,
				Leaf: &Leaf{
					Kind:  rule.Kind,
					Value: append([]byte(nil), current.node.Leaf.Value...),
				},
				State: rule.State,
				Exact: rule.Exact,
			})
			return
		}
		for _, child := range current.children {
			walk(child)
		}
		steps = append(steps, Step{
			NodeLabel: current.rule.Label,
			Children:  append([]string(nil), current.rule.Child...),
			State:     current.rule.State,
		})
	}
	walk(tr)
	final := ""
	if len(steps) > 0 {
		final = steps[len(steps)-1].State
	}
	return &Derivation{Automaton: a.Name, Final: final, Steps: steps}
}

// projectTrace splits a product trace into one component's derivation.
func projectTrace(productTrace *trace, component *Automaton, useLeft bool) *Derivation {
	steps := make([]Step, 0, productTrace.cost)
	var walk func(*trace)
	walk = func(current *trace) {
		if current.leaf {
			steps = append(steps, Step{
				NodeLabel: current.leafRule.Label,
				Leaf: &Leaf{
					Kind:  current.leafRule.Kind,
					Value: append([]byte(nil), current.node.Leaf.Value...),
				},
				State: componentState(current.state, useLeft),
				Exact: current.leafRule.Exact,
			})
			return
		}
		for _, child := range current.children {
			walk(child)
		}
		childStates := make([]string, len(current.rule.Child))
		for i, productState := range current.rule.Child {
			childStates[i] = componentState(productState, useLeft)
		}
		steps = append(steps, Step{
			NodeLabel: current.rule.Label,
			Children:  childStates,
			State:     componentState(current.state, useLeft),
		})
	}
	walk(productTrace)
	final := ""
	if len(steps) > 0 {
		final = steps[len(steps)-1].State
	}
	return &Derivation{Automaton: component.Name, Final: final, Steps: steps}
}

func componentState(productState string, useLeft bool) string {
	left, right := splitPair(productState)
	if useLeft {
		return left
	}
	return right
}
