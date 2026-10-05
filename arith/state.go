package arith

import (
	"encoding/binary"
	"fmt"
)

// EncoderState is a complete, serializable snapshot of an Encoder. Resuming
// from it and continuing to encode produces output byte-identical to
// encoding the same sequence in one shot, because all coder state is exact
// integers.
type EncoderState struct {
	Low     uint32
	High    uint32
	Pending uint64
	Out     []byte // payload bytes emitted so far
	Cur     uint8  // partial output byte
	NBits   uint8  // valid bits in Cur
	Labels  []string
	Counts  [][2]uint16 // parallel to Labels
	Seq     []uint32
	Bits    []byte
}

// Snapshot captures the encoder state. The encoder must not be finished.
func (e *Encoder) Snapshot() (*EncoderState, error) {
	if e.done {
		return nil, fmt.Errorf("%w: encoder already finished", ErrState)
	}
	s := &EncoderState{
		Low:     e.rc.low,
		High:    e.rc.high,
		Pending: e.rc.pending,
		Cur:     e.rc.cur,
		NBits:   e.rc.nbits,
		Out:     append([]byte(nil), e.rc.out...),
		Labels:  append([]string(nil), e.labels...),
		Seq:     append([]uint32(nil), e.seq...),
		Bits:    append([]byte(nil), e.bits...),
	}
	s.Counts = make([][2]uint16, len(e.ctxs))
	for i, c := range e.ctxs {
		s.Counts[i] = [2]uint16{c.c0, c.c1}
	}
	return s, nil
}

// validate checks structural consistency of a (possibly deserialized) state.
func (s *EncoderState) validate() error {
	if len(s.Labels) != len(s.Counts) {
		return fmt.Errorf("%w: %d labels but %d count pairs", ErrState, len(s.Labels), len(s.Counts))
	}
	if len(s.Labels) > MaxContexts {
		return fmt.Errorf("%w: too many contexts", ErrLimit)
	}
	seen := make(map[string]struct{}, len(s.Labels))
	for i, l := range s.Labels {
		if !validLabel(l) {
			return fmt.Errorf("%w: bad label %q", ErrState, l)
		}
		if _, dup := seen[l]; dup {
			return fmt.Errorf("%w: duplicate label %q", ErrState, l)
		}
		seen[l] = struct{}{}
		c := s.Counts[i]
		if c[0] < 1 || c[1] < 1 || uint32(c[0])+uint32(c[1]) > maxTotal {
			return fmt.Errorf("%w: bad counts %v for %q", ErrState, c, l)
		}
	}
	if len(s.Seq) != len(s.Bits) {
		return fmt.Errorf("%w: %d indices but %d bits", ErrState, len(s.Seq), len(s.Bits))
	}
	if uint64(len(s.Bits)) > MaxBits {
		return fmt.Errorf("%w: bit count", ErrLimit)
	}
	for i, v := range s.Seq {
		if int(v) >= len(s.Labels) {
			return fmt.Errorf("%w: context index %d at %d", ErrState, v, i)
		}
	}
	for i, b := range s.Bits {
		if b > 1 {
			return fmt.Errorf("%w: bit %d is %d", ErrState, i, b)
		}
	}
	if s.NBits > 7 {
		return fmt.Errorf("%w: nbits %d", ErrState, s.NBits)
	}
	return nil
}

// Resume rebuilds an encoder from a snapshot.
func Resume(s *EncoderState) (*Encoder, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	e := NewEncoder()
	e.rc.low, e.rc.high, e.rc.pending = s.Low, s.High, s.Pending
	e.rc.out = append([]byte(nil), s.Out...)
	e.rc.cur, e.rc.nbits = s.Cur, s.NBits
	e.labels = append([]string(nil), s.Labels...)
	e.seq = append([]uint32(nil), s.Seq...)
	e.bits = append([]byte(nil), s.Bits...)
	e.ctxs = make([]context, len(s.Counts))
	for i, c := range s.Counts {
		e.ctxs[i] = context{c[0], c[1]}
		e.index[s.Labels[i]] = uint32(i)
	}
	return e, nil
}

// MarshalBinary serializes the state deterministically.
func (s *EncoderState) MarshalBinary() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	var buf []byte
	buf = append(buf, stateMagic...)
	buf = binary.AppendUvarint(buf, uint64(s.Low))
	buf = binary.AppendUvarint(buf, uint64(s.High))
	buf = binary.AppendUvarint(buf, s.Pending)
	buf = binary.AppendUvarint(buf, uint64(len(s.Out)))
	buf = append(buf, s.Out...)
	buf = append(buf, s.Cur, s.NBits)
	buf = binary.AppendUvarint(buf, uint64(len(s.Labels)))
	for i, l := range s.Labels {
		buf = binary.AppendUvarint(buf, uint64(len(l)))
		buf = append(buf, l...)
		buf = binary.AppendUvarint(buf, uint64(s.Counts[i][0]))
		buf = binary.AppendUvarint(buf, uint64(s.Counts[i][1]))
	}
	buf = binary.AppendUvarint(buf, uint64(len(s.Seq)))
	for _, v := range s.Seq {
		buf = binary.AppendUvarint(buf, uint64(v))
	}
	packed := packBits(s.Bits)
	buf = binary.AppendUvarint(buf, uint64(len(packed)))
	buf = append(buf, packed...)
	return buf, nil
}

// UnmarshalEncoderState parses a serialized snapshot.
func UnmarshalEncoderState(data []byte) (*EncoderState, error) {
	c := &cursor{data: data}
	m, err := c.bytes(4)
	if err != nil {
		return nil, err
	}
	if string(m) != string(stateMagic) {
		return nil, fmt.Errorf("%w: bad state magic", ErrState)
	}
	s := &EncoderState{}
	read := func(v *uint64) error {
		x, err := c.uvarint()
		*v = x
		return err
	}
	var v uint64
	if err := read(&v); err != nil {
		return nil, err
	}
	s.Low = uint32(v)
	if err := read(&v); err != nil {
		return nil, err
	}
	s.High = uint32(v)
	if err := read(&v); err != nil {
		return nil, err
	}
	s.Pending = v
	if err := read(&v); err != nil {
		return nil, err
	}
	if s.Out, err = c.bytes(v); err != nil {
		return nil, err
	}
	b, err := c.bytes(2)
	if err != nil {
		return nil, err
	}
	s.Cur, s.NBits = b[0], b[1]
	if err := read(&v); err != nil {
		return nil, err
	}
	k := v
	if k > MaxContexts {
		return nil, fmt.Errorf("%w: too many contexts", ErrLimit)
	}
	s.Labels = make([]string, 0, k)
	s.Counts = make([][2]uint16, 0, k)
	for i := uint64(0); i < k; i++ {
		if err := read(&v); err != nil {
			return nil, err
		}
		lb, err := c.bytes(v)
		if err != nil {
			return nil, err
		}
		var c0, c1 uint64
		if err := read(&c0); err != nil {
			return nil, err
		}
		if err := read(&c1); err != nil {
			return nil, err
		}
		s.Labels = append(s.Labels, string(lb))
		s.Counts = append(s.Counts, [2]uint16{uint16(c0), uint16(c1)})
	}
	if err := read(&v); err != nil {
		return nil, err
	}
	n := v
	if n > MaxBits {
		return nil, fmt.Errorf("%w: bit count", ErrLimit)
	}
	s.Seq = make([]uint32, 0, n)
	for i := uint64(0); i < n; i++ {
		if err := read(&v); err != nil {
			return nil, err
		}
		s.Seq = append(s.Seq, uint32(v))
	}
	if err := read(&v); err != nil {
		return nil, err
	}
	packed, err := c.bytes(v)
	if err != nil {
		return nil, err
	}
	if uint64(len(packed)) != (n+7)/8 {
		return nil, fmt.Errorf("%w: packed bits length %d for %d bits", ErrState, len(packed), n)
	}
	s.Bits = make([]byte, n)
	for i := range s.Bits {
		s.Bits[i] = packed[i>>3] >> (7 - uint(i)&7) & 1
	}
	if c.pos != len(c.data) {
		return nil, fmt.Errorf("%w: %d extra bytes", ErrTrailing, len(c.data)-c.pos)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}
