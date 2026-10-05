package varint

// Decoder is a streaming record decoder bound to one schema version.
// Bytes are supplied with Feed; complete records are pulled with
// DecodeOne or DecodeBatch. A stream that ends in the middle of a
// varint yields ErrNeedMore, never a zero value.
type Decoder struct {
	schema Schema
	buf    []byte // undecoded bytes
}

// NewDecoder validates the schema and returns a streaming decoder.
func NewDecoder(s Schema) (*Decoder, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &Decoder{schema: s}, nil
}

// Feed appends bytes to the decoder's input buffer.
func (d *Decoder) Feed(p []byte) {
	d.buf = append(d.buf, p...)
}

// Pending reports the number of undecoded buffered bytes.
func (d *Decoder) Pending() int {
	return len(d.buf)
}

// Schema returns the schema the decoder is bound to.
func (d *Decoder) Schema() Schema {
	return d.schema
}

// DecodeOne decodes and consumes one record. If the buffered bytes
// end mid-record it returns ErrNeedMore and consumes nothing; on any
// other error it also consumes nothing.
func (d *Decoder) DecodeOne() (Record, error) {
	rec, n, err := decodeRecord(d.buf, d.schema)
	if err != nil {
		return nil, err
	}
	d.buf = d.buf[n:]
	return rec, nil
}

// DecodeBatch decodes up to max records. Decoding stops cleanly at
// end of buffered input (remaining partial record stays buffered).
// If a record fails with a fatal error (ErrOverflow, ErrNonCanonical),
// that record is not committed: the buffer is rolled back to its
// start, the records decoded so far are returned along with the error.
func (d *Decoder) DecodeBatch(max int) ([]Record, error) {
	var out []Record
	for len(out) < max {
		rec, n, err := decodeRecord(d.buf, d.schema)
		if err == ErrNeedMore {
			return out, nil
		}
		if err != nil {
			// Do not commit the failed record.
			return out, err
		}
		d.buf = d.buf[n:]
		out = append(out, rec)
	}
	return out, nil
}

// State is a paused decoder position. It is bound to the schema
// version that produced it.
type State struct {
	Version uint32
	Pending []byte
}

// Snapshot captures the decoder's pause state, including the schema
// version and any undecoded bytes.
func (d *Decoder) Snapshot() State {
	p := make([]byte, len(d.buf))
	copy(p, d.buf)
	return State{Version: d.schema.Version, Pending: p}
}

// Resume restores a decoder from a pause state. The state's schema
// version must equal the given schema's version or ErrSchemaVersion
// is returned.
func Resume(s Schema, st State) (*Decoder, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if st.Version != s.Version {
		return nil, ErrSchemaVersion
	}
	buf := make([]byte, len(st.Pending))
	copy(buf, st.Pending)
	return &Decoder{schema: s, buf: buf}, nil
}

// decodeRecord decodes one record from buf, returning the record and
// bytes consumed. buf is not modified; on error nothing is consumed.
func decodeRecord(buf []byte, s Schema) (Record, int, error) {
	rec := make(Record, len(s.Fields))
	off := 0
	for i, f := range s.Fields {
		if f.Signed {
			v, n, err := decodeSvarint(buf[off:], f.Width)
			if err != nil {
				return nil, 0, err
			}
			rec[i].S = v
			off += n
		} else {
			v, n, err := decodeUvarint(buf[off:], f.Width)
			if err != nil {
				return nil, 0, err
			}
			rec[i].U = v
			off += n
		}
	}
	return rec, off, nil
}
