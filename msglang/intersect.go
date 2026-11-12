package msglang

import (
	"fmt"
	"strings"
)

// pairSep is the internal separator of a product state "left<*>right". It is
// rejected in input state/label names so product states can be split safely.
const pairSep = "<*>"

// Intersect builds the product automaton recognizing exactly the intersection
// of the languages of left and right. The returned automaton is independent
// of the inputs and can be inspected like any Automaton.
func Intersect(left, right *Automaton) (*Automaton, error) {
	if err := left.ensureCompiled(); err != nil {
		return nil, err
	}
	if err := right.ensureCompiled(); err != nil {
		return nil, err
	}
	if err := rejectSeparator(left); err != nil {
		return nil, err
	}
	if err := rejectSeparator(right); err != nil {
		return nil, err
	}

	product := &Automaton{
		Name:   "intersection(" + left.Name + "," + right.Name + ")",
		States: make([]string, 0, len(left.States)*len(right.States)),
	}
	productState := make(map[string]bool)
	addState := func(name string) {
		if !productState[name] {
			productState[name] = true
			product.States = append(product.States, name)
		}
	}

	// Typed leaf rules: both component rules must apply to the same value.
	for i := range left.Leaves {
		leftRule := left.Leaves[i]
		for j := range right.Leaves {
			rightRule := right.Leaves[j]
			if leftRule.Label != rightRule.Label || leftRule.Kind != rightRule.Kind {
				continue
			}
			combined := combineLeafRules(leftRule, rightRule)
			if combined == nil {
				continue
			}
			combined.State = pairName(leftRule.State, rightRule.State)
			addState(combined.State)
			product.Leaves = append(product.Leaves, *combined)
		}
	}

	// Internal rules: same label and arity, synchronized component states.
	for i := range left.Rules {
		leftRule := left.Rules[i]
		for j := range right.Rules {
			rightRule := right.Rules[j]
			if leftRule.Label != rightRule.Label || len(leftRule.Child) != len(rightRule.Child) {
				continue
			}
			children := make([]string, len(leftRule.Child))
			for k := range leftRule.Child {
				children[k] = pairName(leftRule.Child[k], rightRule.Child[k])
				addState(children[k])
			}
			addState(pairName(leftRule.State, rightRule.State))
			product.Rules = append(product.Rules, Rule{
				Label: leftRule.Label,
				Child: children,
				State: pairName(leftRule.State, rightRule.State),
			})
		}
	}

	// Only pairs of component final states are product final states.
	for _, leftFinal := range left.Final {
		for _, rightFinal := range right.Final {
			addState(pairName(leftFinal, rightFinal))
			product.Final = append(product.Final, pairName(leftFinal, rightFinal))
		}
	}

	if err := product.Validate(); err != nil {
		return nil, err
	}
	return product, nil
}

func combineLeafRules(left, right LeafRule) *LeafRule {
	rule := LeafRule{Label: left.Label, Kind: left.Kind}
	switch {
	case left.Exact && right.Exact:
		if !equalBytes(left.Want, right.Want) {
			return nil
		}
		rule.Exact = true
		rule.Want = append([]byte(nil), left.Want...)
	case left.Exact:
		rule.Exact = true
		rule.Want = append([]byte(nil), left.Want...)
	case right.Exact:
		rule.Exact = true
		rule.Want = append([]byte(nil), right.Want...)
	default:
		rule.Exact = false
	}
	return &rule
}

func pairName(left, right string) string {
	return left + pairSep + right
}

func splitPair(state string) (string, string) {
	parts := strings.SplitN(state, pairSep, 2)
	return parts[0], parts[1]
}

func rejectSeparator(a *Automaton) error {
	for _, state := range a.States {
		if strings.Contains(state, pairSep) {
			return fmt.Errorf("msglang: state %q in %q contains reserved separator %q", state, a.Name, pairSep)
		}
	}
	labels := make(map[string]bool)
	for _, rule := range a.Leaves {
		labels[rule.Label] = true
	}
	for _, rule := range a.Rules {
		labels[rule.Label] = true
	}
	for label := range labels {
		if strings.Contains(label, pairSep) {
			return fmt.Errorf("msglang: label %q in %q contains reserved separator %q", label, a.Name, pairSep)
		}
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
