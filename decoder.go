package huffman

import "io"

// Decoder decompresses a stream produced by Encoder. It implements
// io.Reader and rebuilds the identical adaptive tree in lockstep with the
// encoder, so no tree or frequency table is ever transmitted.
type Decoder struct {
	br  *bitReader
	t   *tree
	eof bool
}

// NewDecoder returns a Decoder reading compressed data from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{br: newBitReader(r), t: newTree()}
}

// Read decodes into p, returning io.EOF once the end-of-stream symbol
// written by Encoder.Close has been reached.
func (d *Decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.eof {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		symbol, err := d.decodeSymbol()
		if err != nil {
			return n, err
		}
		if symbol == eofSymbol {
			d.eof = true
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		}
		p[n] = byte(symbol)
		n++
	}
	return n, nil
}

// decodeSymbol walks the tree bit by bit until it reaches a leaf, then
// mirrors the encoder's tree update. A well-formed stream always ends with
// the end-of-stream symbol, so hitting physical EOF means the stream is
// truncated and is reported as io.ErrUnexpectedEOF.
func (d *Decoder) decodeSymbol() (uint16, error) {
	current := d.t.root
	for !current.isLeaf() {
		bit, err := d.br.readBit()
		if err != nil {
			return 0, unexpectedEOF(err)
		}
		if bit {
			current = current.right
		} else {
			current = current.left
		}
	}
	var symbol uint16
	if current == d.t.nyt {
		raw, err := d.br.readValue(rawSymbolBits)
		if err != nil {
			return 0, unexpectedEOF(err)
		}
		symbol = raw
	} else {
		symbol = current.symbol
	}
	d.t.update(symbol)
	return symbol, nil
}

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
