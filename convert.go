package rowconv

import (
	"fmt"
	"math"
	"unicode/utf8"
)

// Converter converts rows from a source schema to a target schema
// according to a field mapping (source name -> target name).
type Converter struct {
	targets []targetBinding
	opts    Options
}

type targetBinding struct {
	field    Field
	srcField *Field // nil when the target field is unmapped
}

// NewConverter validates both schemas and the mapping, and returns a
// reusable Converter.
func NewConverter(src, tgt *Schema, mapping map[string]string, opts Options) (*Converter, error) {
	if err := src.Validate(); err != nil {
		return nil, fmt.Errorf("rowconv: source schema: %v", err)
	}
	if err := tgt.Validate(); err != nil {
		return nil, fmt.Errorf("rowconv: target schema: %v", err)
	}
	switch opts.Rounding {
	case "", Reject, TiesToEven:
	default:
		return nil, fmt.Errorf("rowconv: unknown rounding mode %q", opts.Rounding)
	}
	srcByName := src.byName()
	tgtByName := tgt.byName()
	rev := make(map[string]string, len(mapping)) // target name -> source name
	for s, t := range mapping {
		if _, ok := srcByName[s]; !ok {
			return nil, fmt.Errorf("rowconv: mapping: unknown source field %q", s)
		}
		if _, ok := tgtByName[t]; !ok {
			return nil, fmt.Errorf("rowconv: mapping: unknown target field %q", t)
		}
		if _, dup := rev[t]; dup {
			return nil, fmt.Errorf("rowconv: mapping: target field %q mapped twice", t)
		}
		rev[t] = s
	}
	c := &Converter{opts: opts}
	for _, tf := range tgt.Fields {
		b := targetBinding{field: tf}
		if s, ok := rev[tf.Name]; ok {
			sf := srcByName[s]
			if sf.Type != tf.Type {
				return nil, fmt.Errorf("rowconv: mapping %q -> %q: cannot convert %s to %s",
					s, tf.Name, sf.Type, tf.Type)
			}
			b.srcField = sf
		}
		c.targets = append(c.targets, b)
	}
	return c, nil
}

// ConvertRow converts one row. The conversion is atomic: on any failure
// the returned Row is nil and no partially converted row is produced.
func (c *Converter) ConvertRow(row Row) (Row, error) {
	out := make(Row, len(c.targets))
	for _, b := range c.targets {
		tf := b.field
		var sv *Value
		present := false
		if b.srcField != nil {
			sv, present = row[b.srcField.Name]
		}
		if !present {
			switch {
			case tf.Default != nil:
				out[tf.Name] = tf.Default
			case tf.Required:
				return nil, fieldErr(tf.Name, "required field missing")
			case tf.Nullable:
				out[tf.Name] = nil
			default:
				// Optional field without default: omitted from output.
			}
			continue
		}
		if sv == nil {
			switch {
			case tf.Default != nil:
				out[tf.Name] = tf.Default
			case tf.Nullable:
				out[tf.Name] = nil
			default:
				return nil, fieldErr(tf.Name, "null not allowed")
			}
			continue
		}
		tv, err := convertValue(*b.srcField, tf, sv, c.opts.rounding())
		if err != nil {
			return nil, err
		}
		out[tf.Name] = tv
	}
	return out, nil
}

// Result is the outcome of converting one row in a batch.
type Result struct {
	Index int
	Row   Row // nil when Err != nil
	Err   error
}

// ConvertBatch converts rows independently, preserving input order: the
// i-th result always corresponds to the i-th input row.
func (c *Converter) ConvertBatch(rows []Row) []Result {
	results := make([]Result, len(rows))
	for i, row := range rows {
		out, err := c.ConvertRow(row)
		results[i] = Result{Index: i, Row: out, Err: err}
	}
	return results
}

// convertValue converts a single non-null value to the target field type.
func convertValue(sf, tf Field, sv *Value, mode RoundingMode) (*Value, error) {
	switch tf.Type {
	case TypeInt:
		tbits := tf.Bits
		if tbits == 0 {
			tbits = 64
		}
		min, max := intRange(tbits)
		if sv.Int < min || sv.Int > max {
			return nil, fieldErr(tf.Name, "integer %d overflows int%d range [%d, %d]",
				sv.Int, tbits, min, max)
		}
		v := *sv
		return &v, nil
	case TypeDecimal:
		coeff, err := RescaleDecimal(sv.Dec.Coefficient, sv.Dec.Scale, tf.Scale, mode)
		if err != nil {
			return nil, fieldErr(tf.Name, "%v", err)
		}
		if tf.Precision > 0 && decimalDigits(coeff) > tf.Precision {
			return nil, fieldErr(tf.Name, "decimal %s exceeds precision %d at scale %d",
				coeff.String(), tf.Precision, tf.Scale)
		}
		return &Value{Kind: TypeDecimal, Dec: &Decimal{Coefficient: coeff, Scale: tf.Scale}}, nil
	case TypeString:
		if !utf8.ValidString(sv.Str) {
			return nil, fieldErr(tf.Name, "invalid UTF-8 string")
		}
		if tf.MaxBytes > 0 && len(sv.Str) > tf.MaxBytes {
			return nil, fieldErr(tf.Name, "string of %d bytes exceeds maxBytes %d",
				len(sv.Str), tf.MaxBytes)
		}
		v := *sv
		return &v, nil
	case TypeBytes:
		if tf.MaxBytes > 0 && len(sv.Bytes) > tf.MaxBytes {
			return nil, fieldErr(tf.Name, "blob of %d bytes exceeds maxBytes %d",
				len(sv.Bytes), tf.MaxBytes)
		}
		v := *sv
		return &v, nil
	case TypeTimestamp:
		v := *sv
		return &v, nil
	}
	return nil, fieldErr(tf.Name, "unsupported target type %q", tf.Type)
}

// intRange returns the closed [min, max] range of a signed integer of the
// given bit width.
func intRange(bits int) (int64, int64) {
	if bits >= 64 {
		return math.MinInt64, math.MaxInt64
	}
	max := int64(1)<<uint(bits-1) - 1
	return -max - 1, max
}
