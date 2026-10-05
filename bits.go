package adaptivehuffman

import "io"

// BitWriter packs bits into bytes, most-significant-bit first, and writes
// them to an io.Writer. Use NewBitWriter to create one.
type BitWriter struct {
	w     io.Writer
	buf   []byte
	cur   uint8  // byte under construction; the next bit goes to position (7-used)
	used  uint8  // number of bits already set in cur
	total uint64 // total bits written, including alignment padding
	err   error
}

// NewBitWriter returns a BitWriter writing to w.
func NewBitWriter(w io.Writer) *BitWriter { return &BitWriter{w: w} }

// Err returns the first write error encountered, if any.
func (bw *BitWriter) Err() error { return bw.err }

// BitsWritten returns the number of bits written so far, including any
// padding added by Align or Flush.
func (bw *BitWriter) BitsWritten() uint64 { return bw.total }

// WriteBit appends a single bit (the low bit of bit).
func (bw *BitWriter) WriteBit(bit uint32) {
	if bw.err != nil {
		return
	}
	if bit&1 != 0 {
		bw.cur |= 1 << (7 - bw.used)
	}
	bw.used++
	bw.total++
	if bw.used == 8 {
		bw.buf = append(bw.buf, bw.cur)
		bw.cur = 0
		bw.used = 0
		if len(bw.buf) >= 4096 {
			bw.flushBuf()
		}
	}
}

// WriteBits appends the n low bits of v, most significant bit first.
func (bw *BitWriter) WriteBits(v uint64, n uint) {
	for i := n; i > 0; i-- {
		bw.WriteBit(uint32(v>>(i-1)) & 1)
	}
}

// Align pads the current partial byte with zero bits and returns the number
// of padding bits added (0 if already aligned).
func (bw *BitWriter) Align() uint {
	if bw.used == 0 {
		return 0
	}
	pad := uint(8 - bw.used)
	bw.buf = append(bw.buf, bw.cur)
	bw.cur = 0
	bw.used = 0
	bw.total += uint64(pad)
	return pad
}

func (bw *BitWriter) flushBuf() {
	if bw.err != nil || len(bw.buf) == 0 {
		return
	}
	_, bw.err = bw.w.Write(bw.buf)
	bw.buf = bw.buf[:0]
}

// Flush zero-pads to a byte boundary (like Align) and flushes all buffered
// bytes to the underlying writer.
func (bw *BitWriter) Flush() error {
	bw.Align()
	bw.flushBuf()
	return bw.err
}

// BitReader reads bits from an io.Reader, most-significant-bit first.
// Use NewBitReader to create one.
type BitReader struct {
	r       io.Reader
	scratch [512]byte
	buf     []byte // prefetched whole bytes not yet consumed (aliases scratch)
	cur     uint8  // byte currently being consumed
	used    uint8  // bits of cur already consumed
	haveCur bool
	total   uint64 // total bits consumed
	eof     bool
	base    int64 // byte offset added to reported positions (frame header size)
}

// NewBitReader returns a BitReader reading from r.
func NewBitReader(r io.Reader) *BitReader { return &BitReader{r: r} }

// BitsRead returns the number of bits consumed so far.
func (br *BitReader) BitsRead() uint64 { return br.total }

// ReadBit reads a single bit. It returns io.EOF when the stream is exhausted.
func (br *BitReader) ReadBit() (uint32, error) {
	if !br.haveCur {
		for len(br.buf) == 0 {
			if br.eof {
				return 0, io.EOF
			}
			n, err := br.r.Read(br.scratch[:])
			if n > 0 {
				br.buf = br.scratch[:n]
				if err == io.EOF {
					br.eof = true
				}
				break
			}
			if err != nil {
				br.eof = true
				if err == io.EOF {
					return 0, io.EOF
				}
				return 0, err
			}
		}
		br.cur = br.buf[0]
		br.buf = br.buf[1:]
		br.haveCur = true
		br.used = 0
	}
	bit := uint32(br.cur>>(7-br.used)) & 1
	br.used++
	br.total++
	if br.used == 8 {
		br.haveCur = false
	}
	return bit, nil
}

// ReadBits reads n bits and returns them as the n low bits of the result,
// first bit read being the most significant.
func (br *BitReader) ReadBits(n uint) (uint64, error) {
	var v uint64
	for i := uint(0); i < n; i++ {
		b, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | uint64(b)
	}
	return v, nil
}

// Position returns the current read position as a byte offset and a bit
// offset within that byte (0 = most significant bit).
func (br *BitReader) Position() (byteOff int64, bitOff uint8) {
	pos := br.base*8 + int64(br.total)
	return pos / 8, uint8(pos % 8)
}

// errAt builds an *Error whose position corresponds to the bit position
// startBits (counted from the start of this BitReader's stream).
func (br *BitReader) errAt(kind ErrorKind, startBits uint64) *Error {
	pos := br.base*8 + int64(startBits)
	return &Error{Kind: kind, Byte: pos / 8, Bit: uint8(pos % 8)}
}

// hasMore reports whether any unconsumed bytes remain, either buffered or in
// the underlying reader.
func (br *BitReader) hasMore() (bool, error) {
	if len(br.buf) > 0 {
		return true, nil
	}
	if br.eof {
		return false, nil
	}
	n, err := br.r.Read(br.scratch[:])
	if n > 0 {
		br.buf = br.scratch[:n]
		return true, nil
	}
	if err == nil {
		return false, nil
	}
	br.eof = true
	if err == io.EOF {
		return false, nil
	}
	return false, err
}
