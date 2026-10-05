package adaptivehuffman

// Encoder is an adaptive prefix encoder. It is not safe for concurrent use.
type Encoder struct {
	m       *model
	pathBuf []byte
}

// NewEncoder returns an Encoder in the initial state (a single NYT node)
// with DefaultMaxWeight.
func NewEncoder() *Encoder { return NewEncoderWithMaxWeight(DefaultMaxWeight) }

// NewEncoderWithMaxWeight returns an Encoder in the initial state using the
// given root-weight rescale threshold. Values below 2 select
// DefaultMaxWeight. The decoder must use the same threshold.
func NewEncoderWithMaxWeight(maxWeight uint64) *Encoder {
	return &Encoder{m: newModel(maxWeight)}
}

// RestoreEncoder reconstructs an Encoder from a blob produced by
// ExportState. Continuing to encode with the restored encoder produces a bit
// stream identical to the one the original encoder would have produced.
func RestoreEncoder(state []byte) (*Encoder, error) {
	m, err := unmarshalModel(state)
	if err != nil {
		return nil, err
	}
	return &Encoder{m: m}, nil
}

// ExportState serializes the complete adaptive state. See FORMAT.md for the
// layout.
func (e *Encoder) ExportState() []byte { return e.m.marshal() }

// EncodeSymbol appends the code for sym to bw and updates the adaptive
// state. A symbol seen for the first time is emitted as the escape (NYT)
// code followed by its raw 8 bits, most significant bit first.
func (e *Encoder) EncodeSymbol(bw *BitWriter, sym byte) error {
	if leaf := e.m.leaves[sym]; leaf != nil {
		e.writePath(bw, leaf)
	} else {
		e.writePath(bw, e.m.nyt)
		bw.WriteBits(uint64(sym), 8)
	}
	e.m.update(sym)
	return bw.Err()
}

// writePath writes the code for node n: the path from the root, 0 for a left
// edge and 1 for a right edge.
func (e *Encoder) writePath(bw *BitWriter, n *node) {
	path := e.pathBuf[:0]
	for p := n; p.parent != nil; p = p.parent {
		if p.parent.right == p {
			path = append(path, 1)
		} else {
			path = append(path, 0)
		}
	}
	for i := len(path) - 1; i >= 0; i-- {
		bw.WriteBit(uint32(path[i]))
	}
	e.pathBuf = path
}
