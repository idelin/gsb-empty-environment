package fieldbinding

import "errors"

var (
	// ErrDuplicateID reports a field ID used twice in one schema.
	ErrDuplicateID = errors.New("fieldbinding: duplicate field ID")
	// ErrUnknownField reports a batch leaf field whose ID does not exist
	// in the table schema. A matching name with a different ID is still
	// an unknown field: binding is by ID only.
	ErrUnknownField = errors.New("fieldbinding: batch field has no matching table field ID")
	// ErrMissingField reports a required table field absent from the
	// batch schema.
	ErrMissingField = errors.New("fieldbinding: required table field absent from batch schema")
	// ErrIncompatibleType reports a type change that is not a lossless
	// widening.
	ErrIncompatibleType = errors.New("fieldbinding: incompatible field types")
	// ErrIllegalMove reports a subtree move across a non-nullable path.
	ErrIllegalMove = errors.New("fieldbinding: move requires a nullable subtree")
	// ErrNullability reports a nullable batch field bound to a required
	// table field.
	ErrNullability = errors.New("fieldbinding: batch field may be null but table field is required")
	// ErrMissingValue reports a record that omits a required field. The
	// writer refuses to fabricate a null in place of the missing value.
	ErrMissingValue = errors.New("fieldbinding: missing value for required field")
	// ErrUnexpectedNull reports an explicit null for a non-nullable field.
	ErrUnexpectedNull = errors.New("fieldbinding: explicit null for non-nullable field")
	// ErrRange reports a value that does not fit the target type.
	ErrRange = errors.New("fieldbinding: value does not fit target type")
	// ErrKind reports a value whose representation does not match the
	// field type.
	ErrKind = errors.New("fieldbinding: value representation does not match field type")
)
