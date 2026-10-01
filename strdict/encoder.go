package strdict

import (
	"bytes"
	"encoding/binary"
)

// Encoder writes a record stream. Strings found in the current dictionary are
// emitted as REF records; everything else is emitted INLINE. Switch changes
// the dictionary version for all subsequent records and emits an explicit
// switch point so decoders never reinterpret earlier references.
type Encoder struct {
	buf  bytes.Buffer
	tmp  [binary.MaxVarintLen64]byte
	dict *Dictionary
	hash HashFunc
}

// NewEncoder starts a stream pinned to dict's version (may be nil to start
// fully inline). hash must match the hash used to build dict.
func NewEncoder(dict *Dictionary, hash HashFunc) *Encoder {
	if hash == nil {
		hash = DefaultHash
	}
	return &Encoder{dict: dict, hash: hash}
}

// Switch emits a switch point and re-pins the encoder to d's version.
// References written after this call use the new table; previously written
// references are unaffected on the decode side.
func (e *Encoder) Switch(d *Dictionary) {
	e.dict = d
	e.buf.WriteByte(opSwitch)
	n := binary.PutUvarint(e.tmp[:], d.version)
	e.buf.Write(e.tmp[:n])
}

// WriteString appends one string record.
func (e *Encoder) WriteString(s string) {
	if e.dict != nil {
		if idx, ok := e.dict.IndexOf(e.hash, s); ok {
			e.buf.WriteByte(opRef)
			n := binary.PutUvarint(e.tmp[:], uint64(idx))
			e.buf.Write(e.tmp[:n])
			return
		}
	}
	e.buf.WriteByte(opInline)
	n := binary.PutUvarint(e.tmp[:], uint64(len(s)))
	e.buf.Write(e.tmp[:n])
	e.buf.WriteString(s)
}

// WriteEnd appends the end-of-stream marker.
func (e *Encoder) WriteEnd() { e.buf.WriteByte(opEnd) }

// Bytes returns the encoded stream so far.
func (e *Encoder) Bytes() []byte { return e.buf.Bytes() }
