package cabac

// bitWriter 按 MSB 优先顺序把比特写入字节流。
type bitWriter struct {
	buf   []byte
	cur   byte
	nbits uint
	total int
}

func (w *bitWriter) writeBit(b int) {
	w.cur = w.cur<<1 | byte(b&1)
	w.nbits++
	w.total++
	if w.nbits == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.nbits = 0
	}
}

// finish 把未满一字节的剩余比特低位补零后收尾，返回完整字节流。
func (w *bitWriter) finish() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.nbits))
		w.cur = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader 按 MSB 优先顺序读取比特；读到流外时返回 0（等价于尾部补零）。
type bitReader struct {
	data []byte
	pos  int
	bit  uint
}

func (r *bitReader) readBit() int {
	if r.pos >= len(r.data) {
		return 0
	}
	b := (r.data[r.pos] >> (7 - r.bit)) & 1
	r.bit++
	if r.bit == 8 {
		r.bit = 0
		r.pos++
	}
	return int(b)
}
