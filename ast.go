package netsec

// OperationType identifies the root operation kind of an Operation.
type OperationType string

const (
	OpQuery        OperationType = "query"
	OpMutation     OperationType = "mutation"
	OpSubscription OperationType = "subscription"
)

// Document is the parsed query AST consumed by the admission package.
// It is intentionally framework-agnostic: a gateway adapter maps its own
// parser output onto these types.
type Document struct {
	Operations []*Operation
	Fragments  map[string]*FragmentDefinition
}

// Operation is a single query, mutation or subscription operation.
type Operation struct {
	Type                OperationType
	Name                string
	VariableDefinitions []*VariableDefinition
	SelectionSet        []Selection
}

// VariableDefinition records a declared operation variable.
type VariableDefinition struct {
	Name         string
	DeclaredType string
	DefaultValue Value
}

// Selection is a node that may appear in a selection set.
type Selection interface {
	isSelection()
}

// Field is a leaf or object field selection. Alias may be empty.
type Field struct {
	Alias        string
	Name         string
	Arguments    []*Argument
	Directives   []*Directive
	SelectionSet []Selection
}

// FragmentSpread references a FragmentDefinition by name.
type FragmentSpread struct {
	Name       string
	Directives []*Directive
}

// InlineFragment is an anonymous fragment with an optional type condition.
type InlineFragment struct {
	TypeCondition string
	Directives    []*Directive
	SelectionSet  []Selection
}

// FragmentDefinition is a named, reusable fragment.
type FragmentDefinition struct {
	Name          string
	TypeCondition string
	Directives    []*Directive
	SelectionSet  []Selection
}

func (*Field) isSelection()          {}
func (*FragmentSpread) isSelection() {}
func (*InlineFragment) isSelection() {}

// ResponseKey returns the key under which the field appears in the response:
// the alias when present, otherwise the field name.
func (f *Field) ResponseKey() string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.Name
}

// Directive is a single directive application such as @skip or @include.
type Directive struct {
	Name      string
	Arguments []*Argument
}

// Argument is a named argument on a field or directive.
type Argument struct {
	Name  string
	Value Value
}

// Value is a literal or variable reference in the query.
type Value interface {
	isValue()
}

type Variable struct {
	Name string
}

type IntValue struct {
	Value int64
}

type FloatValue struct {
	Value float64
}

type StringValue struct {
	Value string
}

type BooleanValue struct {
	Value bool
}

type NullValue struct{}

type EnumValue struct {
	Value string
}

type ListValue struct {
	Values []Value
}

type ObjectValue struct {
	Fields []*ObjectField
}

// ObjectField is a member of an ObjectValue.
type ObjectField struct {
	Name  string
	Value Value
}

func (*Variable) isValue()     {}
func (*IntValue) isValue()     {}
func (*FloatValue) isValue()   {}
func (*StringValue) isValue()  {}
func (*BooleanValue) isValue() {}
func (*NullValue) isValue()    {}
func (*EnumValue) isValue()    {}
func (*ListValue) isValue()    {}
func (*ObjectValue) isValue()  {}
