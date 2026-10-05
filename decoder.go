package adaptivehuffman

// Decoder is an adaptive prefix decoder. It is not safe for concurrent use.
type Decoder struct {
	m *model
}

// NewDecoder returns a Decoder in the initial state (a single NYT node)
// with DefaultMaxWeight.
func NewDecoder() *Decoder { return NewDecoderWithMaxWeight(DefaultMaxWeight) }

// NewDecoderWithMaxWeight returns a Decoder in the initial state using the
// given root-weight rescale threshold. It must match the encoder's value.
func NewDecoderWithMaxWeight(maxWeight uint64) *Decoder {
	return &Decoder{m: newModel(maxWeight)}
}

// RestoreDecoder reconstructs a Decoder from a blob produced by ExportState.
// Continuing to decode with the restored decoder behaves identically to the
// original decoder.
func RestoreDecoder(state []byte) (*Decoder, error) {
	m, err := unmarshalModel(state)
	if err != nil {
		return nil, err
	}
	return &Decoder{m: m}, nil
}

// ExportState serializes the complete adaptive state. See FORMAT.md for the
// layout.
func (d *Decoder) ExportState() []byte { return d.m.marshal() }

// DecodeSymbol reads one symbol from br and updates the adaptive state.
//
// Errors are returned as *Error:
//   - ErrKindTruncated: the stream ended mid-codeword (reported at the bit
//     position where the incomplete codeword started);
//   - ErrKindInvalidEscape: an escape codeword appeared although all 256
//     symbols are already known, or the raw byte following an escape already
//     has a codeword.
func (d *Decoder) DecodeSymbol(br *BitReader) (byte, error) {
	start := br.total
	n := d.m.root
	for n.left != nil {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, br.errAt(ErrKindTruncated, start)
		}
		if bit == 0 {
			n = n.left
		} else {
			n = n.right
		}
	}
	var sym byte
	if n.symbol == symNYT {
		if d.m.numSyms == numSymbols {
			return 0, br.errAt(ErrKindInvalidEscape, start)
		}
		v, err := br.ReadBits(8)
		if err != nil {
			return 0, br.errAt(ErrKindTruncated, start)
		}
		sym = byte(v)
		if d.m.leaves[sym] != nil {
			return 0, br.errAt(ErrKindInvalidEscape, start)
		}
	} else {
		sym = byte(n.symbol)
	}
	d.m.update(sym)
	return sym, nil
}
