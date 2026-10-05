package varstream

import "fmt"

// AppendUvarint appends the canonical (shortest) LEB128 encoding of v.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendRecord encodes one record (one value per schema field) onto dst
// and returns the extended slice. Values are range-checked against their
// field bit widths; out-of-range values fail with ErrOverflow.
func (s *Schema) AppendRecord(dst []byte, vals ...Value) ([]byte, error) {
	if len(vals) != len(s.Fields) {
		return nil, fmt.Errorf("varstream: got %d values, schema has %d fields", len(vals), len(s.Fields))
	}
	for i, f := range s.Fields {
		v := vals[i]
		if f.Signed {
			if !fitsInt(v.I, f.Bits) {
				return nil, fmt.Errorf("varstream: field %q: %v", f.Name, ErrOverflow)
			}
			dst = AppendUvarint(dst, zigzag(v.I))
		} else {
			if !fitsUint(v.U, f.Bits) {
				return nil, fmt.Errorf("varstream: field %q: %v", f.Name, ErrOverflow)
			}
			dst = AppendUvarint(dst, v.U)
		}
	}
	return dst, nil
}

// Encode encodes one record into a fresh byte slice.
func (s *Schema) Encode(vals ...Value) ([]byte, error) {
	return s.AppendRecord(nil, vals...)
}
