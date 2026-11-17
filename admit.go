package netsec

import (
	"errors"
	"fmt"
)

// FieldKind describes how a field's length is represented on the wire.
type FieldKind uint8

const (
	// Fixed means the field has exactly MaxLength value bytes and no prefix.
	Fixed FieldKind = iota
	// LLVAR means the field starts with a two-digit ASCII length prefix.
	LLVAR
	// LLLVAR means the field starts with a three-digit ASCII length prefix.
	LLLVAR
)

func (k FieldKind) valid() bool {
	return k == Fixed || k == LLVAR || k == LLLVAR
}

func (k FieldKind) prefixDigits() int {
	switch k {
	case LLVAR:
		return 2
	case LLLVAR:
		return 3
	default:
		return 0
	}
}

// Field is one entry in a caller-supplied field table.
//
// For Fixed fields MaxLength is the exact value length. For LLVAR and LLLVAR
// fields it is the upper bound accepted for the value, excluding the length
// prefix.
type Field struct {
	Kind      FieldKind
	MaxLength int
}

// FieldTable maps ISO 8583 field numbers (2 through 128) to their layout.
// Field 1 is reserved for the secondary-bitmap indicator and must not appear
// in the table.
type FieldTable map[int]Field

// Config configures an Admitter.
type Config struct {
	// Fields is the complete allow-list of fields for this deployment.
	Fields FieldTable

	// PINBlockMTIs lists the only message types permitted to carry field 52.
	// Matching is exact and case-sensitive.
	PINBlockMTIs []string

	// AllowTrailing permits bytes after the last bitmap-selected field. It is
	// disabled by default so that unparsed attacker-controlled bytes are not
	// silently accepted.
	AllowTrailing bool
}

// FieldValue describes where a decoded field value lives in the input.
//
// Offset is relative to the byte slice passed to Admit or to the complete
// message passed to AdmitMessage. Length excludes any length prefix.
type FieldValue struct {
	Number int
	Offset int
	Length int
}

// Bytes returns the field value from the original input without copying it.
// It returns nil if the value is outside input.
func (f FieldValue) Bytes(input []byte) []byte {
	if f.Offset < 0 || f.Length < 0 || f.Offset > len(input) {
		return nil
	}
	if f.Length > len(input)-f.Offset {
		return nil
	}
	return input[f.Offset : f.Offset+f.Length]
}

// Result is the structural admission result supplied to a message decoder.
type Result struct {
	MTI             string
	BitmapOffset    int
	BitmapLength    int
	SecondaryBitmap bool
	Fields          []FieldValue
	TrailingLength  int
}

// Field returns the admitted field with the given ISO 8583 field number.
func (r Result) Field(number int) (FieldValue, bool) {
	for _, f := range r.Fields {
		if f.Number == number {
			return f, true
		}
	}
	return FieldValue{}, false
}

// Admitter validates bitmap-selected fields before a decoder reads them.
// It is immutable after construction and safe for concurrent use.
type Admitter struct {
	fields        FieldTable
	pinBlockMTIs  map[string]struct{}
	allowTrailing bool
}

// New builds an Admitter from a field table and the message types allowed to
// carry field 52.
func New(fields FieldTable, pinBlockMTIs []string) (*Admitter, error) {
	return NewWithConfig(Config{
		Fields:       fields,
		PINBlockMTIs: pinBlockMTIs,
	})
}

// NewWithConfig builds an Admitter from cfg.
func NewWithConfig(cfg Config) (*Admitter, error) {
	fields := make(FieldTable, len(cfg.Fields))
	for number, field := range cfg.Fields {
		if err := validateField(number, field); err != nil {
			return nil, err
		}
		fields[number] = field
	}

	pinBlockMTIs := make(map[string]struct{}, len(cfg.PINBlockMTIs))
	for _, mti := range cfg.PINBlockMTIs {
		if !validMTI(mti) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidMTI, mti)
		}
		pinBlockMTIs[mti] = struct{}{}
	}

	return &Admitter{
		fields:        fields,
		pinBlockMTIs:  pinBlockMTIs,
		allowTrailing: cfg.AllowTrailing,
	}, nil
}

// Admit validates data, which starts at the primary bitmap (it does not
// include the four-byte MTI).
func (a *Admitter) Admit(mti string, data []byte) (Result, error) {
	if !validMTI(mti) {
		return Result{}, newAdmissionError("admit", 0, 0, ErrInvalidMTI)
	}
	return a.admit(mti, data, 0)
}

// AdmitMessage validates a complete message consisting of a four-byte MTI,
// the bitmap, and the selected fields. No bytes are logged or copied into
// errors.
func (a *Admitter) AdmitMessage(message []byte) (Result, error) {
	if len(message) < 4 {
		return Result{}, newAdmissionError("admit message", 0, 0, ErrTruncatedMTI)
	}
	mti := string(message[:4])
	if !validMTI(mti) {
		return Result{}, newAdmissionError("admit message", 0, 0, ErrInvalidMTI)
	}
	return a.admit(mti, message, 4)
}

func (a *Admitter) admit(mti string, input []byte, bitmapOffset int) (Result, error) {
	result := Result{
		MTI:          mti,
		BitmapOffset: bitmapOffset,
	}

	if len(input)-bitmapOffset < 8 {
		return Result{}, newAdmissionError("read primary bitmap", 0, bitmapOffset, ErrTruncatedBitmap)
	}

	bitmapLength := 8
	if input[bitmapOffset]&0x80 != 0 {
		bitmapLength = 16
		result.SecondaryBitmap = true
		if len(input)-bitmapOffset < bitmapLength {
			return Result{}, newAdmissionError("read secondary bitmap", 0, bitmapOffset, ErrTruncatedBitmap)
		}
	}
	result.BitmapLength = bitmapLength

	bitmap := input[bitmapOffset : bitmapOffset+bitmapLength]
	lastField := bitmapLength * 8

	// Reject the whole bitmap before interpreting any field length. This makes
	// admission independent of attacker-controlled field contents.
	for number := 2; number <= lastField; number++ {
		if !bitmapBit(bitmap, number) {
			continue
		}
		if _, ok := a.fields[number]; !ok {
			return Result{}, newAdmissionError("check bitmap", number, bitmapOffset, ErrUndefinedField)
		}
	}

	if bitmapBit(bitmap, 52) {
		if _, ok := a.pinBlockMTIs[mti]; !ok {
			return Result{}, newAdmissionError("check field 52", 52, bitmapOffset, ErrPINBlockNotAllowed)
		}
	}

	offset := bitmapOffset + bitmapLength
	for number := 2; number <= lastField; number++ {
		if !bitmapBit(bitmap, number) {
			continue
		}

		field := a.fields[number]
		length, prefixLength, err := readFieldLength(input, offset, field)
		if err != nil {
			return Result{}, newAdmissionError("read field length", number, offset, err)
		}

		valueOffset := offset + prefixLength
		if length > len(input)-valueOffset {
			return Result{}, newAdmissionError("read field", number, valueOffset, ErrLengthExceedsRemaining)
		}
		if number == 52 && length != 8 {
			return Result{}, newAdmissionError("validate field 52", 52, valueOffset, ErrPINBlockLength)
		}

		result.Fields = append(result.Fields, FieldValue{
			Number: number,
			Offset: valueOffset,
			Length: length,
		})
		offset = valueOffset + length
	}

	result.TrailingLength = len(input) - offset
	if result.TrailingLength != 0 && !a.allowTrailing {
		return Result{}, newAdmissionError("check trailing bytes", 0, offset, ErrTrailingData)
	}

	return result, nil
}

func readFieldLength(input []byte, offset int, field Field) (length int, prefixLength int, err error) {
	switch field.Kind {
	case Fixed:
		if field.MaxLength > len(input)-offset {
			return 0, 0, ErrTruncatedField
		}
		return field.MaxLength, 0, nil
	case LLVAR, LLLVAR:
		prefixLength = field.Kind.prefixDigits()
		if prefixLength > len(input)-offset {
			return 0, 0, ErrTruncatedLengthPrefix
		}
		for _, digit := range input[offset : offset+prefixLength] {
			if digit < '0' || digit > '9' {
				return 0, 0, ErrInvalidLengthPrefix
			}
			length = length*10 + int(digit-'0')
		}
		if length > field.MaxLength {
			return 0, 0, ErrFieldLimitExceeded
		}
		if length > len(input)-(offset+prefixLength) {
			return 0, 0, ErrLengthExceedsRemaining
		}
		return length, prefixLength, nil
	default:
		return 0, 0, ErrInvalidFieldTable
	}
}

func bitmapBit(bitmap []byte, number int) bool {
	if number < 1 || number > len(bitmap)*8 {
		return false
	}
	index := number - 1
	return bitmap[index/8]&(0x80>>uint(index%8)) != 0
}

func validateField(number int, field Field) error {
	if number < 2 || number > 128 {
		return fmt.Errorf("%w: field number %d is outside 2..128", ErrInvalidFieldTable, number)
	}
	if !field.Kind.valid() {
		return fmt.Errorf("%w: field %d has an unknown kind", ErrInvalidFieldTable, number)
	}
	if field.MaxLength <= 0 {
		return fmt.Errorf("%w: field %d has a non-positive maximum length", ErrInvalidFieldTable, number)
	}
	switch field.Kind {
	case LLVAR:
		if field.MaxLength > 99 {
			return fmt.Errorf("%w: LLVAR field %d maximum exceeds 99", ErrInvalidFieldTable, number)
		}
	case LLLVAR:
		if field.MaxLength > 999 {
			return fmt.Errorf("%w: LLLVAR field %d maximum exceeds 999", ErrInvalidFieldTable, number)
		}
	}
	return nil
}

func validMTI(mti string) bool {
	if len(mti) != 4 {
		return false
	}
	for i := 0; i < len(mti); i++ {
		if mti[i] < '0' || mti[i] > '9' {
			return false
		}
	}
	return true
}

// Sentinel errors. AdmissionError wraps these with structural context; it
// never includes field values or raw message bytes.
var (
	ErrTruncatedMTI           = errors.New("iso8583: truncated message type")
	ErrInvalidMTI             = errors.New("iso8583: message type must be four ASCII digits")
	ErrTruncatedBitmap        = errors.New("iso8583: truncated bitmap")
	ErrUndefinedField         = errors.New("iso8583: bitmap selects a field absent from the field table")
	ErrInvalidFieldTable      = errors.New("iso8583: invalid field table")
	ErrTruncatedField         = errors.New("iso8583: truncated fixed field")
	ErrTruncatedLengthPrefix  = errors.New("iso8583: truncated variable-length prefix")
	ErrInvalidLengthPrefix    = errors.New("iso8583: variable-length prefix is not decimal")
	ErrLengthExceeded         = errors.New("iso8583: length exceeds permitted bound")
	ErrFieldLimitExceeded     = fmt.Errorf("%w: field maximum", ErrLengthExceeded)
	ErrLengthExceedsRemaining = fmt.Errorf("%w: remaining message bytes", ErrLengthExceeded)
	ErrPINBlockNotAllowed     = errors.New("iso8583: field 52 is not allowed for this message type")
	ErrPINBlockLength         = errors.New("iso8583: field 52 must be exactly 8 bytes")
	ErrTrailingData           = errors.New("iso8583: bytes remain after the last selected field")
)

// AdmissionError reports a structural rejection without exposing message
// contents.
type AdmissionError struct {
	Op     string
	Field  int
	Offset int
	Err    error
}

func newAdmissionError(op string, field, offset int, err error) *AdmissionError {
	return &AdmissionError{Op: op, Field: field, Offset: offset, Err: err}
}

func (e *AdmissionError) Error() string {
	switch {
	case e.Field > 0:
		return fmt.Sprintf("iso8583: %s: field %d at offset %d: %v", e.Op, e.Field, e.Offset, e.Err)
	case e.Offset > 0:
		return fmt.Sprintf("iso8583: %s: offset %d: %v", e.Op, e.Offset, e.Err)
	default:
		return fmt.Sprintf("iso8583: %s: %v", e.Op, e.Err)
	}
}

func (e *AdmissionError) Unwrap() error {
	return e.Err
}
