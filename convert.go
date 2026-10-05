package rowconv

import (
	"fmt"
	"unicode/utf8"
)

// Converter is a declarative row converter: source schema, target schema,
// field mapping (target name -> source name) and narrowing config.
type Converter struct {
	source  Schema
	target  Schema
	mapping map[string]string
	config  Config
}

// NewConverter validates schemas and config and returns a ready converter.
// Fields absent from mapping default to the same source name.
func NewConverter(source, target Schema, mapping map[string]string, config Config) (*Converter, error) {
	if err := source.validate(); err != nil {
		return nil, fmt.Errorf("source schema: %v", err)
	}
	if err := target.validate(); err != nil {
		return nil, fmt.Errorf("target schema: %v", err)
	}
	cfg, err := config.normalized()
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for dst, src := range mapping {
		if _, ok := target.field(dst); !ok {
			return nil, fmt.Errorf("mapping references unknown target field %q", dst)
		}
		if _, ok := source.field(src); !ok {
			return nil, fmt.Errorf("mapping references unknown source field %q", src)
		}
		m[dst] = src
	}
	return &Converter{source: source, target: target, mapping: m, config: cfg}, nil
}

// ConvertRow converts one row atomically: on any error no partial row is
// returned. The output contains exactly the target fields, in schema order
// semantics (a map keyed by target field name).
func (c *Converter) ConvertRow(row map[string]Value) (map[string]Value, *Error) {
	out := make(map[string]Value, len(c.target.Fields))
	for _, tf := range c.target.Fields {
		srcName := tf.Name
		if mapped, ok := c.mapping[tf.Name]; ok {
			srcName = mapped
		}
		sf, ok := c.source.field(srcName)
		if !ok {
			return nil, &Error{Field: tf.Name, Code: CodeUnknownSourceField,
				Msg: fmt.Sprintf("source field %q not in source schema", srcName)}
		}
		val, present := row[srcName]
		if !present {
			if tf.Default != nil {
				// Defaults are expressed in target terms: convert with the
				// target field acting as its own source spec.
				dv, err := c.convertValue(*tf.Default, tf, tf)
				if err != nil {
					err.Field = tf.Name
					err.Code = CodeBadDefault
					err.Msg = "default: " + err.Msg
					return nil, err
				}
				out[tf.Name] = dv
				continue
			}
			if tf.Required {
				return nil, &Error{Field: tf.Name, Code: CodeMissingRequired,
					Msg: fmt.Sprintf("source key %q absent and no default", srcName)}
			}
			out[tf.Name] = Null()
			continue
		}
		if val.Kind == KindNull {
			if tf.Required {
				return nil, &Error{Field: tf.Name, Code: CodeNullNotAllowed,
					Msg: "explicit null on required field"}
			}
			out[tf.Name] = Null()
			continue
		}
		cv, err := c.convertValue(val, sf, tf)
		if err != nil {
			err.Field = tf.Name
			return nil, err
		}
		out[tf.Name] = cv
	}
	return out, nil
}

func (c *Converter) convertValue(val Value, sf, tf Field) (Value, *Error) {
	if sf.Kind != tf.Kind {
		return Value{}, &Error{Code: CodeKindMismatch,
			Msg: fmt.Sprintf("source kind %q, target kind %q", sf.Kind, tf.Kind)}
	}
	if val.Kind != sf.Kind {
		return Value{}, &Error{Code: CodeTypeMismatch,
			Msg: fmt.Sprintf("value kind %q, schema kind %q", val.Kind, sf.Kind)}
	}
	switch tf.Kind {
	case KindInt:
		if err := checkRange(tf, val.Int); err != nil {
			return Value{}, err
		}
		return Int(val.Int), nil
	case KindDecimal:
		coef, err := RescaleDecimal(val.Dec.Coef, sf.scale(), tf.scale(), c.config.DecimalMode)
		if err != nil {
			return Value{}, err
		}
		if err := checkRange(tf, coef); err != nil {
			return Value{}, err
		}
		return Dec(coef, tf.scale()), nil
	case KindString:
		if !utf8.ValidString(val.Str) {
			return Value{}, &Error{Code: CodeInvalidUTF8, Msg: "string is not valid UTF-8"}
		}
		if err := checkMaxBytes(tf, int64(len(val.Str))); err != nil {
			return Value{}, err
		}
		return Str(val.Str), nil
	case KindBytes:
		if err := checkMaxBytes(tf, int64(len(val.Bytes))); err != nil {
			return Value{}, err
		}
		b := make([]byte, len(val.Bytes))
		copy(b, val.Bytes)
		return Bytes(b), nil
	case KindTimestamp:
		if !val.TsIsRaw {
			return Value{}, &Error{Code: CodeTypeMismatch,
				Msg: "timestamp input must be raw text with explicit offset"}
		}
		micros, err := ParseTimestampMicros(val.TsRaw, c.config.TimestampMode)
		if err != nil {
			return Value{}, err
		}
		if err := checkRange(tf, micros); err != nil {
			return Value{}, err
		}
		return TsMicros(micros), nil
	}
	return Value{}, &Error{Code: CodeKindMismatch, Msg: fmt.Sprintf("unhandled kind %q", tf.Kind)}
}

func checkRange(tf Field, v int64) *Error {
	if tf.Min != nil && v < *tf.Min {
		return &Error{Code: CodeOutOfRange, Msg: fmt.Sprintf("%d < min %d", v, *tf.Min)}
	}
	if tf.Max != nil && v > *tf.Max {
		return &Error{Code: CodeOutOfRange, Msg: fmt.Sprintf("%d > max %d", v, *tf.Max)}
	}
	return nil
}

func checkMaxBytes(tf Field, n int64) *Error {
	if tf.MaxBytes != nil && n > *tf.MaxBytes {
		return &Error{Code: CodeTooLong,
			Msg: fmt.Sprintf("%d bytes > max_bytes %d", n, *tf.MaxBytes)}
	}
	return nil
}

// Result is the per-row outcome of a batch. Row is nil when Err is set.
type Result struct {
	Index int              `json:"index"`
	OK    bool             `json:"ok"`
	Row   map[string]Value `json:"row,omitempty"`
	Err   *Error           `json:"error,omitempty"`
}

// ConvertBatch converts rows independently, preserving input order: results[i]
// always corresponds to rows[i], and one failing row never affects the rest.
func (c *Converter) ConvertBatch(rows []map[string]Value) []Result {
	results := make([]Result, len(rows))
	for i, row := range rows {
		results[i].Index = i
		out, err := c.ConvertRow(row)
		if err != nil {
			results[i].Err = err
			continue
		}
		results[i].OK = true
		results[i].Row = out
	}
	return results
}
