package shrink

import (
	"errors"
	"math"
	"strconv"
)

func newInvalidSchemaError() error { return errors.New("shrink: invalid schema") }

// Kind enumerates the supported field value domains.
type Kind int

const (
	// IntegerKind enumerates the inclusive range [Min, Max].
	IntegerKind Kind = iota + 1
	// StringKind enumerates one representative string per length [0, MaxLen].
	StringKind
)

func (k Kind) String() string {
	switch k {
	case IntegerKind:
		return "integer"
	case StringKind:
		return "string"
	default:
		return "unknown(" + strconv.Itoa(int(k)) + ")"
	}
}

// IntegerField declares an integer field whose domain is [Min, Max].
type IntegerField struct {
	Name string
	Min  int64
	Max  int64
}

// StringField declares a string field whose domain contains one
// representative value for every length in [0, MaxLen].
type StringField struct {
	Name   string
	MaxLen int
}

// Field is one position of the structured input. When Optional is false the
// field is present in every candidate.
type Field struct {
	Optional bool
	Integer  *IntegerField
	String   *StringField
}

// Schema is the immutable description of a minimization input domain.
type Schema struct {
	name       string
	assertion  string
	fields     []Field
	optional   int
	values     [][]val
	integerIdx []bool
}

// NewSchema validates the field declarations and builds an immutable schema.
// assertionID is the original failing assertion identifier and is retained by
// every generated candidate.
func NewSchema(name, assertionID string, fields []Field) (*Schema, error) {
	if name == "" {
		return nil, errors.Join(ErrInvalidSchema, errors.New("schema name is empty"))
	}
	if assertionID == "" {
		return nil, errors.Join(ErrInvalidSchema, errors.New("assertion id is empty"))
	}
	if len(fields) == 0 {
		return nil, errors.Join(ErrInvalidSchema, errors.New("at least one field is required"))
	}
	if len(fields) > MaxTotalFields {
		return nil, errors.Join(ErrInvalidSchema,
			errors.New("at most "+strconv.Itoa(MaxTotalFields)+" fields, got "+strconv.Itoa(len(fields))))
	}

	optional := 0
	for _, f := range fields {
		if f.Optional {
			optional++
		}
	}
	if optional > MaxOptionalFields {
		return nil, errors.Join(ErrInvalidSchema,
			errors.New("at most "+strconv.Itoa(MaxOptionalFields)+" optional fields, got "+strconv.Itoa(optional)))
	}

	s := &Schema{
		name:       name,
		assertion:  assertionID,
		fields:     append([]Field(nil), fields...),
		optional:   optional,
		integerIdx: make([]bool, len(fields)),
		values:     make([][]val, len(fields)),
	}

	for i, f := range fields {
		switch {
		case f.Integer != nil && f.String != nil:
			return nil, fieldError(i, "field declares both integer and string domains")
		case f.Integer == nil && f.String == nil:
			return nil, fieldError(i, "field declares neither integer nor string domain")
		case f.Integer != nil:
			iv := f.Integer
			if iv.Name == "" {
				return nil, fieldError(i, "integer field name is empty")
			}
			if iv.Min > iv.Max {
				return nil, fieldError(i, "integer min is greater than max")
			}
			if iv.Max-iv.Min > MaxIntRange {
				return nil, fieldError(i, "integer range exceeds "+strconv.Itoa(MaxIntRange))
			}
			s.integerIdx[i] = true
			s.values[i] = integerValues(iv.Min, iv.Max)
		default:
			sf := f.String
			if sf.Name == "" {
				return nil, fieldError(i, "string field name is empty")
			}
			if sf.MaxLen < 0 {
				return nil, fieldError(i, "string max length is negative")
			}
			if sf.MaxLen > MaxStringLen {
				return nil, fieldError(i, "string max length exceeds "+strconv.Itoa(MaxStringLen))
			}
			s.values[i] = stringValues(sf.MaxLen)
		}
	}
	return s, nil
}

func fieldError(i int, msg string) error {
	return errors.Join(ErrInvalidSchema, errors.New("field "+strconv.Itoa(i)+": "+msg))
}

// AssertionID returns the original failing assertion identifier retained by
// the schema and every candidate.
func (s *Schema) AssertionID() string { return s.assertion }

// Name returns the schema name.
func (s *Schema) Name() string { return s.name }

// FieldCount returns the total number of declared fields.
func (s *Schema) FieldCount() int { return len(s.fields) }

// OptionalCount returns the number of optional fields.
func (s *Schema) OptionalCount() int { return s.optional }

// Field returns a copy of field i's declaration.
func (s *Schema) Field(i int) (Field, bool) {
	if i < 0 || i >= len(s.fields) {
		return Field{}, false
	}
	return s.fields[i], true
}

// Kind reports the value kind of field i.
func (s *Schema) Kind(i int) Kind {
	if i < 0 || i >= len(s.fields) {
		return 0
	}
	if s.integerIdx[i] {
		return IntegerKind
	}
	return StringKind
}

// Size reports the cardinality of the full domain, counting absence as one
// choice per optional field. It saturates at math.MaxInt when the exact
// product does not fit in an int.
func (s *Schema) Size() int {
	size := 1
	for i, f := range s.fields {
		factor := len(s.values[i])
		if f.Optional {
			factor++
		}
		if factor == 0 || size > math.MaxInt/factor {
			return math.MaxInt
		}
		size *= factor
	}
	return size
}

// maskBytes returns the serialized width of the presence mask.
func (s *Schema) maskBytes() int {
	if s.optional == 0 {
		return 0
	}
	return (s.optional + 7) / 8
}
