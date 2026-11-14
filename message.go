package netsec

import "sort"

// RawField is one decoded field occurrence as received off the wire.
// Repeated fields appear as multiple RawField entries sharing one Number.
type RawField struct {
	Number int
	Value  any
}

// RawMessage is an ordered sequence of decoded field occurrences.
type RawMessage []RawField

// KnownFieldReader is the only surface downstream consumers should depend on.
// It exposes declared, validated fields exclusively; injected or unknown
// fields are unreachable through this interface.
type KnownFieldReader interface {
	// Version returns the schema version the message was sanitized against.
	Version() string
	// Numbers returns the populated known field numbers in ascending order.
	Numbers() []int
	// Get returns the values of a known field. A singular field returns at
	// most one value; an absent field returns nil.
	Get(number int) []any
	// GetByName resolves a field by its declared name.
	GetByName(name string) ([]any, bool)
}

// MessageStats summarizes how a message was handled by the policy.
type MessageStats struct {
	// KnownAccepted counts occurrences that matched the declared schema.
	KnownAccepted int
	// UnknownDropped counts unknown occurrences removed by the policy.
	UnknownDropped int
	// UnknownQuarantined counts unknown occurrences held in the quarantine.
	UnknownQuarantined int
	// ReservedRejected counts occurrences whose number is reserved.
	ReservedRejected int
}

// SanitizedMessage is the validated, downstream-safe view of a message.
// Known fields are readable through KnownFieldReader; quarantined fields are
// kept apart from the readable view so downstream code cannot consume them
// accidentally, while operators may still inspect the raw payload.
type SanitizedMessage struct {
	schema     *VersionSchema
	known      map[int][]any
	quarantine []RawField
	stats      MessageStats
}

var _ KnownFieldReader = (*SanitizedMessage)(nil)

// Version implements KnownFieldReader.
func (m *SanitizedMessage) Version() string { return m.schema.Version() }

// Numbers implements KnownFieldReader.
func (m *SanitizedMessage) Numbers() []int {
	numbers := make([]int, 0, len(m.known))
	for n := range m.known {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers
}

// Get implements KnownFieldReader.
func (m *SanitizedMessage) Get(number int) []any {
	values := m.known[number]
	if len(values) == 0 {
		return nil
	}
	out := make([]any, len(values))
	copy(out, values)
	return out
}

// GetByName implements KnownFieldReader.
func (m *SanitizedMessage) GetByName(name string) ([]any, bool) {
	spec, ok := m.schema.FieldByName(name)
	if !ok {
		return nil, false
	}
	return m.Get(spec.Number), true
}

// Quarantine returns a copy of the raw fields isolated by the policy.
// These fields never appear in the readable known-field view.
func (m *SanitizedMessage) Quarantine() []RawField {
	if len(m.quarantine) == 0 {
		return nil
	}
	out := make([]RawField, len(m.quarantine))
	copy(out, m.quarantine)
	return out
}

// Stats reports policy counters for observability and alerting.
func (m *SanitizedMessage) Stats() MessageStats { return m.stats }

// matchesType reports whether the dynamic value is assignable to the
// declared field type. TypeMessage accepts nested RawMessage values only.
func matchesType(t FieldType, value any) bool {
	switch t {
	case TypeInt64:
		_, ok := value.(int64)
		return ok
	case TypeUint64:
		_, ok := value.(uint64)
		return ok
	case TypeDouble:
		_, ok := value.(float64)
		return ok
	case TypeBool:
		_, ok := value.(bool)
		return ok
	case TypeString:
		_, ok := value.(string)
		return ok
	case TypeBytes:
		_, ok := value.([]byte)
		return ok
	case TypeMessage:
		_, ok := value.(RawMessage)
		return ok
	default:
		return false
	}
}
