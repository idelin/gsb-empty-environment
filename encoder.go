package huffman

import "io"

// Encoder compresses a byte stream into an adaptive Huffman bit stream.
// It implements io.WriteCloser; Close must be called to emit the
// end-of-stream symbol and flush the final partial byte.
type Encoder struct {
	bw     *bitWriter
	t      *tree
	closed bool
}

// NewEncoder returns an Encoder writing compressed data to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{bw: newBitWriter(w), t: newTree()}
}

// Write compresses p in a single streaming pass. It may be called any
// number of times; the tree adapts after every byte.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, io.ErrClosedPipe
	}
	for _, b := range p {
		e.encodeSymbol(uint16(b))
	}
	if e.bw.err != nil {
		return 0, e.bw.err
	}
	return len(p), nil
}

func (e *Encoder) encodeSymbol(symbol uint16) {
	if leaf, seen := e.t.leaves[symbol]; seen {
		e.bw.writeBits(codeOf(leaf))
	} else {
		e.bw.writeBits(codeOf(e.t.nyt))
		e.bw.writeValue(symbol, rawSymbolBits)
	}
	e.t.update(symbol)
}

// Close emits the end-of-stream symbol and flushes any buffered bits.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.encodeSymbol(eofSymbol)
	return e.bw.flush()
}
