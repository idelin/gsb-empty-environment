package rangecoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Stream format: a small fixed header followed by the range-coded bytes.
//
//	magic   4 bytes  "RCST"
//	version uint16   = StreamVersion
//	flags   uint16   = 0 (reserved)
//	payload ...      range-coded segments, each flushed at checkpoint bounds
//
// Checkpoint offsets are absolute byte offsets into this stream (header
// included).
const (
	StreamMagic     = "RCST"
	StreamVersion   = 1
	StreamHeaderLen = 8
)

// Encoder incrementally builds a code stream. At each Checkpoint call the
// range coder is flushed (byte-aligned) and reset, and the model state is
// snapshotted, so every checkpoint is a valid random-access entry point for
// both decoding and resumed encoding.
type Encoder struct {
	buf  bytes.Buffer
	re   *rangeEncoder
	m    *Model
	syms uint64
	cps  []Checkpoint
	done bool
}

// NewEncoder starts a fresh stream with a fresh model.
func NewEncoder() *Encoder {
	e := &Encoder{m: NewModel()}
	e.buf.WriteString(StreamMagic)
	var hdr [4]byte
	binary.BigEndian.PutUint16(hdr[0:2], StreamVersion)
	// hdr[2:4] flags, zero
	e.buf.Write(hdr[:])
	e.re = newRangeEncoder(&e.buf)
	return e
}

// Encode compresses p into the stream.
func (e *Encoder) Encode(p []byte) {
	for _, b := range p {
		s := int(b)
		e.re.encode(e.m.cumFreq(s), e.m.freq[s], e.m.total)
		e.m.update(s)
		e.syms++
	}
}

// Checkpoint flushes and resets the range coder and snapshots the model.
// The returned checkpoint can be used to decode the following segment
// independently, or to resume encoding later with a bit-identical result.
func (e *Encoder) Checkpoint() Checkpoint {
	e.re.flush()
	e.re = newRangeEncoder(&e.buf)
	cp := Checkpoint{
		Offset:      uint64(e.buf.Len()),
		SymbolIndex: e.syms,
		Model:       e.m.Clone(),
	}
	e.cps = append(e.cps, cp)
	return cp
}

// Checkpoints returns the checkpoints recorded so far.
func (e *Encoder) Checkpoints() []Checkpoint {
	return append([]Checkpoint(nil), e.cps...)
}

// Symbols returns the number of symbols encoded so far.
func (e *Encoder) Symbols() uint64 {
	return e.syms
}

// Finish encodes the end-of-stream marker, flushes, and returns the complete
// code stream. The encoder must not be used afterwards.
func (e *Encoder) Finish() []byte {
	if !e.done {
		e.re.encode(e.m.cumFreq(EOFSymbol), e.m.freq[EOFSymbol], e.m.total)
		e.re.flush()
		e.done = true
	}
	return e.buf.Bytes()
}

// ResumeEncoder continues an interrupted encoding run from a checkpoint: the
// stream is truncated to the checkpoint offset and the model restored from
// the checkpoint. Provided the same data and the same checkpoint policy are
// applied, the resulting stream is byte-identical to an uninterrupted run.
func ResumeEncoder(stream []byte, cp Checkpoint) (*Encoder, error) {
	if err := ValidateStreamHeader(stream); err != nil {
		return nil, err
	}
	if cp.Model == nil {
		return nil, fmt.Errorf("%w: nil model", ErrModelCorrupt)
	}
	if cp.Offset < StreamHeaderLen || cp.Offset > uint64(len(stream)) {
		return nil, fmt.Errorf("%w: offset %d, stream length %d", ErrBadOffset, cp.Offset, len(stream))
	}
	e := &Encoder{m: cp.Model.Clone(), syms: cp.SymbolIndex}
	e.buf.Write(stream[:cp.Offset])
	e.re = newRangeEncoder(&e.buf)
	return e, nil
}

// ValidateStreamHeader checks the stream magic and version.
func ValidateStreamHeader(b []byte) error {
	if len(b) < StreamHeaderLen {
		return ErrBadLength
	}
	if string(b[:4]) != StreamMagic {
		return ErrBadMagic
	}
	if v := binary.BigEndian.Uint16(b[4:6]); v != StreamVersion {
		return fmt.Errorf("%w: stream version %d, want %d", ErrUnsupportedVersion, v, StreamVersion)
	}
	return nil
}
