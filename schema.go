package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MinFieldNumber and MaxFieldNumber bound the legal field number space.
// Field numbers must be positive; the upper bound mirrors the largest
// number commonly supported by tagged binary message formats.
const (
	MinFieldNumber = 1
	MaxFieldNumber = 536870911
)

// FieldType enumerates the value types a declared field may carry.
// A raw field whose Go value does not match the declared type is a
// hard validation failure (see ViolationTypeMismatch).
type FieldType int

const (
	TypeInvalid FieldType = iota
	TypeInt64
	TypeUint64
	TypeDouble
	TypeBool
	TypeString
	TypeBytes
	TypeMessage
)

// String renders the field type for diagnostics.
func (t FieldType) String() string {
	switch t {
	case TypeInt64:
		return "int64"
	case TypeUint64:
		return "uint64"
	case TypeDouble:
		return "double"
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	case TypeBytes:
		return "bytes"
	case TypeMessage:
		return "message"
	default:
		return "invalid"
	}
}

// Cardinality declares how many times a field number may appear.
type Cardinality int

const (
	// CardinalityUnknown is the zero value and is rejected by NewVersionSchema.
	CardinalityUnknown Cardinality = iota
	// CardinalitySingular allows at most one occurrence of the field.
	CardinalitySingular
	// CardinalityRepeated allows zero or more occurrences, bounded by
	// FieldSpec.MaxCount when MaxCount > 0.
	CardinalityRepeated
)

// String renders the cardinality for diagnostics.
func (c Cardinality) String() string {
	switch c {
	case CardinalitySingular:
		return "singular"
	case CardinalityRepeated:
		return "repeated"
	default:
		return "unknown"
	}
}

// NumberRange is an inclusive range of field numbers.
type NumberRange struct {
	From int
	To   int
}

func (r NumberRange) contains(n int) bool { return n >= r.From && n <= r.To }

// FieldSpec declares one known field of a message version.
type FieldSpec struct {
	// Number is the on-wire field number; it must be unique within the version.
	Number int
	// Name is the stable textual identifier; it must be unique within the version.
	Name string
	// Type constrains the dynamic Go value type accepted for this field.
	Type FieldType
	// Cardinality constrains how many occurrences are legal.
	Cardinality Cardinality
	// MaxCount, when positive, bounds the occurrence count of a repeated field.
	// A value of 0 means unbounded. It is ignored for singular fields.
	MaxCount int
}

// VersionSchema declares every known field and every reserved field number
// for one message version. Schemas are immutable once constructed.
type VersionSchema struct {
	version  string
	fields   map[int]FieldSpec
	byName   map[string]int
	reserved []NumberRange
}

// NewVersionSchema validates and compiles a message version declaration.
// It fails on invalid numbers or types, duplicate numbers/names, reserved
// numbers overlapping known fields, and malformed reserved ranges. This
// fail-fast construction prevents a vulnerable schema from ever being used.
func NewVersionSchema(version string, fields []FieldSpec, reserved []NumberRange) (*VersionSchema, error) {
	if version == "" {
		return nil, errors.New("netsec: schema version must not be empty")
	}
	s := &VersionSchema{
		version:  version,
		fields:   make(map[int]FieldSpec, len(fields)),
		byName:   make(map[string]int, len(fields)),
		reserved: append([]NumberRange(nil), reserved...),
	}
	for _, r := range s.reserved {
		if r.From < MinFieldNumber || r.To > MaxFieldNumber || r.From > r.To {
			return nil, fmt.Errorf("netsec: schema %q has invalid reserved range %d..%d", version, r.From, r.To)
		}
	}
	for _, f := range fields {
		if f.Number < MinFieldNumber || f.Number > MaxFieldNumber {
			return nil, fmt.Errorf("netsec: schema %q field %q number %d out of range", version, f.Name, f.Number)
		}
		if f.Name == "" {
			return nil, fmt.Errorf("netsec: schema %q field number %d has empty name", version, f.Number)
		}
		if f.Type == TypeInvalid {
			return nil, fmt.Errorf("netsec: schema %q field %q has invalid type", version, f.Name)
		}
		if f.Cardinality != CardinalitySingular && f.Cardinality != CardinalityRepeated {
			return nil, fmt.Errorf("netsec: schema %q field %q has invalid cardinality", version, f.Name)
		}
		if f.MaxCount < 0 {
			return nil, fmt.Errorf("netsec: schema %q field %q has negative MaxCount", version, f.Name)
		}
		if _, dup := s.fields[f.Number]; dup {
			return nil, fmt.Errorf("netsec: schema %q reuses field number %d", version, f.Number)
		}
		if _, dup := s.byName[f.Name]; dup {
			return nil, fmt.Errorf("netsec: schema %q reuses field name %q", version, f.Name)
		}
		for _, r := range s.reserved {
			if r.contains(f.Number) {
				return nil, fmt.Errorf("netsec: schema %q field %q uses reserved number %d", version, f.Name, f.Number)
			}
		}
		s.fields[f.Number] = f
		s.byName[f.Name] = f.Number
	}
	return s, nil
}

// Version returns the version identifier.
func (s *VersionSchema) Version() string { return s.version }

// Field looks up the declared spec for a field number.
func (s *VersionSchema) Field(number int) (FieldSpec, bool) {
	f, ok := s.fields[number]
	return f, ok
}

// FieldByName looks up the declared spec for a field name.
func (s *VersionSchema) FieldByName(name string) (FieldSpec, bool) {
	number, ok := s.byName[name]
	if !ok {
		return FieldSpec{}, false
	}
	return s.fields[number], true
}

// KnownNumbers returns the declared field numbers in ascending order.
func (s *VersionSchema) KnownNumbers() []int {
	numbers := make([]int, 0, len(s.fields))
	for n := range s.fields {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers
}

// IsReserved reports whether the number belongs to a reserved range.
func (s *VersionSchema) IsReserved(number int) bool {
	for _, r := range s.reserved {
		if r.contains(number) {
			return true
		}
	}
	return false
}

// Registry holds the compiled message versions used by a service.
type Registry struct {
	versions map[string]*VersionSchema
}

// NewRegistry creates an empty schema registry.
func NewRegistry() *Registry {
	return &Registry{versions: make(map[string]*VersionSchema)}
}

// Register adds a compiled version, rejecting duplicate version identifiers.
func (r *Registry) Register(s *VersionSchema) error {
	if s == nil {
		return errors.New("netsec: cannot register nil schema")
	}
	if _, exists := r.versions[s.Version()]; exists {
		return fmt.Errorf("netsec: version %q already registered", s.Version())
	}
	r.versions[s.Version()] = s
	return nil
}

// Schema returns the compiled schema for a version.
func (r *Registry) Schema(version string) (*VersionSchema, bool) {
	s, ok := r.versions[version]
	return s, ok
}
