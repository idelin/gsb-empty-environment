package speckey

import (
	"strconv"
	"strings"
)

// Field is a single nullable string field of a SpecKey.
//
// A null field (Set == false) is semantically distinct from a field
// holding an empty string (Set == true, Value == ""). The canonical
// encoding preserves this distinction.
type Field struct {
	Set   bool
	Value string
}

// Null returns a null (absent) field.
func Null() Field { return Field{} }

// Str returns a field explicitly set to s (possibly the empty string).
func Str(s string) Field { return Field{Set: true, Value: s} }

// Equal reports whether two fields are identical, treating null and
// empty string as different values.
func (f Field) Equal(o Field) bool {
	return f.Set == o.Set && (!f.Set || f.Value == o.Value)
}

// appendField writes the canonical, self-delimiting encoding of f:
//
//	null         -> "N"
//	empty string -> "E"
//	non-empty    -> "V" <decimal byte length> ":" <raw bytes>
//
// Because non-empty values are length-prefixed, any byte sequence
// (including "N", "E", ":", "|", or text that merely looks like an
// encoding) round-trips unambiguously.
func appendField(b *strings.Builder, f Field) {
	if !f.Set {
		b.WriteByte('N')
		return
	}
	if f.Value == "" {
		b.WriteByte('E')
		return
	}
	b.WriteByte('V')
	b.WriteString(strconv.Itoa(len(f.Value)))
	b.WriteByte(':')
	b.WriteString(f.Value)
}
