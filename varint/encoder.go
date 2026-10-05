package varint

// AppendRecord encodes one record according to the schema and appends
// it to dst. Each value must fit its field's width or ErrOverflow is
// returned (wrapped in a FieldError). The output is the unique
// canonical byte string for the record.
func AppendRecord(dst []byte, s Schema, vals ...Value) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if len(vals) != len(s.Fields) {
		return nil, &FieldError{Field: "", Err: errValueCount}
	}
	var err error
	for i, f := range s.Fields {
		if f.Signed {
			dst, err = AppendSvarint(dst, vals[i].S, f.Width)
		} else {
			dst, err = AppendUvarint(dst, vals[i].U, f.Width)
		}
		if err != nil {
			return nil, &FieldError{Field: f.Name, Err: err}
		}
	}
	return dst, nil
}
