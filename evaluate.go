package netsec

import "fmt"

// evaluateType collects the selection set against parentType, applies
// response-key merging and conflict checks, then charges each field group.
// multiplier is the product of the enclosing list maximum lengths. Cost
// contributions are appended to contribs; for abstract return types only the
// most expensive concrete branch records contributions, so the sum of every
// recorded contribution equals the returned worst-case cost.
func (a *admitter) evaluateType(
	parentType *Type,
	selections []Selection,
	multiplier int64,
	responsePath []string,
	schemaPath []string,
	contribs *[]*Contribution,
) (int64, error) {
	groups, err := a.collectFields(parentType, selections)
	if err != nil {
		return 0, err
	}

	total := int64(0)
	for _, key := range sortedGroupKeys(groups) {
		group := groups[key]
		cost, err := a.evaluateGroup(
			parentType, group, multiplier, responsePath, schemaPath, contribs,
		)
		if err != nil {
			return 0, err
		}
		total = satAdd(total, cost)
	}
	return total, nil
}

func (a *admitter) evaluateGroup(
	parentType *Type,
	group *fieldGroup,
	multiplier int64,
	responsePath []string,
	schemaPath []string,
	contribs *[]*Contribution,
) (int64, error) {
	first := group.members[0].field
	def, ok := parentType.Fields[first.Name]
	if !ok {
		return 0, &AdmissionError{
			Code:       ReasonValidation,
			Message:    fmt.Sprintf("field %q does not exist on type %q", first.Name, parentType.Name),
			SchemaPath: append(append([]string{}, schemaPath...), first.Name),
		}
	}

	// Authorization uses the real field name and schema path, never the
	// alias, so aliasing cannot bypass field-level permissions.
	fieldSchemaPath := append(append([]string{}, schemaPath...), def.Name)
	if def.Scope != "" && !a.scopes[def.Scope] {
		return 0, &AdmissionError{
			Code:       ReasonPermission,
			Message:    fmt.Sprintf("field %q requires scope %q", def.Name, def.Scope),
			SchemaPath: fieldSchemaPath,
		}
	}

	// A field resolver runs once per parent instance. The enclosing
	// multiplier already counts the parent instances; the field's own list
	// dimensions only amplify its sub-selections below.
	invocations := multiplier
	cost := satMul(multiplier, def.Cost)

	*contribs = append(*contribs, &Contribution{
		ResponsePath: append(append([]string{}, responsePath...), group.key),
		SchemaPath:   fieldSchemaPath,
		ObjectType:   parentType.Name,
		Field:        def.Name,
		Multiplier:   multiplier,
		Invocations:  invocations,
		UnitCost:     def.Cost,
		Cost:         cost,
	})

	// Merge the sub-selections of every group member; merged fields charge
	// their children exactly once per merged resolver invocation.
	mergedSelections := make([]Selection, 0)
	for _, member := range group.members {
		mergedSelections = append(mergedSelections, member.field.SelectionSet...)
	}

	returnType := def.Type
	if returnType == nil {
		return 0, &AdmissionError{
			Code:       ReasonValidation,
			Message:    fmt.Sprintf("field %q has no return type", def.Name),
			SchemaPath: fieldSchemaPath,
		}
	}

	switch {
	case returnType.IsLeaf():
		if len(mergedSelections) != 0 {
			return 0, &AdmissionError{
				Code:       ReasonValidation,
				Message:    fmt.Sprintf("field %q on type %q is a leaf and cannot have selections", def.Name, parentType.Name),
				SchemaPath: fieldSchemaPath,
			}
		}
	case len(mergedSelections) == 0:
		return 0, &AdmissionError{
			Code:       ReasonValidation,
			Message:    fmt.Sprintf("field %q on type %q requires a selection set", def.Name, parentType.Name),
			SchemaPath: fieldSchemaPath,
		}
	default:
		childMultiplier := satMul(multiplier, dimensionProduct(def.ListMaxLen))
		childResponsePath := append(append([]string{}, responsePath...), group.key)
		childCost, err := a.evaluateReturn(
			returnType,
			mergedSelections,
			childMultiplier,
			childResponsePath,
			fieldSchemaPath,
			contribs,
		)
		if err != nil {
			return 0, err
		}
		cost = satAdd(cost, childCost)
	}
	return cost, nil
}

// evaluateReturn handles abstract return types by enumerating every concrete
// possible type and charging the most expensive branch; the bound must hold
// for any runtime object type.
func (a *admitter) evaluateReturn(
	returnType *Type,
	selections []Selection,
	multiplier int64,
	responsePath []string,
	schemaPath []string,
	contribs *[]*Contribution,
) (int64, error) {
	switch returnType.Kind {
	case KindObject:
		return a.evaluateType(
			returnType, selections, multiplier, responsePath, schemaPath, contribs,
		)
	case KindInterface, KindUnion:
		worst := int64(-1)
		var worstContribs []*Contribution
		for _, concreteName := range returnType.PossibleTypes {
			concrete, ok := a.schema.Types[concreteName]
			if !ok {
				return 0, &AdmissionError{
					Code:       ReasonValidation,
					Message:    fmt.Sprintf("possible type %q is not in the schema", concreteName),
					SchemaPath: schemaPath,
				}
			}
			var branchContribs []*Contribution
			branchCost, err := a.evaluateType(
				concrete, selections, multiplier, responsePath, schemaPath, &branchContribs,
			)
			if err != nil {
				return 0, err
			}
			if branchCost > worst {
				worst = branchCost
				worstContribs = branchContribs
			}
		}
		if worst < 0 {
			return 0, &AdmissionError{
				Code:       ReasonValidation,
				Message:    fmt.Sprintf("abstract type %q has no possible types", returnType.Name),
				SchemaPath: schemaPath,
			}
		}
		*contribs = append(*contribs, worstContribs...)
		return worst, nil
	default:
		return 0, &AdmissionError{
			Code:       ReasonValidation,
			Message:    fmt.Sprintf("type %q cannot hold a selection set", returnType.Name),
			SchemaPath: schemaPath,
		}
	}
}
