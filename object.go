// Package graphser serializes object graphs that contain shared references
// and cycles into a pure-data JSON document, and restores them with object
// identity preserved.
//
// Supported value domain (anything else is rejected by Marshal):
//
//   - nil, bool, string
//   - float64 (finite only); int, int64, uint64 within the exact float64
//     range are accepted and normalized to float64
//   - []any          -- a plain value; equal slices are interchangeable and
//     carry no identity
//   - *Object        -- a heap object with identity; two pointers to the same
//     Object are the SAME object, two pointers to distinct Objects with equal
//     fields are merely VALUE-equal
//
// The wire format is a JSON document:
//
//	{"version":1,"count":N,"objects":[{"id":1,"fields":{...}},...],"root":<node>}
//
// where a node is a scalar, {"list":[<node>...]} for slices, or {"ref":id}
// for a reference to an object in the table. IDs are assigned in
// deterministic first-encounter order (fields iterated in sorted-key order),
// so marshaling the same graph twice yields identical bytes.
//
// Unmarshal never executes host code: the format contains no type names or
// constructors, only data. All declared counts and lengths are validated
// against hard limits before any graph node is allocated; dangling
// references and duplicate object IDs are rejected.
package graphser

// Object is a heap object with identity. Identity is the Go pointer:
// two *Object values are the same object iff the pointers are equal.
type Object struct {
	Fields map[string]any
}

// NewObject returns an Object with an empty, non-nil field map.
func NewObject() *Object {
	return &Object{Fields: map[string]any{}}
}

// Hard limits enforced by both Marshal and Unmarshal. Unmarshal validates
// every one of them before allocating any graph node.
const (
	MaxInputBytes = 32 << 20 // maximum serialized document size
	MaxObjects    = 1 << 20  // maximum number of distinct objects
	MaxListLen    = 1 << 20  // maximum items in one []any
	MaxStringLen  = 8 << 20  // maximum bytes in one string
	MaxFields     = 1 << 16  // maximum fields in one Object
	MaxDepth      = 512      // maximum node nesting depth
)
