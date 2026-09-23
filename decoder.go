package rangecoder

import "fmt"

// Decoder decodes a stream starting at a checkpoint. The decoder state is
// built solely from the checkpoint's model snapshot and the stream bytes at
// the checkpoint offset, so segments are decoded independently of each
// other.
type Decoder struct {
	rd *rangeDecoder
	m  *Model
}

// NewDecoder positions a decoder at the given checkpoint. The checkpoint is
// trusted to belong to this stream; use ParseCheckpoint to verify a
// serialized checkpoint before use.
func NewDecoder(stream []byte, cp Checkpoint) (*Decoder, error) {
	if err := ValidateStreamHeader(stream); err != nil {
		return nil, err
	}
	if cp.Model == nil {
		return nil, fmt.Errorf("%w: nil model", ErrModelCorrupt)
	}
	if cp.Offset < StreamHeaderLen || cp.Offset > uint64(len(stream)) {
		return nil, fmt.Errorf("%w: offset %d, stream length %d", ErrBadOffset, cp.Offset, len(stream))
	}
	return &Decoder{
		rd: newRangeDecoder(stream, int(cp.Offset)),
		m:  cp.Model.Clone(),
	}, nil
}

// Decode decodes at most maxSymbols symbols, stopping early at the
// end-of-stream marker. The output quota is hard-bounded: the returned slice
// never holds more than maxSymbols bytes, regardless of stream content.
func (d *Decoder) Decode(maxSymbols uint64) ([]byte, error) {
	capHint := maxSymbols
	if capHint > 1<<20 {
		capHint = 1 << 20
	}
	out := make([]byte, 0, capHint)
	for i := uint64(0); i < maxSymbols; i++ {
		c := d.rd.freq(d.m.total)
		sym, cum := d.m.symbolFor(c)
		if sym < 0 {
			return out, ErrCorruptStream
		}
		d.rd.decode(cum, d.m.freq[sym], d.m.total)
		if sym == EOFSymbol {
			return out, nil
		}
		out = append(out, byte(sym))
		d.m.update(sym)
	}
	return out, nil
}

// DecodeAll decodes a complete stream from the beginning, bounded by
// maxSymbols. The initial state (fresh model at the first payload byte) is
// the implicit zeroth checkpoint.
//
// DecodeAll is only valid for streams encoded without intermediate
// checkpoints; for checkpointed streams use DecodeSegments.
func DecodeAll(stream []byte, maxSymbols uint64) ([]byte, error) {
	d, err := NewDecoder(stream, Checkpoint{Offset: StreamHeaderLen, Model: NewModel()})
	if err != nil {
		return nil, err
	}
	return d.Decode(maxSymbols)
}

// DecodeSegments decodes a complete checkpointed stream: every segment is
// decoded independently, starting from the checkpoint that precedes it, and
// the results are concatenated. The total output is hard-bounded by
// maxSymbols. Checkpoints must be the ones recorded by the Encoder, in
// order.
func DecodeSegments(stream []byte, cps []Checkpoint, maxSymbols uint64) ([]byte, error) {
	if err := ValidateStreamHeader(stream); err != nil {
		return nil, err
	}
	var out []byte
	remaining := maxSymbols
	decode := func(cp Checkpoint, n uint64) error {
		d, err := NewDecoder(stream, cp)
		if err != nil {
			return err
		}
		got, err := d.Decode(n)
		out = append(out, got...)
		remaining -= uint64(len(got))
		return err
	}
	prev := Checkpoint{Offset: StreamHeaderLen, Model: NewModel()}
	for i, cp := range cps {
		if cp.SymbolIndex < prev.SymbolIndex {
			return out, fmt.Errorf("%w: checkpoint %d symbol index goes backwards",
				ErrCorruptStream, i)
		}
		n := cp.SymbolIndex - prev.SymbolIndex
		if n > remaining {
			n = remaining
		}
		if err := decode(prev, n); err != nil {
			return out, err
		}
		prev = cp
	}
	// Tail after the last checkpoint, up to the end-of-stream marker.
	if remaining > 0 {
		if err := decode(prev, remaining); err != nil {
			return out, err
		}
	}
	return out, nil
}
