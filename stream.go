package mtf

import "encoding/binary"

// UnknownPolicy selects how the encoder treats symbols outside the
// alphabet.
type UnknownPolicy int

const (
	// RejectUnknown makes the encoder fail on unknown symbols.
	RejectUnknown UnknownPolicy = iota
	// EscapeUnknown emits an explicit escape token and extends the
	// alphabet through the shared update protocol.
	EscapeUnknown
)

// BlockEncoder encodes one block of symbols into a token stream. Tokens
// are uvarint indices; the index equal to the current alphabet size is the
// escape token and is followed by one literal byte naming the new symbol.
// Both sides then extend the alphabet (bumping its version) and move the
// new symbol to the front, so the models stay in lockstep without any
// private encoder-side state.
type BlockEncoder struct {
	m      *Model
	policy UnknownPolicy
	out    []byte
}

func NewBlockEncoder(m *Model, policy UnknownPolicy) *BlockEncoder {
	return &BlockEncoder{m: m, policy: policy}
}

// Write encodes one symbol.
func (e *BlockEncoder) Write(sym byte) error {
	idx, err := e.m.EncodeIndex(sym)
	if err == nil {
		e.out = binary.AppendUvarint(e.out, uint64(idx))
		return nil
	}
	if err != ErrUnknownSymbol {
		return err
	}
	if e.policy == RejectUnknown {
		return err
	}
	// Escape token: index == current size, then the literal symbol.
	e.out = binary.AppendUvarint(e.out, uint64(e.m.Alphabet().Size()))
	e.out = append(e.out, sym)
	return e.m.extend(sym)
}

// Bytes returns the encoded token stream for the block.
func (e *BlockEncoder) Bytes() []byte { return e.out }

// BlockDecoder decodes one block with the mirror of the encoder protocol.
// The decoder's model must start in the same state the encoder had at the
// block boundary (fresh model, Reset, or Restore from the checkpoint taken
// at that boundary).
type BlockDecoder struct {
	m *Model
}

func NewBlockDecoder(m *Model) *BlockDecoder { return &BlockDecoder{m: m} }

// Decode decodes the whole block, producing at most maxOut symbols. The
// output is bounded: exceeding maxOut fails with ErrOutputLimit. On any
// error the model may be partially advanced and should be discarded or
// restored from the block-boundary checkpoint.
func (d *BlockDecoder) Decode(data []byte, maxOut int) ([]byte, error) {
	out := make([]byte, 0, min(maxOut, len(data)))
	for len(data) > 0 {
		v, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, ErrCorruptStream
		}
		data = data[n:]
		size := uint64(d.m.Alphabet().Size())
		var sym byte
		switch {
		case v < size:
			s, err := d.m.DecodeIndex(int(v))
			if err != nil {
				return nil, err
			}
			sym = s
		case v == size: // escape token
			if len(data) < 1 {
				return nil, ErrCorruptStream
			}
			sym = data[0]
			data = data[1:]
			if d.m.Alphabet().Contains(sym) {
				return nil, ErrCorruptStream // escape of a known symbol
			}
			if err := d.m.extend(sym); err != nil {
				return nil, err
			}
		default:
			return nil, ErrIndexOutOfRange
		}
		out = append(out, sym)
		if len(out) > maxOut {
			return nil, ErrOutputLimit
		}
	}
	return out, nil
}
