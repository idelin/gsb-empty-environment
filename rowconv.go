// Package rowconv implements a declarative row converter that moves values
// from a source schema to a target schema without silent precision loss.
//
// Supported types: signed integers, fixed-point decimals (integer
// coefficient + scale), UTF-8 strings, byte blobs, and timestamps with an
// explicit offset (stored canonically as UTC integer microseconds).
//
// Any conversion that cannot be represented exactly in the target type is
// rejected by default; callers may explicitly opt into ties-to-even
// rounding. Row conversion is atomic: a failed row yields no partial
// output. Batch conversion preserves input order.
package rowconv

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// Type is a supported column type.
type Type string

const (
	TypeInt       Type = "int"
	TypeDecimal   Type = "decimal"
	TypeString    Type = "string"
	TypeBytes     Type = "bytes"
	TypeTimestamp Type = "timestamp"
)

// RoundingMode controls how inexact conversions are handled.
type RoundingMode string

const (
	// Reject refuses any conversion that cannot be represented exactly.
	Reject RoundingMode = "reject"
	// TiesToEven rounds discarded fraction digits to the nearest
	// representable value, breaking ties toward the even neighbor.
	TiesToEven RoundingMode = "ties-to-even"
)

// Options configures conversion behavior.
type Options struct {
	Rounding RoundingMode `json:"rounding"`
}

func (o Options) rounding() RoundingMode {
	if o.Rounding == TiesToEven {
		return TiesToEven
	}
	return Reject
}

// Error describes a conversion failure on a specific field.
type Error struct {
	Field   string
	Message string
}

func (e *Error) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("field %q: %s", e.Field, e.Message)
}

func fieldErr(field, format string, args ...interface{}) *Error {
	return &Error{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Decimal is a fixed-point decimal: Coefficient * 10^-Scale.
type Decimal struct {
	Coefficient *big.Int
	Scale       int
}

func (d *Decimal) String() string {
	return fmt.Sprintf("%sE-%d", d.Coefficient.String(), d.Scale)
}

// Value is a typed column value. Kind selects the active member.
type Value struct {
	Kind   Type
	Int    int64
	Dec    *Decimal
	Str    string
	Bytes  []byte
	Micros int64 // timestamp: UTC microseconds since the Unix epoch
}

// Row maps field names to values. A nil *Value is an explicit null;
// an absent key is a missing field. The two are distinct.
type Row map[string]*Value

// Field describes one column of a schema.
type Field struct {
	Name      string `json:"name"`
	Type      Type   `json:"type"`
	Bits      int    `json:"bits,omitempty"`      // int: 8, 16, 32, 64 (default 64)
	Precision int    `json:"precision,omitempty"` // decimal: max coefficient digits (0 = unlimited)
	Scale     int    `json:"scale,omitempty"`     // decimal: fractional digits
	MaxBytes  int    `json:"maxBytes,omitempty"`  // string/bytes: byte length limit (0 = unlimited)
	Required  bool   `json:"required,omitempty"`
	Nullable  bool   `json:"nullable,omitempty"`
	Default   *Value `json:"default,omitempty"`
}

// Schema is an ordered list of fields.
type Schema struct {
	Fields []Field `json:"fields"`
}

func (s *Schema) byName() map[string]*Field {
	m := make(map[string]*Field, len(s.Fields))
	for i := range s.Fields {
		m[s.Fields[i].Name] = &s.Fields[i]
	}
	return m
}

// Validate checks schema internal consistency.
func (s *Schema) Validate() error {
	seen := make(map[string]bool, len(s.Fields))
	for i := range s.Fields {
		f := &s.Fields[i]
		if f.Name == "" {
			return errors.New("rowconv: field with empty name")
		}
		if seen[f.Name] {
			return fmt.Errorf("rowconv: duplicate field %q", f.Name)
		}
		seen[f.Name] = true
		switch f.Type {
		case TypeInt:
			switch f.Bits {
			case 0, 8, 16, 32, 64:
			default:
				return fmt.Errorf("rowconv: field %q: invalid int bits %d", f.Name, f.Bits)
			}
		case TypeDecimal:
			if f.Scale < 0 {
				return fmt.Errorf("rowconv: field %q: negative scale", f.Name)
			}
			if f.Precision < 0 {
				return fmt.Errorf("rowconv: field %q: negative precision", f.Name)
			}
		case TypeString, TypeBytes:
			if f.MaxBytes < 0 {
				return fmt.Errorf("rowconv: field %q: negative maxBytes", f.Name)
			}
		case TypeTimestamp:
		default:
			return fmt.Errorf("rowconv: field %q: unknown type %q", f.Name, f.Type)
		}
		if f.Default != nil && f.Default.Kind != f.Type {
			return fmt.Errorf("rowconv: field %q: default kind %s does not match field type %s",
				f.Name, f.Default.Kind, f.Type)
		}
	}
	return nil
}

// UnmarshalJSON decodes a Field, interpreting "default" according to the
// declared field type.
func (f *Field) UnmarshalJSON(data []byte) error {
	type wire Field
	var w struct {
		wire
		Default json.RawMessage `json:"default"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*f = Field(w.wire)
	if len(w.Default) != 0 && string(w.Default) != "null" {
		v, err := decodeJSONValue(f.Type, w.Default, Reject)
		if err != nil {
			return fmt.Errorf("field %q: invalid default: %v", f.Name, err)
		}
		f.Default = v
	}
	return nil
}

// MarshalJSON encodes a value in its canonical wire form: ints and
// timestamps (UTC micros) as numbers, decimals as
// {"coefficient": "...", "scale": n}, strings as strings, bytes as base64.
func (v *Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case TypeInt:
		return json.Marshal(v.Int)
	case TypeDecimal:
		return json.Marshal(struct {
			Coefficient string `json:"coefficient"`
			Scale       int    `json:"scale"`
		}{v.Dec.Coefficient.String(), v.Dec.Scale})
	case TypeString:
		return json.Marshal(v.Str)
	case TypeBytes:
		return json.Marshal(v.Bytes)
	case TypeTimestamp:
		return json.Marshal(v.Micros)
	}
	return nil, fmt.Errorf("rowconv: cannot marshal value of kind %q", v.Kind)
}

// decodeJSONValue parses one JSON-encoded value for the given type.
// Timestamps accept an RFC3339 string with explicit offset (converted to
// UTC micros per mode) or an integer already in UTC micros.
func decodeJSONValue(t Type, raw json.RawMessage, mode RoundingMode) (*Value, error) {
	switch t {
	case TypeInt:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("expected JSON integer: %v", err)
		}
		return &Value{Kind: TypeInt, Int: n}, nil
	case TypeDecimal:
		var d struct {
			Coefficient string `json:"coefficient"`
			Scale       int    `json:"scale"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("expected decimal object: %v", err)
		}
		c, ok := new(big.Int).SetString(d.Coefficient, 10)
		if !ok {
			return nil, fmt.Errorf("invalid decimal coefficient %q", d.Coefficient)
		}
		if d.Scale < 0 {
			return nil, fmt.Errorf("negative decimal scale %d", d.Scale)
		}
		return &Value{Kind: TypeDecimal, Dec: &Decimal{Coefficient: c, Scale: d.Scale}}, nil
	case TypeString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("expected JSON string: %v", err)
		}
		return &Value{Kind: TypeString, Str: s}, nil
	case TypeBytes:
		var b []byte
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("expected base64 string: %v", err)
		}
		return &Value{Kind: TypeBytes, Bytes: b}, nil
	case TypeTimestamp:
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			micros, err := ParseTimestamp(s, mode)
			if err != nil {
				return nil, err
			}
			return &Value{Kind: TypeTimestamp, Micros: micros}, nil
		}
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, errors.New("expected RFC3339 string or integer UTC micros")
		}
		return &Value{Kind: TypeTimestamp, Micros: n}, nil
	}
	return nil, fmt.Errorf("unknown type %q", t)
}

// DecodeRow parses a JSON object into a Row according to the source schema.
// Keys absent from the schema are ignored. A JSON null becomes an explicit
// null (nil *Value), distinct from a missing key.
func DecodeRow(schema *Schema, raw map[string]json.RawMessage, opts Options) (Row, error) {
	byName := schema.byName()
	row := Row{}
	for name, rawVal := range raw {
		f, ok := byName[name]
		if !ok {
			continue
		}
		if string(rawVal) == "null" {
			row[name] = nil
			continue
		}
		v, err := decodeJSONValue(f.Type, rawVal, opts.rounding())
		if err != nil {
			return nil, fieldErr(name, "decode: %v", err)
		}
		row[name] = v
	}
	return row, nil
}
