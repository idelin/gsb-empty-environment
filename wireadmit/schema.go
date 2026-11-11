package wireadmit

import "fmt"

// Schema is the set of fields authorised for one message level. Field numbers
// not present here are treated as unknown fields: they are bounds-checked and
// retained as byte ranges but never interpreted against a Rule.
type Schema struct {
	rules map[int]*Rule
}

// NewSchema validates rules and builds an immutable Schema. Duplicate field
// numbers, non-positive field numbers, unknown Type values and missing or
// extraneous Nested schemas are rejected with an Error whose Code is
// ErrSchema. Nested schemas are validated recursively.
func NewSchema(rules []Rule) (*Schema, error) {
	s := &Schema{rules: make(map[int]*Rule, len(rules))}
	seen := make(map[int]bool, len(rules))
	for i := range rules {
		r := rules[i]
		if err := validateRule(&r, seen, map[*Schema]bool{}); err != nil {
			return nil, err
		}
		cp := r
		s.rules[r.Number] = &cp
	}
	return s, nil
}

func validateRule(r *Rule, seen map[int]bool, stack map[*Schema]bool) error {
	if r.Number <= 0 {
		return schemaErr("field number must be positive, got %d", r.Number)
	}
	if r.Number > maxFieldNumber {
		return schemaErr("field number %d exceeds %d", r.Number, maxFieldNumber)
	}
	if seen[r.Number] {
		return schemaErr("duplicate rule for field %d", r.Number)
	}
	switch r.Type {
	case Varint, Fixed64, Fixed32, Bytes:
		if r.Nested != nil {
			return schemaErr("scalar field %d must not have a nested schema", r.Number)
		}
	case Message, Group:
		if r.Nested == nil {
			return schemaErr("field %d of type %d requires a nested schema", r.Number, r.Type)
		}
		if stack[r.Nested] {
			return schemaErr("cyclic nested schema at field %d", r.Number)
		}
		if err := validateSchema(r.Nested, stack); err != nil {
			return err
		}
	default:
		return schemaErr("field %d has unknown type %d", r.Number, r.Type)
	}
	switch r.Cardinality {
	case Singular, Repeated:
	default:
		return schemaErr("field %d has invalid cardinality %d", r.Number, r.Cardinality)
	}
	seen[r.Number] = true
	return nil
}

func validateSchema(s *Schema, stack map[*Schema]bool) error {
	if s == nil {
		return schemaErr("nil nested schema")
	}
	if stack[s] {
		return schemaErr("cyclic nested schema")
	}
	stack[s] = true
	defer delete(stack, s)
	childSeen := make(map[int]bool, len(s.rules))
	for _, r := range s.rules {
		if err := validateRule(r, childSeen, stack); err != nil {
			return err
		}
	}
	return nil
}

func schemaErr(format string, args ...any) error {
	return &Error{
		Code:   ErrSchema,
		Offset: -1,
		Msg:    fmt.Sprintf(format, args...),
	}
}

// rule returns the Rule for field number, or nil when the number is unknown.
func (s *Schema) rule(number int) *Rule {
	if s == nil {
		return nil
	}
	return s.rules[number]
}

const maxFieldNumber = (1 << 29) - 1

// expectedWireType reports the single non-group wire type allowed for t.
// Groups use wire types 3 (start) and 4 (end) and are handled separately.
func expectedWireType(t Type) (int, bool) {
	switch t {
	case Varint:
		return 0, true
	case Fixed64:
		return 1, true
	case Bytes, Message:
		return 2, true
	case Fixed32:
		return 5, true
	default:
		return 0, false
	}
}

// packable reports whether t may use length-delimited packed encoding. Only
// repeated scalar numeric types are packable; bytes, messages and groups are
// not.
func packable(t Type) bool {
	switch t {
	case Varint, Fixed64, Fixed32:
		return true
	default:
		return false
	}
}
