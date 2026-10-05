package varint

import "errors"

// Field describes one integer field of a record.
type Field struct {
	Name string
	// Width is the field's bit width, in [1, 64]. Values that do not
	// fit in Width bits are rejected with ErrOverflow.
	Width uint
	// Signed selects zigzag varint coding; unsigned uses plain LEB128.
	Signed bool
}

// Schema is a versioned record layout. The version is bound into
// decoder pause/resume state.
type Schema struct {
	Version uint32
	Fields  []Field
}

// Validate checks that the schema is well formed.
func (s Schema) Validate() error {
	if len(s.Fields) == 0 {
		return errors.New("varint: schema has no fields")
	}
	for _, f := range s.Fields {
		if f.Width == 0 || f.Width > 64 {
			return &FieldError{Field: f.Name, Err: errors.New("width out of range [1,64]")}
		}
	}
	return nil
}

// Value carries one field value. Use U for unsigned fields and S for
// signed fields, matching the schema.
type Value struct {
	U uint64
	S int64
}

// Record is one decoded record, one Value per schema field.
type Record []Value
