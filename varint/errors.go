package varint

import "errors"

// Sentinel errors returned by this package. Compare with ==.
var (
	// ErrNeedMore is returned when the input ends in the middle of a
	// varint or record. It is never returned for a complete encoding,
	// and a truncated stream is never decoded as a zero value.
	ErrNeedMore = errors.New("varint: need more input")

	// ErrOverflow is returned when a value does not fit in the
	// bit width declared by the schema field.
	ErrOverflow = errors.New("varint: value overflows field width")

	// ErrNonCanonical is returned when a varint is not the shortest
	// possible encoding of its value (e.g. a trailing zero group).
	ErrNonCanonical = errors.New("varint: non-shortest encoding")

	// ErrSchemaVersion is returned when resuming from a pause state
	// whose schema version does not match the decoder's schema.
	ErrSchemaVersion = errors.New("varint: schema version mismatch")
)

// FieldError annotates an error with the schema field that caused it.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string {
	return "varint: field " + e.Field + ": " + e.Err.Error()
}

var errValueCount = errors.New("value count does not match schema field count")
