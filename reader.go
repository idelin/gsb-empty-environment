package bitstream

// Reader reads bits MSB-first from a byte slice.
type Reader struct {
	buf   []byte
	nbits int
	pos   int
}

// NewReader builds a reader over the first nbits bits of buf.
func NewReader(buf []byte, nbits int) (*Reader, error) {
	if nbits < 0 || nbits > len(buf)*8 {
		return nil, ErrBadSeek
	}
	return &Reader{buf: buf, nbits: nbits}, nil
}

// Remaining returns the number of unread bits.
func (r *Reader) Remaining() int { return r.nbits - r.pos }

// Pos returns the current read position in bits.
func (r *Reader) Pos() int { return r.pos }

// Bookmark returns the current read position for later Reset.
func (r *Reader) Bookmark() int { return r.pos }

// Reset returns the read position to a bookmark.
func (r *Reader) Reset(bm int) error {
	if bm < 0 || bm > r.nbits {
		return ErrBadSeek
	}
	r.pos = bm
	return nil
}

// ReadBits reads n bits MSB-first. On underflow the position is unchanged.
func (r *Reader) ReadBits(n int) (uint64, error) {
	if n < 1 || n > 64 {
		return 0, ErrBitCount
	}
	if r.pos+n > r.nbits {
		return 0, ErrUnderflow
	}
	var v uint64
	for i := 0; i < n; i++ {
		p := r.pos + i
		v = v<<1 | uint64((r.buf[p>>3]>>uint(7-(uint(p)&7)))&1)
	}
	r.pos += n
	return v, nil
}

// ReadSigned reads n bits and sign-extends them to int64.
func (r *Reader) ReadSigned(n int) (int64, error) {
	v, err := r.ReadBits(n)
	if err != nil {
		return 0, err
	}
	if n < 64 && v&(uint64(1)<<uint(n-1)) != 0 {
		v |= ^uint64(0) << uint(n)
	}
	return int64(v), nil
}
