package objectgraph

// Kind enumerates the supported entity types. The supported serialization
// scope is intentionally small: leaves are nil, bool, int64, float64 and
// string; containers are arrays and string-keyed objects. The decoder never
// interprets payload bytes as host code: there is no function, interface,
// import or type-tag-driven instantiation mechanism.
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindArray
	KindObject
)

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	default:
		return "unknown"
	}
}

// Field is one object entry. Order is preserved as encoded.
type Field struct {
	Key   string
	Value *Value
}

// Value is an immutable decoded entity. Cycles are represented by the same
// *Value pointer occurring more than once in the graph; a shared node is one
// entity even though many edges point at it.
type Value struct {
	kind   Kind
	boolV  bool
	intV   int64
	floatV float64
	strV   string
	elems  []*Value
	fields []Field
}

func (v *Value) Kind() Kind     { return v.kind }
func (v *Value) Bool() bool     { return v.boolV }
func (v *Value) Int() int64     { return v.intV }
func (v *Value) Float() float64 { return v.floatV }
func (v *Value) Text() string   { return v.strV }

func (v *Value) String() string {
	switch v.kind {
	case KindString:
		return v.strV
	case KindArray:
		return "[array]"
	case KindObject:
		return "{object}"
	default:
		return v.kind.String()
	}
}

// Len returns the element count for arrays and the field count for objects;
// it returns 0 for leaves.
func (v *Value) Len() int {
	if v.kind == KindArray {
		return len(v.elems)
	}
	return len(v.fields)
}

func (v *Value) Elems() []*Value { return v.elems }
func (v *Value) Fields() []Field { return v.fields }

func (v *Value) Field(key string) *Value {
	for i := range v.fields {
		if v.fields[i].Key == key {
			return v.fields[i].Value
		}
	}
	return nil
}
