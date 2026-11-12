// Package capture copies call arguments out of a remote, concurrently
// mutated address space into an immutable snapshot graph.
//
// A sandboxed agent typically audits arguments (e.g. a path string) in
// target memory and the kernel re-reads them at exec time, opening a
// TOCTOU window. Capture removes the window for the Go host: it walks a
// caller-supplied Schema, copies every reachable byte exactly once, and
// retries the whole capture if the target's version counter moved.
//
// All scalars and pointers are 8 bytes, little-endian.
package capture

// Schema describes how to copy one value out of the target address
// space. The concrete types are Uint64, CString, Blob, Ptr, Struct and
// Counted.
type Schema interface{ schema() }

// Field is one member of a Struct schema.
type Field struct {
	Name string
	Off  uint64 // byte offset within the enclosing struct
	Type Schema
}

type (
	// Uint64 captures an 8-byte scalar.
	Uint64 struct{}

	// CString captures a NUL-terminated byte string, reading at most
	// Max bytes. An unterminated string is truncated at Max.
	CString struct{ Max int }

	// Blob captures exactly Len bytes.
	Blob struct{ Len int }

	// Ptr captures an 8-byte pointer and then its target. Repeated
	// pointers to the same address share a single capture; a zero
	// pointer captures as a nil Elem.
	Ptr struct{ Elem Schema }

	// Struct captures a fixed-size record field by field.
	Struct struct{ Fields []Field }

	// Counted captures a header of {u64 count; u64 ptr} followed by
	// count elements of Elem read from ptr. Elem must have a static
	// size (see StaticSize).
	Counted struct{ Elem Schema }

	// Ref resolves *S lazily, allowing recursive layouts (a list node
	// pointing at its own schema). A Ref chain must pass through Ptr
	// or Counted so recursion is bounded by memory, not by the schema
	// graph itself:
	//
	//	var node Schema
	//	node = Struct{Fields: []Field{
	//		{Name: "next", Off: 0, Type: Ptr{Elem: Ref{S: &node}}},
	//	}}
	Ref struct{ S *Schema }
)

func (Uint64) schema()  {}
func (CString) schema() {}
func (Blob) schema()    {}
func (Ptr) schema()     {}
func (Struct) schema()  {}
func (Counted) schema() {}
func (Ref) schema()     {}

// StaticSize reports the fixed byte size of s, if it has one. CString
// has no static size; every other schema does.
func StaticSize(s Schema) (uint64, bool) {
	switch t := s.(type) {
	case Uint64:
		return 8, true
	case Ptr:
		return 8, true
	case Blob:
		if t.Len < 0 {
			return 0, false
		}
		return uint64(t.Len), true
	case Counted:
		return 16, true
	case Struct:
		var end uint64
		for _, f := range t.Fields {
			sz, ok := StaticSize(f.Type)
			if !ok {
				return 0, false
			}
			if e := f.Off + sz; e > end {
				end = e
			}
		}
		return end, true
	case CString:
		return 0, false
	case Ref:
		if t.S == nil {
			return 0, false
		}
		return StaticSize(*t.S)
	default:
		return 0, false
	}
}
