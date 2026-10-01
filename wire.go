package graphser

// Version is the wire format version written by Marshal and required by
// Unmarshal.
const Version = 1

// Value kinds appearing in the wire format.
const (
	KindNull   = "null"
	KindBool   = "bool"
	KindInt    = "int"
	KindUint   = "uint"
	KindFloat  = "float"
	KindString = "string"
	KindStruct = "struct"
	KindSlice  = "slice"
	KindArray  = "array"
	KindMap    = "map"
	KindRef    = "ref"
)

// Value is one node of the wire-format value tree. Exactly the fields
// implied by Kind are meaningful.
type Value struct {
	Kind    string  `json:"kind"`
	Bool    bool    `json:"bool,omitempty"`
	Int     int64   `json:"int,omitempty"`
	Uint    uint64  `json:"uint,omitempty"`
	Float   float64 `json:"float,omitempty"`
	Str     string  `json:"str,omitempty"`
	Ref     uint64  `json:"ref,omitempty"`
	Type    string  `json:"type,omitempty"`    // struct/slice/array/map
	Fields  []Field `json:"fields,omitempty"`  // struct
	Elems   []Value `json:"elems,omitempty"`   // slice/array
	Entries []Entry `json:"entries,omitempty"` // map
}

// Field is one exported struct field.
type Field struct {
	Name  string `json:"name"`
	Value Value  `json:"value"`
}

// Entry is one map key/value pair.
type Entry struct {
	Key Value `json:"key"`
	Val Value `json:"val"`
}

// Object is the serialized form of one identity-bearing value (a pointer
// pointee or a map). ID is stable: objects are numbered in deterministic
// first-seen traversal order starting at 1.
type Object struct {
	ID    uint64 `json:"id"`
	Type  string `json:"type"`
	Value Value  `json:"value"`
}

// Document is the top-level wire format.
type Document struct {
	Version uint64   `json:"version"`
	Root    Value    `json:"root"`
	Objects []Object `json:"objects"`
}
