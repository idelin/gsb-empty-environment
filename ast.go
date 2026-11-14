package netsec

import (
	"fmt"
	"reflect"
	"strconv"
)

// Position is a one-based source location.
type Position struct {
	Line   int
	Column int
}

// OperationType is the kind of executable GraphQL operation.
type OperationType string

const (
	OperationQuery        OperationType = "query"
	OperationMutation     OperationType = "mutation"
	OperationSubscription OperationType = "subscription"
)

// Document is an executable GraphQL document.
type Document struct {
	Operations []*Operation
	Fragments  map[string]*FragmentDefinition
}

// Operation is a query, mutation, or subscription.
type Operation struct {
	Type                OperationType
	Name                string
	VariableDefinitions []*VariableDefinition
	Directives          []*Directive
	SelectionSet        []Selection
	Position            Position
}

// VariableDefinition declares an operation variable.
type VariableDefinition struct {
	Name       string
	Type       *TypeReference
	Default    *Value
	HasDefault bool
	Directives []*Directive
	Position   Position
}

// TypeReference is a GraphQL variable type such as String!, [ID!]!.
type TypeReference struct {
	Named   string
	List    *TypeReference
	NonNull bool
}

func (t *TypeReference) String() string {
	if t == nil {
		return ""
	}
	var inner string
	if t.List != nil {
		inner = "[" + t.List.String() + "]"
	} else {
		inner = t.Named
	}
	if t.NonNull {
		inner += "!"
	}
	return inner
}

// Directive is an executable directive such as @skip(if: $hide).
type Directive struct {
	Name      string
	Arguments []*Argument
	Position  Position
	Origin    string
}

// Argument is a field or directive argument.
type Argument struct {
	Name     string
	Value    *Value
	Position Position
}

// Selection is implemented by Field, FragmentSpread, and InlineFragment.
type Selection interface {
	isSelection()
	selectionPosition() Position
	selectionDirectives() []*Directive
}

// Field is a field selection, possibly with an alias.
type Field struct {
	Alias        string
	Name         string
	Arguments    []*Argument
	Directives   []*Directive
	SelectionSet []Selection
	Position     Position
}

func (*Field) isSelection()                        {}
func (f *Field) selectionPosition() Position       { return f.Position }
func (f *Field) selectionDirectives() []*Directive { return f.Directives }

// ResponseKey is the alias when present, otherwise the field name.
func (f *Field) ResponseKey() string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.Name
}

// FragmentSpread spreads a named fragment.
type FragmentSpread struct {
	Name       string
	Directives []*Directive
	Position   Position
}

func (*FragmentSpread) isSelection()                        {}
func (s *FragmentSpread) selectionPosition() Position       { return s.Position }
func (s *FragmentSpread) selectionDirectives() []*Directive { return s.Directives }

// InlineFragment is an ... on Type selection, or an untyped ... selection.
type InlineFragment struct {
	TypeCondition string
	Directives    []*Directive
	SelectionSet  []Selection
	Position      Position
}

func (*InlineFragment) isSelection()                        {}
func (s *InlineFragment) selectionPosition() Position       { return s.Position }
func (s *InlineFragment) selectionDirectives() []*Directive { return s.Directives }

// FragmentDefinition defines a reusable named fragment.
type FragmentDefinition struct {
	Name          string
	TypeCondition string
	Directives    []*Directive
	SelectionSet  []Selection
	Position      Position
}

// ValueKind identifies a GraphQL literal or variable reference.
type ValueKind int

const (
	ValueVariable ValueKind = iota
	ValueInt
	ValueFloat
	ValueString
	ValueBoolean
	ValueNull
	ValueEnum
	ValueList
	ValueObject
)

// Value is a GraphQL input value.
type Value struct {
	Kind     ValueKind
	Variable string
	Raw      string
	Bool     bool
	List     []*Value
	Object   []*ObjectValueField
	Position Position
}

// ObjectValueField is one field in an input object literal.
type ObjectValueField struct {
	Name     string
	Value    *Value
	Position Position
}

// EnumValue is a GraphQL enum literal.
type EnumValue string

// Literal returns a JSON-like representation of a literal value. It never
// evaluates variables. Passing a variable returns an error.
func (v *Value) Literal() (any, error) {
	if v == nil {
		return nil, nil
	}
	switch v.Kind {
	case ValueVariable:
		return nil, fmt.Errorf("netsec: variable $%s is not a literal", v.Variable)
	case ValueInt:
		value, err := strconv.ParseInt(v.Raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid integer %q: %w", v.Raw, err)
		}
		if int64(int(value)) == value {
			return int(value), nil
		}
		return value, nil
	case ValueFloat:
		value, err := strconv.ParseFloat(v.Raw, 64)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid float %q: %w", v.Raw, err)
		}
		return value, nil
	case ValueString:
		return v.Raw, nil
	case ValueBoolean:
		return v.Bool, nil
	case ValueNull:
		return nil, nil
	case ValueEnum:
		return v.Raw, nil
	case ValueList:
		result := make([]any, len(v.List))
		for i, item := range v.List {
			value, err := item.Literal()
			if err != nil {
				return nil, err
			}
			result[i] = value
		}
		return result, nil
	case ValueObject:
		result := make(map[string]any, len(v.Object))
		for _, field := range v.Object {
			value, err := field.Value.Literal()
			if err != nil {
				return nil, err
			}
			result[field.Name] = value
		}
		return result, nil
	default:
		return nil, fmt.Errorf("netsec: unknown value kind %d", v.Kind)
	}
}

// Evaluate resolves variables recursively and returns a detached value. It
// never mutates the variable map or the AST.
func (v *Value) Evaluate(variables map[string]any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch v.Kind {
	case ValueVariable:
		value, ok := variables[v.Variable]
		if !ok {
			return nil, fmt.Errorf("netsec: variable $%s is not defined at runtime", v.Variable)
		}
		return cloneJSONValue(value), nil
	case ValueList:
		result := make([]any, len(v.List))
		for i, item := range v.List {
			value, err := item.Evaluate(variables)
			if err != nil {
				return nil, err
			}
			result[i] = value
		}
		return result, nil
	case ValueObject:
		result := make(map[string]any, len(v.Object))
		for _, field := range v.Object {
			value, err := field.Value.Evaluate(variables)
			if err != nil {
				return nil, err
			}
			result[field.Name] = value
		}
		return result, nil
	default:
		return v.Literal()
	}
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneJSONValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONValue(item)
		}
		return result
	case []string:
		result := append([]string(nil), typed...)
		return result
	case map[string]string:
		result := make(map[string]string, len(typed))
		for key, item := range typed {
			result[key] = item
		}
		return result
	case []byte:
		return append([]byte(nil), typed...)
	default:
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() {
			switch reflected.Kind() {
			case reflect.Slice, reflect.Array:
				result := make([]any, reflected.Len())
				for i := 0; i < reflected.Len(); i++ {
					result[i] = cloneJSONValue(reflected.Index(i).Interface())
				}
				return result
			case reflect.Map:
				if reflected.Type().Key().Kind() == reflect.String {
					result := make(map[string]any, reflected.Len())
					iterator := reflected.MapRange()
					for iterator.Next() {
						result[iterator.Key().String()] = cloneJSONValue(iterator.Value().Interface())
					}
					return result
				}
			}
		}
		return value
	}
}
