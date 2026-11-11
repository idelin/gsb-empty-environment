package netsec

// TypeKind classifies the kinds of output types a field may return.
type TypeKind int

const (
	KindScalar TypeKind = iota
	KindEnum
	KindObject
	KindInterface
	KindUnion
)

// Type describes an output type in the finite schema.
type Type struct {
	// Kind is scalar, enum, object, interface or union.
	Kind TypeKind
	// Name is the type name. Scalars and enums are leaf types.
	Name string
	// Fields holds the fields of object and interface types.
	Fields map[string]*FieldDef
	// PossibleTypes lists concrete object type names for interfaces and
	// unions (in deterministic, schema-declared order).
	PossibleTypes []string
}

// IsLeaf reports whether selections are forbidden on the type.
func (t *Type) IsLeaf() bool {
	return t.Kind == KindScalar || t.Kind == KindEnum
}

// FieldDef describes a field of an object or interface type.
type FieldDef struct {
	// Name is the real schema field name; aliases never appear here.
	Name string
	// Type is the field's declared output type.
	Type *Type
	// ListMaxLen is the maximum possible list length per list dimension,
	// innermost-first. Empty means the field returns a single object/value.
	// A field returning [[T]] uses two entries.
	ListMaxLen []int
	// Cost is the per-instance resolver cost charged for one invocation.
	Cost int64
	// Scope is the permission scope required to access the field. Empty
	// means any caller may access it.
	Scope string
}

// Schema is the finite set of types and root operation objects.
type Schema struct {
	// Types maps type name to type.
	Types map[string]*Type
	// QueryType is the name of the query root object type.
	QueryType string
	// MutationType is the name of the mutation root object type.
	MutationType string
}
