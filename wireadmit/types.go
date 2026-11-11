// Package wireadmit is a protocol-buffers wire-format admission gate for
// transparent Go gateways.
//
// A gateway that validates a message and then forwards the original bytes to
// a backend cannot rely on the backend re-parsing the same way it did: a
// single-valued field that appears twice may resolve to the first or the last
// occurrence depending on the backend library, letting an attacker shadow a
// validated value with a second one ("field shadowing").
//
// wireadmit parses only the protobuf wire format (the encoding described by
// https://protobuf.dev/programming-guides/encoding/), without generated code.
// The caller supplies the set of authorised fields with their numbers, types
// and cardinalities. Admit then guarantees:
//
//   - Single-valued (non-repeated) fields appearing more than once are
//     rejected, so no backend can pick a different occurrence than the one
//     that was validated.
//   - Repeated fields may arrive in packed or unpacked form, including a mix
//     of both; every element is surfaced in arrival order so the caller can
//     merge them deterministically.
//   - Unknown fields are never interpreted against the schema. Their original
//     byte ranges are recorded for auditing or stripping, and the bytes are
//     returned untouched for forwarding.
//   - Delimited lengths, varint encodings (including >64-bit overflows) and a
//     recursion/nesting budget are checked before the message is admitted.
//
// The input slice is never copied or modified: after a successful Admit the
// caller may forward exactly the bytes it received.
package wireadmit

// Cardinality declares how many times a field may appear.
type Cardinality int

const (
	// Singular allows at most one logical occurrence of a field. A second
	// occurrence, even with an identical value, is rejected.
	Singular Cardinality = iota
	// Repeated allows any number of occurrences, including packed and
	// unpacked encodings of the same field within one message.
	Repeated
)

// Type is the scalar or structural type the caller associates with a field.
// The accepted wire type(s) are derived from it.
type Type int

const (
	// Varint covers int32, int64, uint32, uint64, sint32, sint64, bool and
	// enum fields. Only the varint wire type (0) is accepted.
	Varint Type = iota
	// Fixed64 covers fixed64, sfixed64 and double fields. Only the 64-bit
	// fixed-length wire type (1) is accepted.
	Fixed64
	// Fixed32 covers fixed32, sfixed32 and float fields. Only the 32-bit
	// fixed-length wire type (5) is accepted.
	Fixed32
	// Bytes covers bytes, string and sub-message fields that are inspected
	// opaquely (no nested Rule). Only the length-delimited wire type (2) is
	// accepted; the length and payload bounds are validated.
	Bytes
	// Message is a length-delimited sub-message (wire type 2) whose own
	// fields are validated against the nested Schema, consuming nesting
	// budget.
	Message
	// Group is a deprecated start-group field (wire type 3). Groups remain
	// legal on the wire; their fields are validated like a message and each
	// nesting level consumes budget.
	Group
)

// Rule describes one authorised field.
type Rule struct {
	// Number is the protobuf field number. Zero and negative numbers are
	// rejected at schema construction.
	Number int
	// Type constrains the acceptable wire type and, for Message and Group,
	// points at the nested schema.
	Type Type
	// Cardinality selects singular versus repeated handling.
	Cardinality Cardinality
	// Nested holds the schema of sub-fields when Type is Message or Group.
	// It must be nil for scalar types and non-nil for Message and Group.
	Nested *Schema
}

// Range is a half-open byte interval [Start, End) inside the original input.
// Ranges always refer to the exact bytes supplied to Admit; they are never
// re-backed by an internal copy.
type Range struct {
	Start int
	End   int
}

// Value is one element of a field in arrival order.
//
// For scalar fields Raw is the field payload (the varint bytes, the fixed
// bytes or the length-delimited payload) and Sub is nil. For Message and
// Group fields Raw spans the whole encoded field and Sub is the nested
// report.
type Value struct {
	// Wire is the wire type the element actually arrived with.
	Wire int
	// Raw is the exact payload bytes, aliasing the caller's input slice.
	Raw []byte
	// Sub is the validation report of a Message or Group field, nil for
	// scalar fields.
	Sub *Report
}

// Entry records one logical field occurrence in arrival order. A packed
// repeated field is one Entry holding every element decoded from the packed
// payload in Values; an unpacked occurrence holds a single Value.
type Entry struct {
	// Number is the field number.
	Number int
	// Type is the schema type of the field.
	Type Type
	// Packed reports whether this occurrence used length-delimited packed
	// encoding. It is only true for packable repeated scalar fields.
	Packed bool
	// Data is the exact byte range of the occurrence (tag included) within
	// the input.
	Data Range
	// Values holds the element value(s) in arrival order. Unpacked scalar
	// fields have one element; packed fields have one element per encoded
	// item; Message and Group fields have one element carrying Sub.
	Values []Value
}

// Report is the admission result returned for a valid message.
type Report struct {
	// Fields lists every known-field occurrence in arrival order. Merging
	// repeated fields means iterating this list and concatenating Values for
	// a given field number.
	Fields []Entry
	// Unknown lists the exact byte ranges of every field whose number has
	// no Rule. They were bounds-checked and skipped, but never interpreted.
	Unknown []Range
	// MaxDepth is the greatest nesting depth reached while parsing. The
	// top-level message is depth 1.
	MaxDepth int
}

// Values returns every value of field number in arrival order, concatenating
// packed and unpacked occurrences exactly as they appeared on the wire. It
// returns nil when the field is absent.
func (r *Report) Values(number int) []Value {
	var out []Value
	for i := range r.Fields {
		if r.Fields[i].Number == number {
			out = append(out, r.Fields[i].Values...)
		}
	}
	return out
}

// ErrorCode identifies a class of admission failure.
type ErrorCode string

const (
	// ErrTruncated means the input ended while more bytes were required by
	// a tag, varint, fixed field or declared length.
	ErrTruncated ErrorCode = "truncated"
	// ErrVarintOverflow means a varint encodes more than 64 bits.
	ErrVarintOverflow ErrorCode = "varint overflow"
	// ErrLengthOverflow means a length-delimited payload would extend past
	// the end of its enclosing buffer.
	ErrLengthOverflow ErrorCode = "length overflow"
	// ErrInvalidField means a tag carries field number 0 or a reserved wire
	// type (group markers are legal only where the grammar allows).
	ErrInvalidField ErrorCode = "invalid field"
	// ErrWireType means a known field arrived with a wire type incompatible
	// with its declared Type.
	ErrWireType ErrorCode = "wire type mismatch"
	// ErrDuplicateSingular means a Singular field occurs more than once.
	ErrDuplicateSingular ErrorCode = "duplicate singular field"
	// ErrPacked means an unpackable field used packed encoding or a packed
	// payload was malformed.
	ErrPacked ErrorCode = "invalid packed encoding"
	// ErrNesting means the configured nesting budget was exceeded.
	ErrNesting ErrorCode = "nesting budget exceeded"
	// ErrSize means the configured maximum message size was exceeded.
	ErrSize ErrorCode = "message too large"
	// ErrSchema means a Schema or Rule is malformed.
	ErrSchema ErrorCode = "invalid schema"
)

// Error is a structured admission error.
type Error struct {
	// Code is the failure class.
	Code ErrorCode
	// Offset is the byte offset in the input where parsing failed, or -1 for
	// errors that are not tied to a position (for example size limits).
	Offset int
	// Field is the field number being parsed when the error occurred, or 0
	// when no field context exists.
	Field int
	// Msg carries human-readable detail.
	Msg string
}

func (e *Error) Error() string {
	if e.Field != 0 {
		return "wireadmit: " + string(e.Code) + " at offset " +
			itoa(e.Offset) + " (field " + itoa(e.Field) + "): " + e.Msg
	}
	return "wireadmit: " + string(e.Code) + " at offset " +
		itoa(e.Offset) + ": " + e.Msg
}

// Is reports whether the error's Code matches err's sentinel, so errors.Is
// works against the package-level sentinel variables.
func (e *Error) Is(err error) bool {
	if sentinel, ok := err.(*Error); ok {
		return sentinel.Code == e.Code
	}
	return false
}

// Sentinel errors for errors.Is. Every structured Error wraps the sentinel
// matching its Code.
var (
	// ErrTruncatedInput: input ended before a complete field.
	ErrTruncatedInput = &Error{Code: ErrTruncated, Offset: -1, Msg: "input ended before a complete field"}
	// ErrVarintTooLarge: a varint encodes more than 64 bits.
	ErrVarintTooLarge = &Error{Code: ErrVarintOverflow, Offset: -1, Msg: "varint encodes more than 64 bits"}
	// ErrLengthTooLarge: declared length exceeds the enclosing buffer.
	ErrLengthTooLarge = &Error{Code: ErrLengthOverflow, Offset: -1, Msg: "length-delimited payload exceeds buffer"}
	// ErrInvalidTag: field number zero or a reserved wire type.
	ErrInvalidTag = &Error{Code: ErrInvalidField, Offset: -1, Msg: "invalid tag"}
	// ErrWireTypeMismatch: known field used an incompatible wire type.
	ErrWireTypeMismatch = &Error{Code: ErrWireType, Offset: -1, Msg: "wire type does not match declared field type"}
	// ErrDuplicateField: a singular field appeared more than once.
	ErrDuplicateField = &Error{Code: ErrDuplicateSingular, Offset: -1, Msg: "singular field occurs more than once"}
	// ErrPackedEncoding: malformed or illegal packed encoding.
	ErrPackedEncoding = &Error{Code: ErrPacked, Offset: -1, Msg: "invalid packed encoding"}
	// ErrNestingBudget: nesting deeper than the configured budget.
	ErrNestingBudget = &Error{Code: ErrNesting, Offset: -1, Msg: "nesting depth exceeds budget"}
	// ErrMessageTooLarge: message exceeds the configured size limit.
	ErrMessageTooLarge = &Error{Code: ErrSize, Offset: -1, Msg: "message exceeds size limit"}
	// ErrInvalidSchema: malformed schema or rule.
	ErrInvalidSchema = &Error{Code: ErrSchema, Offset: -1, Msg: "invalid schema"}
)

func itoa(n int) string {
	if n < 0 {
		return "-" + uitoa(uint(-n))
	}
	return uitoa(uint(n))
}

func uitoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
