package adaptivehuffman

import "io"

// Encoder compresses a byte stream with FGK adaptive Huffman coding.
// It implements io.Writer: bytes may be supplied in arbitrarily sized
// chunks and are encoded on the fly in a single pass.
type Encoder struct {
	w     io.Writer
	t     *tree
	acc   byte // bit accumulator, filled MSB first
	nbits uint // number of valid bits in acc
}

// NewEncoder returns an Encoder writing the compressed bit stream to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, t: newTree()}
}

// Write encodes p and returns len(p). Bytes are processed one at a
// time; the tree is updated after every byte.
func (e *Encoder) Write(p []byte) (int, error) {
	for i, b := range p {
		if err := e.encodeByte(b); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

func (e *Encoder) encodeByte(b byte) error {
	if leaf, ok := e.t.leaves[b]; ok {
		// Known byte: emit its current code, then adapt.
		if err := e.writeBits(pathBits(leaf)); err != nil {
			return err
		}
		e.t.update(leaf)
		return nil
	}
	// New byte: emit the NYT code followed by the raw 8-bit value,
	// then grow the tree.
	if err := e.writeBits(pathBits(e.t.nyt)); err != nil {
		return err
	}
	raw := make([]byte, 8)
	for i := 0; i < 8; i++ {
		raw[i] = b >> uint(7-i) & 1
	}
	if err := e.writeBits(raw); err != nil {
		return err
	}
	e.t.insert(b)
	return nil
}

// Close flushes the final partially filled byte. The padding bits
// follow the path from the root toward the NYT node, so the decoder
// either stops mid-descent or reaches NYT with fewer than 8 bits left;
// in both cases it recognizes a clean end of stream. Close does not
// close the underlying writer.
func (e *Encoder) Close() error {
	if e.nbits == 0 {
		return nil
	}
	path := pathBits(e.t.nyt)
	for i := 0; e.nbits != 0; i++ {
		var bit byte
		if i < len(path) {
			bit = path[i]
		}
		if err := e.writeBit(bit); err != nil {
			return err
		}
	}
	return nil
}

// TreeSize returns the current number of nodes in the adaptive tree.
// It never exceeds 2*256+1 = 513 regardless of the input length.
func (e *Encoder) TreeSize() int { return len(e.t.all) }

func (e *Encoder) writeBits(bits []byte) error {
	for _, bit := range bits {
		if err := e.writeBit(bit); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) writeBit(bit byte) error {
	e.acc = e.acc<<1 | bit
	e.nbits++
	if e.nbits == 8 {
		if _, err := e.w.Write([]byte{e.acc}); err != nil {
			return err
		}
		e.acc, e.nbits = 0, 0
	}
	return nil
}

// Decoder decompresses a stream produced by Encoder. It implements
// io.Reader and adapts its tree in lockstep with the encoder, so the
// original bytes are recovered in a single streaming pass.
type Decoder struct {
	r     io.Reader
	t     *tree
	cur   *node // current position while descending the tree
	buf   byte  // bit buffer, consumed MSB first
	nbits uint  // number of unconsumed bits in buf
	done  bool  // stream exhausted
}

// NewDecoder returns a Decoder reading a compressed stream from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r, t: newTree()}
}

// Read decompresses into p, returning io.EOF once the stream is
// exhausted.
func (d *Decoder) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		b, err := d.next()
		if err != nil {
			if err == io.EOF && n > 0 {
				return n, nil
			}
			return n, err
		}
		p[n] = b
		n++
	}
	return n, nil
}

// next decodes and returns the next byte of plaintext.
func (d *Decoder) next() (byte, error) {
	if d.done {
		return 0, io.EOF
	}
	for {
		if d.cur == nil {
			d.cur = d.t.root
		}
		if d.cur.isLeaf() {
			if d.cur == d.t.nyt {
				// NYT: the next 8 bits are a raw new byte.
				// Fewer than 8 bits available means we consumed
				// the encoder's end-of-stream padding.
				sym, ok := d.readRawByte()
				if !ok {
					d.done = true
					return 0, io.EOF
				}
				d.t.insert(sym)
				d.cur = nil
				return sym, nil
			}
			b := byte(d.cur.symbol)
			d.t.update(d.cur)
			d.cur = nil
			return b, nil
		}
		bit, ok := d.readBit()
		if !ok {
			// End of stream reached mid-descent: only the
			// encoder's padding can end like this.
			d.done = true
			return 0, io.EOF
		}
		if bit == 0 {
			d.cur = d.cur.left
		} else {
			d.cur = d.cur.right
		}
	}
}

// TreeSize returns the current number of nodes in the adaptive tree.
func (d *Decoder) TreeSize() int { return len(d.t.all) }

func (d *Decoder) readBit() (byte, bool) {
	if d.nbits == 0 {
		var one [1]byte
		if _, err := io.ReadFull(d.r, one[:]); err != nil {
			return 0, false
		}
		d.buf = one[0]
		d.nbits = 8
	}
	bit := d.buf >> 7
	d.buf <<= 1
	d.nbits--
	return bit, true
}

func (d *Decoder) readRawByte() (byte, bool) {
	var b byte
	for i := 0; i < 8; i++ {
		bit, ok := d.readBit()
		if !ok {
			return 0, false
		}
		b = b<<1 | bit
	}
	return b, true
}
