package rangecoder

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// NumSymbols is the alphabet size: byte values 0..255 plus EOFSymbol.
	NumSymbols = 257
	// EOFSymbol marks the end of a finished stream.
	EOFSymbol = 256

	// maxTotal bounds the cumulative frequency; the model is rescaled
	// before the total can reach it. Must stay well below rcBot so that
	// rng/tot never drops to 0.
	maxTotal = 1 << 15
	// increment is the frequency bump per observed symbol.
	increment = 4
)

// Model is an adaptive order-0 frequency model over NumSymbols symbols.
// Every symbol always has frequency >= 1, so any symbol can be coded at
// any time; frequencies are halved when the total grows too large.
type Model struct {
	freq  [NumSymbols]uint32
	total uint32
}

// NewModel returns a fresh model with all frequencies set to 1.
func NewModel() *Model {
	m := &Model{}
	for i := range m.freq {
		m.freq[i] = 1
	}
	m.total = NumSymbols
	return m
}

// Clone returns a deep copy of the model.
func (m *Model) Clone() *Model {
	c := *m
	return &c
}

// Equal reports whether both models are in exactly the same state.
func (m *Model) Equal(o *Model) bool {
	if m == nil || o == nil {
		return m == o
	}
	return m.total == o.total && m.freq == o.freq
}

// cumFreq returns the cumulative frequency of all symbols below s.
func (m *Model) cumFreq(s int) uint32 {
	var c uint32
	for i := 0; i < s; i++ {
		c += m.freq[i]
	}
	return c
}

// symbolFor finds the symbol whose cumulative interval contains count.
// Returns -1 if count is out of range (corrupt stream).
func (m *Model) symbolFor(count uint32) (sym int, cum uint32) {
	var c uint32
	for i := 0; i < NumSymbols; i++ {
		if count < c+m.freq[i] {
			return i, c
		}
		c += m.freq[i]
	}
	return -1, c
}

// update adapts the model after coding symbol s.
func (m *Model) update(s int) {
	m.freq[s] += increment
	m.total += increment
	if m.total >= maxTotal {
		var t uint32
		for i := range m.freq {
			// (f+1)>>1 keeps non-zero frequencies >= 1 and zero at 0.
			m.freq[i] = (m.freq[i] + 1) >> 1
			t += m.freq[i]
		}
		m.total = t
	}
}

// Model serialization format (all integers big-endian):
//
//	magic   4 bytes  "RMDL"
//	version uint16   = ModelVersion
//	nsym    uint16   = NumSymbols
//	total   uint32   sum of all frequencies
//	freq    uint32 x nsym
//
// Deserialization verifies magic, version, exact length, the total against
// the sum of frequencies, and per-frequency bounds.
const (
	ModelMagic   = "RMDL"
	ModelVersion = 1
	modelLen     = 4 + 2 + 2 + 4 + 4*NumSymbols
)

var ErrModelCorrupt = errors.New("rangecoder: model state failed verification")

// Marshal serializes the model to its canonical binary form.
func (m *Model) Marshal() []byte {
	out := make([]byte, 0, modelLen)
	out = append(out, ModelMagic...)
	out = binary.BigEndian.AppendUint16(out, ModelVersion)
	out = binary.BigEndian.AppendUint16(out, NumSymbols)
	out = binary.BigEndian.AppendUint32(out, m.total)
	for _, f := range m.freq {
		out = binary.BigEndian.AppendUint32(out, f)
	}
	return out
}

// UnmarshalModel parses and verifies a serialized model.
func UnmarshalModel(b []byte) (*Model, error) {
	if len(b) != modelLen {
		return nil, fmt.Errorf("%w: bad length %d, want %d", ErrModelCorrupt, len(b), modelLen)
	}
	if string(b[:4]) != ModelMagic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrModelCorrupt, b[:4])
	}
	if v := binary.BigEndian.Uint16(b[4:6]); v != ModelVersion {
		return nil, fmt.Errorf("%w: model version %d, want %d", ErrUnsupportedVersion, v, ModelVersion)
	}
	if n := binary.BigEndian.Uint16(b[6:8]); n != NumSymbols {
		return nil, fmt.Errorf("%w: symbol count %d, want %d", ErrModelCorrupt, n, NumSymbols)
	}
	m := &Model{total: binary.BigEndian.Uint32(b[8:12])}
	var sum uint32
	for i := 0; i < NumSymbols; i++ {
		f := binary.BigEndian.Uint32(b[12+4*i:])
		if f < 1 || f >= maxTotal {
			return nil, fmt.Errorf("%w: frequency[%d]=%d out of bounds", ErrModelCorrupt, i, f)
		}
		m.freq[i] = f
		sum += f
	}
	if sum != m.total || m.total >= maxTotal {
		return nil, fmt.Errorf("%w: total %d does not match sum %d", ErrModelCorrupt, m.total, sum)
	}
	return m, nil
}
