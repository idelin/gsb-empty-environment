package dod

// bitWriter packs values of arbitrary bit width, LSB-first.
type bitWriter struct {
	buf []byte
	cur byte
	n   uint
}

func (w *bitWriter) write(v uint64, width uint) {
	for width > 0 {
		take := 8 - w.n
		if take > width {
			take = width
		}
		mask := uint64(1)<<take - 1
		w.cur |= byte((v & mask) << w.n)
		w.n += take
		v >>= take
		width -= take
		if w.n == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur = 0
			w.n = 0
		}
	}
}

func (w *bitWriter) bytes() []byte {
	if w.n > 0 {
		return append(w.buf, w.cur)
	}
	return w.buf
}

// bitReader is the inverse of bitWriter.
type bitReader struct {
	buf []byte
	pos int
	n   uint
	err error
}

func (r *bitReader) read(width uint) uint64 {
	var v uint64
	var got uint
	for width > 0 {
		if r.pos >= len(r.buf) {
			r.err = ErrTruncated
			return 0
		}
		take := 8 - r.n
		if take > width {
			take = width
		}
		mask := uint64(1)<<take - 1
		v |= (uint64(r.buf[r.pos]) >> r.n & mask) << got
		got += take
		r.n += take
		width -= take
		if r.n == 8 {
			r.pos++
			r.n = 0
		}
	}
	return v
}

// zigzag maps int64 to uint64 so small magnitudes (incl. negatives) stay small.
func zigzag(x int64) uint64 { return uint64(x<<1) ^ uint64(x>>63) }

func unzigzag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// checkedSub / checkedAdd detect int64 overflow so extreme deltas never wrap.
func checkedSub(a, b int64) (int64, bool) {
	d := a - b
	if (a^b) < 0 && (d^a) < 0 {
		return 0, false
	}
	return d, true
}

func checkedAdd(a, b int64) (int64, bool) {
	s := a + b
	if (a^b) >= 0 && (s^a) < 0 {
		return 0, false
	}
	return s, true
}

func uvlen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

func svlen(v int64) int { return uvlen(zigzag(v)) }

// widthClasses are the permitted per-segment bit widths.
var widthClasses = []uint{0, 2, 4, 8, 16, 32, 64}

func widthFor(v uint64) uint {
	for _, w := range widthClasses {
		if w == 64 || v < uint64(1)<<w {
			return w
		}
	}
	return 64
}

func validWidth(w uint) bool {
	for _, c := range widthClasses {
		if c == w {
			return true
		}
	}
	return false
}
