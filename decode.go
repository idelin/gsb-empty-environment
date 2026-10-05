package varstream

import "fmt"

// decodeUvarint decodes one canonical varint from the head of p.
// It returns ErrNeedMore if p ends mid-varint, ErrNonMinimal for
// redundant trailing zero groups, and ErrVarintOverflow past 64 bits.
func decodeUvarint(p []byte) (v uint64, n int, err error) {
	for i := 0; i < len(p); i++ {
		b := p[i]
		payload := uint64(b & 0x7f)
		shift := uint(7 * i)
		if shift >= 64 || (shift == 63 && payload > 1) {
			return 0, 0, ErrVarintOverflow
		}
		v |= payload << shift
		if b&0x80 == 0 {
			if i > 0 && payload == 0 {
				return 0, 0, ErrNonMinimal
			}
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrNeedMore
}

// finishValue range-checks a raw varint against its field and converts
// it to a Value.
func finishValue(f Field, u uint64) (Value, error) {
	if f.Signed {
		i := unzigzag(u)
		if !fitsInt(i, f.Bits) {
			return Value{}, fmt.Errorf("varstream: field %q: %v", f.Name, ErrOverflow)
		}
		return Int(i), nil
	}
	if !fitsUint(u, f.Bits) {
		return Value{}, fmt.Errorf("varstream: field %q: %v", f.Name, ErrOverflow)
	}
	return Uint(u), nil
}

// DecodeRecord decodes exactly one record from the head of p and returns
// it with the number of bytes consumed. The decode is transactional: on
// any error (including ErrNeedMore for a truncated tail) no record and
// no consumed bytes are returned.
func (s *Schema) DecodeRecord(p []byte) (Record, int, error) {
	if err := s.Validate(); err != nil {
		return nil, 0, err
	}
	rec := make(Record, 0, len(s.Fields))
	off := 0
	for _, f := range s.Fields {
		u, n, err := decodeUvarint(p[off:])
		if err != nil {
			return nil, 0, err
		}
		off += n
		v, err := finishValue(f, u)
		if err != nil {
			return nil, 0, err
		}
		rec = append(rec, v)
	}
	return rec, off, nil
}

// DecodeAll decodes as many records as possible from p. Records are
// committed only when fully decoded: if a record fails, DecodeAll
// returns the records completed before it, the offset of the failing
// record, and the error. The failing record is never committed.
func (s *Schema) DecodeAll(p []byte) ([]Record, int, error) {
	var recs []Record
	off := 0
	for off < len(p) {
		rec, n, err := s.DecodeRecord(p[off:])
		if err != nil {
			return recs, off, err
		}
		recs = append(recs, rec)
		off += n
	}
	return recs, off, nil
}

// State is a serializable snapshot of a streaming Decoder, bound to the
// schema version it was taken from.
type State struct {
	Version  uint32
	FieldIdx int
	Acc      uint64
	Shift    uint
	NBytes   int
	Cur      []Value
}

// Decoder consumes a byte stream incrementally, emitting one Record per
// schema record. Partial progress survives between Feed calls, so a
// stream paused anywhere — even mid-varint — resumes correctly.
type Decoder struct {
	schema *Schema

	// varint in progress
	acc    uint64
	shift  uint
	nbytes int

	// record in progress
	fieldIdx int
	cur      []Value

	done []Record
}

// NewDecoder builds a streaming decoder for s.
func NewDecoder(s *Schema) (*Decoder, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &Decoder{schema: s}, nil
}

// Resume rebuilds a decoder from a paused state. The state's schema
// version must match s.Version.
func (s *Schema) Resume(st State) (*Decoder, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if st.Version != s.Version {
		return nil, ErrSchemaVersion
	}
	if st.FieldIdx < 0 || st.FieldIdx >= len(s.Fields) || len(st.Cur) != st.FieldIdx {
		return nil, fmt.Errorf("varstream: pause state inconsistent with schema")
	}
	cur := make([]Value, len(st.Cur))
	copy(cur, st.Cur)
	return &Decoder{
		schema:   s,
		acc:      st.Acc,
		shift:    st.Shift,
		nbytes:   st.NBytes,
		fieldIdx: st.FieldIdx,
		cur:      cur,
	}, nil
}

// Pause snapshots the decoder. Feeding may continue afterwards.
func (d *Decoder) Pause() State {
	cur := make([]Value, len(d.cur))
	copy(cur, d.cur)
	return State{
		Version:  d.schema.Version,
		FieldIdx: d.fieldIdx,
		Acc:      d.acc,
		Shift:    d.shift,
		NBytes:   d.nbytes,
		Cur:      cur,
	}
}

// Feed consumes p byte by byte. It returns the number of bytes consumed;
// on a decode error the partial record is discarded (never committed)
// and the decoder resyncs to the next record boundary. Bytes after the
// error are not consumed.
func (d *Decoder) Feed(p []byte) (int, error) {
	for i, b := range p {
		if err := d.step(b); err != nil {
			d.resetRecord()
			return i, err
		}
	}
	return len(p), nil
}

// Records drains the records completed so far.
func (d *Decoder) Records() []Record {
	recs := d.done
	d.done = nil
	return recs
}

// Pending reports whether a partial record is buffered, i.e. the stream
// currently ends mid-record and needs more input.
func (d *Decoder) Pending() bool {
	return d.fieldIdx != 0 || d.nbytes != 0
}

func (d *Decoder) resetRecord() {
	d.acc, d.shift, d.nbytes = 0, 0, 0
	d.fieldIdx = 0
	d.cur = d.cur[:0]
}

func (d *Decoder) step(b byte) error {
	payload := uint64(b & 0x7f)
	if d.shift >= 64 || (d.shift == 63 && payload > 1) {
		return ErrVarintOverflow
	}
	d.acc |= payload << d.shift
	d.nbytes++
	if b&0x80 != 0 {
		d.shift += 7
		return nil
	}
	if d.nbytes > 1 && payload == 0 {
		return ErrNonMinimal
	}
	f := d.schema.Fields[d.fieldIdx]
	v, err := finishValue(f, d.acc)
	if err != nil {
		return err
	}
	d.acc, d.shift, d.nbytes = 0, 0, 0
	d.cur = append(d.cur, v)
	d.fieldIdx++
	if d.fieldIdx == len(d.schema.Fields) {
		rec := make(Record, len(d.cur))
		copy(rec, d.cur)
		d.done = append(d.done, rec)
		d.fieldIdx = 0
		d.cur = d.cur[:0]
	}
	return nil
}
