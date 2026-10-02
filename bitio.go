package huffman

import "io"

// bitWriter packs bits MSB-first into an underlying io.Writer.
type bitWriter struct {
	w     io.Writer
	buf   byte
	nbits uint
	err   error
}

func newBitWriter(w io.Writer) *bitWriter { return &bitWriter{w: w} }

func (bw *bitWriter) writeBit(bit bool) {
	if bw.err != nil {
		return
	}
	bw.buf <<= 1
	if bit {
		bw.buf |= 1
	}
	bw.nbits++
	if bw.nbits == 8 {
		bw.err = bw.flushByte()
	}
}

func (bw *bitWriter) writeBits(bits []bool) {
	for _, bit := range bits {
		bw.writeBit(bit)
	}
}

// writeValue writes the n low-order bits of value, MSB-first.
func (bw *bitWriter) writeValue(value uint16, n uint) {
	for i := n; i > 0; i-- {
		bw.writeBit(value&(1<<(i-1)) != 0)
	}
}

func (bw *bitWriter) flushByte() error {
	if _, err := bw.w.Write([]byte{bw.buf}); err != nil {
		return err
	}
	bw.buf = 0
	bw.nbits = 0
	return nil
}

// flush pads the final partial byte with zero bits and flushes it.
func (bw *bitWriter) flush() error {
	if bw.err != nil {
		return bw.err
	}
	if bw.nbits > 0 {
		bw.buf <<= 8 - bw.nbits
		bw.err = bw.flushByte()
	}
	return bw.err
}

// bitReader reads bits MSB-first from an underlying io.Reader.
type bitReader struct {
	r     io.Reader
	buf   byte
	nbits uint
}

func newBitReader(r io.Reader) *bitReader { return &bitReader{r: r} }

func (br *bitReader) readBit() (bool, error) {
	if br.nbits == 0 {
		var b [1]byte
		if _, err := io.ReadFull(br.r, b[:]); err != nil {
			return false, err
		}
		br.buf = b[0]
		br.nbits = 8
	}
	bit := br.buf&0x80 != 0
	br.buf <<= 1
	br.nbits--
	return bit, nil
}

// readValue reads n bits MSB-first and returns them as a value.
func (br *bitReader) readValue(n uint) (uint16, error) {
	var value uint16
	for i := uint(0); i < n; i++ {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		value <<= 1
		if bit {
			value |= 1
		}
	}
	return value, nil
}
