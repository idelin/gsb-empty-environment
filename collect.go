package netsec

import (
	"fmt"
	"sort"
)

// validateFragments rejects cycles and undefined fragment references.
func (a *admitter) validateFragments() error {
	state := map[string]int{} // 0 unseen, 1 on stack, 2 done
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return &AdmissionError{
				Code:    ReasonValidation,
				Message: fmt.Sprintf("fragment cycle detected through %q", name),
			}
		case 2:
			return nil
		}
		state[name] = 1
		frag, ok := a.doc.Fragments[name]
		if !ok {
			return &AdmissionError{
				Code:    ReasonValidation,
				Message: fmt.Sprintf("fragment %q is not defined", name),
			}
		}
		for _, sel := range frag.SelectionSet {
			if spread, ok := sel.(*FragmentSpread); ok {
				if err := visit(spread.Name); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		return nil
	}
	names := make([]string, 0, len(a.doc.Fragments))
	for name := range a.doc.Fragments {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// collectFields expands fragments and conditional directives and groups
// fields by response key, rejecting conflicting selections.
func (a *admitter) collectFields(parentType *Type, selections []Selection) (map[string]*fieldGroup, error) {
	groups := map[string]*fieldGroup{}
	var walk func(typ *Type, sels []Selection) error
	walk = func(typ *Type, sels []Selection) error {
		for _, sel := range sels {
			switch node := sel.(type) {
			case *Field:
				included, err := directiveIncluded(node.Directives, a.variables)
				if err != nil {
					return err
				}
				if !included {
					continue
				}
				key := node.ResponseKey()
				group, exists := groups[key]
				if !exists {
					group = &fieldGroup{key: key, parentType: typ}
					groups[key] = group
				} else if err := a.checkConflict(typ, group, node); err != nil {
					return err
				}
				group.members = append(group.members, &collectedField{
					field:      node,
					parentType: typ,
				})

			case *FragmentSpread:
				included, err := directiveIncluded(node.Directives, a.variables)
				if err != nil {
					return err
				}
				if !included {
					continue
				}
				frag, ok := a.doc.Fragments[node.Name]
				if !ok {
					return &AdmissionError{
						Code:    ReasonValidation,
						Message: fmt.Sprintf("fragment %q is not defined", node.Name),
					}
				}
				cond, err := a.lookupType(frag.TypeCondition)
				if err != nil {
					return err
				}
				if !a.typeApplies(typ, cond) {
					continue
				}
				if err := walk(typ, frag.SelectionSet); err != nil {
					return err
				}

			case *InlineFragment:
				included, err := directiveIncluded(node.Directives, a.variables)
				if err != nil {
					return err
				}
				if !included {
					continue
				}
				if node.TypeCondition == "" {
					if err := walk(typ, node.SelectionSet); err != nil {
						return err
					}
					continue
				}
				cond, err := a.lookupType(node.TypeCondition)
				if err != nil {
					return err
				}
				if !a.typeApplies(typ, cond) {
					continue
				}
				if err := walk(typ, node.SelectionSet); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(parentType, selections); err != nil {
		return nil, err
	}
	return groups, nil
}

func (a *admitter) lookupType(name string) (*Type, error) {
	typ, ok := a.schema.Types[name]
	if !ok {
		return nil, &AdmissionError{
			Code:    ReasonValidation,
			Message: fmt.Sprintf("type %q is not in the schema", name),
		}
	}
	return typ, nil
}

// typeApplies reports whether a fragment with condition cond may apply to an
// object of runtime type typ. Same type always applies; an object implements
// an interface; an object is a union member.
func (a *admitter) typeApplies(typ, cond *Type) bool {
	if typ.Name == cond.Name {
		return true
	}
	switch cond.Kind {
	case KindInterface, KindUnion:
		for _, name := range cond.PossibleTypes {
			if name == typ.Name {
				return true
			}
		}
	}
	return false
}

// checkConflict enforces the GraphQL "fields must merge" rule for two
// selections sharing a response key at the same concrete parent type.
func (a *admitter) checkConflict(parentType *Type, group *fieldGroup, incoming *Field) error {
	existing := group.members[0].field
	if existing.Name != incoming.Name {
		return &AdmissionError{
			Code: ReasonValidation,
			Message: fmt.Sprintf(
				"response key %q maps to conflicting fields %q and %q on type %q",
				incoming.ResponseKey(), existing.Name, incoming.Name, parentType.Name,
			),
		}
	}
	equal, err := sameArguments(existing.Arguments, incoming.Arguments, a.variables)
	if err != nil {
		return err
	}
	if !equal {
		return &AdmissionError{
			Code: ReasonValidation,
			Message: fmt.Sprintf(
				"field %q merged under response key %q has conflicting arguments",
				existing.Name, incoming.ResponseKey(),
			),
		}
	}
	return nil
}

// dimensionProduct multiplies the maximum lengths of every list dimension.
func dimensionProduct(dims []int) int64 {
	product := int64(1)
	for _, dim := range dims {
		if dim <= 0 {
			continue
		}
		product = satMul(product, int64(dim))
	}
	return product
}

func sortedGroupKeys(groups map[string]*fieldGroup) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
