package wire

import (
	"errors"
	"fmt"
)

// FieldKind describes the logical protobuf type of a field, used by the
// reflection validation engine to check wire types and materialize values.
type FieldKind int

const (
	KindVarint        FieldKind = iota // int32/int64/uint32/uint64/bool/enum
	KindSVarint                        // sint32/sint64 (ZigZag)
	KindFixed32                        // fixed32/sfixed32/float bits
	KindFixed64                        // fixed64/sfixed64/double bits
	KindBytes                          // bytes
	KindString                         // string (length-delimited)
	KindMessage                        // nested message (length-delimited)
	KindPackedVarint                   // packed repeated varint
	KindPackedFixed32                  // packed repeated fixed32
	KindPackedFixed64                  // packed repeated fixed64
)

// ErrValidation marks any schema/validation failure.
var ErrValidation = errors.New("wire: schema validation failed")

// Field describes one schema field.
type Field struct {
	Name     string
	Number   int
	Kind     FieldKind
	Repeated bool    // non-packed repeated (one occurrence per element)
	Required bool    // must appear at least once
	Nested   *Schema // for KindMessage
}

// Schema is a reflection descriptor for a message type.
type Schema struct {
	Name   string
	Fields []Field
}

func (k FieldKind) wireType() WireType {
	switch k {
	case KindVarint, KindSVarint:
		return WireVarint
	case KindFixed32:
		return WireFixed32
	case KindFixed64:
		return WireFixed64
	default: // bytes, string, message, packed*
		return WireLengthDelimited
	}
}

// Validate checks a parsed Message against the schema: wire-type agreement,
// required-field presence, well-formed packed payloads, and recursive
// validation of nested messages.
func (s *Schema) Validate(m Message) error {
	_, err := s.Decode(m)
	return err
}

// Decode validates and materializes the message into a map of field name to
// Go value: uint64 (varint), int64 (svarint), uint32, uint64, string,
// []byte, []uint64, []uint32, []interface{} for repeated, and nested
// map[string]interface{} for sub-messages.
func (s *Schema) Decode(m Message) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(s.Fields))
	for _, f := range s.Fields {
		vals, present := m[f.Number]
		if !present || len(vals) == 0 {
			if f.Required {
				return nil, errRequired(s.Name, f.Name)
			}
			continue
		}
		v, err := s.decodeField(f, vals)
		if err != nil {
			return nil, err
		}
		out[f.Name] = v
	}
	return out, nil
}

func errRequired(schema, field string) error {
	return fmt.Errorf("%v: %s.%s is required", ErrValidation, schema, field)
}

func (s *Schema) decodeField(f Field, vals []Value) (interface{}, error) {
	want := f.Kind.wireType()
	for _, v := range vals {
		if v.Wire != want {
			return nil, fmt.Errorf("%v: %s.%s wire type %d, want %d",
				ErrValidation, s.Name, f.Name, v.Wire, want)
		}
	}
	switch f.Kind {
	case KindMessage:
		if f.Repeated {
			list := make([]interface{}, 0, len(vals))
			for _, v := range vals {
				sub, err := s.decodeNested(f, v.Bytes)
				if err != nil {
					return nil, err
				}
				list = append(list, sub)
			}
			return list, nil
		}
		return s.decodeNested(f, vals[len(vals)-1].Bytes)
	case KindPackedVarint, KindPackedFixed32, KindPackedFixed64:
		return decodePacked(f, vals)
	}
	if f.Repeated {
		list := make([]interface{}, 0, len(vals))
		for _, v := range vals {
			list = append(list, scalar(f.Kind, v))
		}
		return list, nil
	}
	return scalar(f.Kind, vals[len(vals)-1]), nil
}

func (s *Schema) decodeNested(f Field, raw []byte) (map[string]interface{}, error) {
	if f.Nested == nil {
		return nil, fmt.Errorf("%v: %s.%s has no nested schema", ErrValidation, s.Name, f.Name)
	}
	sub, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%v: %s.%s nested parse: %v", ErrValidation, s.Name, f.Name, err)
	}
	out, err := f.Nested.Decode(sub)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func scalar(k FieldKind, v Value) interface{} {
	switch k {
	case KindVarint:
		return v.U64
	case KindSVarint:
		return int64(v.U64>>1) ^ -int64(v.U64&1)
	case KindFixed32:
		return uint32(v.U64)
	case KindFixed64:
		return v.U64
	case KindString:
		return string(v.Bytes)
	default: // KindBytes
		return v.Bytes
	}
}

func decodePacked(f Field, vals []Value) (interface{}, error) {
	switch f.Kind {
	case KindPackedVarint:
		var out []uint64
		for _, v := range vals {
			d := NewDecoder(v.Bytes)
			for !d.EOF() {
				u, err := d.ReadVarint()
				if err != nil {
					return nil, fmt.Errorf("%v: packed field %s: %v", ErrValidation, f.Name, err)
				}
				out = append(out, u)
			}
		}
		return out, nil
	case KindPackedFixed32:
		var out []uint32
		for _, v := range vals {
			if len(v.Bytes)%4 != 0 {
				return nil, fmt.Errorf("%v: packed field %s: length %d not multiple of 4",
					ErrValidation, f.Name, len(v.Bytes))
			}
			d := NewDecoder(v.Bytes)
			for !d.EOF() {
				u, _ := d.ReadFixed32()
				out = append(out, u)
			}
		}
		return out, nil
	default: // KindPackedFixed64
		var out []uint64
		for _, v := range vals {
			if len(v.Bytes)%8 != 0 {
				return nil, fmt.Errorf("%v: packed field %s: length %d not multiple of 8",
					ErrValidation, f.Name, len(v.Bytes))
			}
			d := NewDecoder(v.Bytes)
			for !d.EOF() {
				u, _ := d.ReadFixed64()
				out = append(out, u)
			}
		}
		return out, nil
	}
}
