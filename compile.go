package netsec

import (
	"fmt"
	"strings"
)

const (
	defaultMaxDepth         = 64
	defaultMaxNodes         = 100000
	defaultMaxFragmentDepth = 64
)

// Compile parses and normalizes an executable operation without evaluating any
// variables. Every field occurrence, including duplicate aliases and every
// fragment expansion, receives its own template node. operationName may be
// omitted when the document contains exactly one operation.
func Compile(query string, operationName ...string) (*StaticPlan, error) {
	name := ""
	if len(operationName) > 1 {
		return nil, fmt.Errorf("%w: at most one operation name may be supplied", ErrInvalidQuery)
	}
	if len(operationName) == 1 {
		name = operationName[0]
	}
	return compileWithLimits(query, name, defaultMaxDepth, defaultMaxNodes, defaultMaxFragmentDepth)
}

func compileWithLimits(query, name string, maxDepth, maxNodes, maxFragmentDepth int) (*StaticPlan, error) {
	document, err := Parse(query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}
	operation, err := selectOperation(document, name)
	if err != nil {
		return nil, err
	}
	if err := validateVariables(document, operation); err != nil {
		return nil, err
	}
	compiler := &planCompiler{
		document:    document,
		maxDepth:    maxDepth,
		maxNodes:    maxNodes,
		maxFragment: maxFragmentDepth,
	}
	if err := compiler.compileOperation(operation); err != nil {
		return nil, err
	}
	plan := &StaticPlan{
		Document:      document,
		Operation:     operation,
		OperationName: operation.Name,
		Nodes:         compiler.nodes,
	}
	plan.Digest = digestString(canonicalStaticPlan(plan))
	return plan, nil
}

// ParseQuery is an alias for Parse.
func ParseQuery(query string) (*Document, error) {
	return Parse(query)
}

func selectOperation(document *Document, name string) (*Operation, error) {
	if name == "" {
		if len(document.Operations) != 1 {
			return nil, fmt.Errorf("%w: operation name is required when the document has multiple operations", ErrInvalidQuery)
		}
		return document.Operations[0], nil
	}
	for _, operation := range document.Operations {
		if operation.Name == name {
			return operation, nil
		}
	}
	return nil, fmt.Errorf("%w: operation %q not found", ErrInvalidQuery, name)
}

type planCompiler struct {
	document    *Document
	nodes       []*NodeTemplate
	maxDepth    int
	maxNodes    int
	maxFragment int
}

type compileState struct {
	parentPath    Path
	parentType    string
	parentNodeID  string
	conditions    []PlannedDirective
	fragmentStack []string
	depth         int
	origin        string
}

func (c *planCompiler) compileOperation(operation *Operation) error {
	state := compileState{
		parentType: operationRootType(operation.Type),
		depth:      0,
		origin:     "operation",
	}
	state.conditions = appendDirectives(state.conditions, operation.Directives, "operation")
	return c.expandSelections(operation.SelectionSet, state)
}

func operationRootType(operationType OperationType) string {
	switch operationType {
	case OperationMutation:
		return "Mutation"
	case OperationSubscription:
		return "Subscription"
	default:
		return "Query"
	}
}

func appendDirectives(existing []PlannedDirective, directives []*Directive, fallbackOrigin string) []PlannedDirective {
	if len(directives) == 0 {
		return existing
	}
	result := make([]PlannedDirective, len(existing), len(existing)+len(directives))
	copy(result, existing)
	for _, directive := range directives {
		origin := directive.Origin
		if origin == "" {
			origin = fallbackOrigin
		}
		result = append(result, PlannedDirective{
			Name:      directive.Name,
			Origin:    origin,
			Arguments: directive.Arguments,
			Position:  directive.Position,
		})
	}
	return result
}

func (c *planCompiler) expandSelections(selections []Selection, state compileState) error {
	if state.depth > c.maxDepth {
		return fmt.Errorf("%w: selection depth exceeds %d", ErrInvalidQuery, c.maxDepth)
	}
	for _, selection := range selections {
		if len(c.nodes) >= c.maxNodes {
			return fmt.Errorf("%w: selection node limit %d exceeded", ErrInvalidQuery, c.maxNodes)
		}
		switch typed := selection.(type) {
		case *Field:
			if err := c.addField(typed, state); err != nil {
				return err
			}
		case *InlineFragment:
			conditions := appendDirectives(state.conditions, typed.Directives, "inline fragment")
			parentType := state.parentType
			if typed.TypeCondition != "" {
				parentType = typed.TypeCondition
			}
			next := state
			next.conditions = conditions
			next.parentType = parentType
			next.depth++
			next.origin = state.origin + "/inline"
			if err := c.expandSelections(typed.SelectionSet, next); err != nil {
				return err
			}
		case *FragmentSpread:
			if err := c.expandFragment(typed, state); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown selection type %T", ErrInvalidQuery, selection)
		}
	}
	return nil
}

func (c *planCompiler) expandFragment(spread *FragmentSpread, state compileState) error {
	fragment, exists := c.document.Fragments[spread.Name]
	if !exists {
		return fmt.Errorf("%w: fragment %q is not defined", ErrInvalidQuery, spread.Name)
	}
	for _, active := range state.fragmentStack {
		if active == spread.Name {
			return fmt.Errorf("%w: fragment cycle involving %q", ErrInvalidQuery, spread.Name)
		}
	}
	if len(state.fragmentStack) >= c.maxFragment {
		return fmt.Errorf("%w: fragment expansion depth exceeds %d", ErrInvalidQuery, c.maxFragment)
	}

	conditions := appendDirectives(state.conditions, spread.Directives, "fragment spread:"+spread.Name)
	conditions = appendDirectives(conditions, fragment.Directives, "fragment definition:"+spread.Name)

	next := state
	next.conditions = conditions
	next.parentType = fragment.TypeCondition
	next.depth++
	next.origin = state.origin + "/fragment:" + spread.Name
	next.fragmentStack = append(append([]string(nil), state.fragmentStack...), spread.Name)
	return c.expandSelections(fragment.SelectionSet, next)
}

func (c *planCompiler) addField(field *Field, state compileState) error {
	conditions := appendDirectives(state.conditions, field.Directives, "field")
	responseKey := field.ResponseKey()
	path := joinPath(state.parentPath, responseKey)
	node := &NodeTemplate{
		ID:          fmt.Sprintf("node-%08d", len(c.nodes)),
		ParentID:    state.parentNodeID,
		Order:       len(c.nodes),
		ParentType:  state.parentType,
		FieldName:   field.Name,
		Alias:       field.Alias,
		ResponseKey: responseKey,
		Path:        path,
		Arguments:   field.Arguments,
		Directives:  field.Directives,
		Conditions:  conditions,
		Depth:       state.depth,
		Position:    field.Position,
		Origin:      state.origin,
	}
	c.nodes = append(c.nodes, node)
	if len(field.SelectionSet) == 0 {
		return nil
	}
	next := state
	next.parentPath = path
	next.parentNodeID = node.ID
	next.conditions = conditions
	next.depth = state.depth + 1
	next.origin = state.origin + "/field:" + field.Name
	return c.expandSelections(field.SelectionSet, next)
}

func validateVariables(document *Document, operation *Operation) error {
	defined := make(map[string]*VariableDefinition, len(operation.VariableDefinitions))
	for _, definition := range operation.VariableDefinitions {
		defined[definition.Name] = definition
		if definition.HasDefault {
			if definition.Default != nil && definition.Default.Kind == ValueVariable {
				return fmt.Errorf("%w: default value of $%s may not reference a variable", ErrInvalidVariables, definition.Name)
			}
			if definition.Type != nil && definition.Type.NonNull && definition.Default.Kind == ValueNull {
				return fmt.Errorf("%w: non-null variable $%s has a null default", ErrInvalidVariables, definition.Name)
			}
		}
	}
	visitor := &variableVisitor{
		document: document,
		defined:  defined,
		visiting: make(map[string]bool),
		expanded: make(map[string]bool),
	}
	if err := visitor.selections(operation.SelectionSet); err != nil {
		return err
	}
	if err := visitor.directives(operation.Directives); err != nil {
		return err
	}
	for _, definition := range operation.VariableDefinitions {
		if err := visitor.directives(definition.Directives); err != nil {
			return err
		}
	}
	return nil
}

type variableVisitor struct {
	document *Document
	defined  map[string]*VariableDefinition
	visiting map[string]bool
	expanded map[string]bool
}

func (v *variableVisitor) selections(selections []Selection) error {
	for _, selection := range selections {
		switch typed := selection.(type) {
		case *Field:
			for _, argument := range typed.Arguments {
				if err := v.value(argument.Value); err != nil {
					return err
				}
			}
			if err := v.directives(typed.Directives); err != nil {
				return err
			}
			if err := v.selections(typed.SelectionSet); err != nil {
				return err
			}
		case *InlineFragment:
			if err := v.directives(typed.Directives); err != nil {
				return err
			}
			if err := v.selections(typed.SelectionSet); err != nil {
				return err
			}
		case *FragmentSpread:
			if err := v.directives(typed.Directives); err != nil {
				return err
			}
			fragment, exists := v.document.Fragments[typed.Name]
			if !exists {
				return fmt.Errorf("%w: fragment %q is not defined", ErrInvalidQuery, typed.Name)
			}
			if v.expanded[typed.Name] {
				continue
			}
			if v.visiting[typed.Name] {
				return fmt.Errorf("%w: fragment cycle involving %q", ErrInvalidQuery, typed.Name)
			}
			v.visiting[typed.Name] = true
			if err := v.directives(fragment.Directives); err != nil {
				return err
			}
			if err := v.selections(fragment.SelectionSet); err != nil {
				return err
			}
			delete(v.visiting, typed.Name)
			v.expanded[typed.Name] = true
		}
	}
	return nil
}

func (v *variableVisitor) directives(directives []*Directive) error {
	for _, directive := range directives {
		for _, argument := range directive.Arguments {
			if err := v.value(argument.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *variableVisitor) value(value *Value) error {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case ValueVariable:
		if _, exists := v.defined[value.Variable]; !exists {
			return fmt.Errorf("%w: variable $%s is used but not defined by this operation", ErrInvalidVariables, value.Variable)
		}
	case ValueList:
		for _, item := range value.List {
			if err := v.value(item); err != nil {
				return err
			}
		}
	case ValueObject:
		for _, field := range value.Object {
			if err := v.value(field.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinReasons(reasons []string) string {
	return strings.Join(reasons, "; ")
}
