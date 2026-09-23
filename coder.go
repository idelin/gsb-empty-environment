package arcode

import "io"

// 32 位区间常量。区间 [low, high] 用 uint64 保存但始终截断在 32 位内，
// range * cumFreq 最大约 2^32 * 2^14 = 2^46，uint64 不会溢出。
const (
	topValue uint64 = 0xFFFFFFFF
	firstQtr uint64 = 1 << 30
	half     uint64 = 1 << 31
	thirdQtr uint64 = 3 << 30
)

// bitWriter 按 MSB 优先写比特。
type bitWriter struct {
	w   io.Writer
	buf byte
	n   uint
	err error
}

func (b *bitWriter) writeBit(bit uint64) {
	if b.err != nil {
		return
	}
	b.buf = b.buf<<1 | byte(bit&1)
	b.n++
	if b.n == 8 {
		_, b.err = b.w.Write([]byte{b.buf})
		b.buf, b.n = 0, 0
	}
}

func (b *bitWriter) flush() error {
	if b.err != nil {
		return b.err
	}
	if b.n > 0 {
		b.buf <<= 8 - b.n
		if _, err := b.w.Write([]byte{b.buf}); err != nil {
			return err
		}
		b.buf, b.n = 0, 0
	}
	return nil
}

// bitReader 按 MSB 优先读比特；流耗尽后返回 0 比特。
// 解码的符号数被头部长度严格限定（length+1 个），
// 每个符号最多触发约 30 次归一化移位，因此读取比特总数有限，
// 补零不会导致无限解码。
type bitReader struct {
	data []byte
	pos  int // 比特位置
}

func (b *bitReader) readBit() uint64 {
	idx := b.pos >> 3
	if idx >= len(b.data) {
		b.pos++
		return 0
	}
	bit := (b.data[idx] >> (7 - uint(b.pos&7))) & 1
	b.pos++
	return uint64(bit)
}

// encoder 是算术编码器。
type encoder struct {
	low, high uint64
	pending   uint64 // 下溢挂起比特计数
	bw        *bitWriter
	m         *model
}

func newEncoder(w io.Writer) *encoder {
	return &encoder{high: topValue, bw: &bitWriter{w: w}, m: newModel()}
}

func (e *encoder) emit(bit uint64) {
	e.bw.writeBit(bit)
	inv := bit ^ 1
	for ; e.pending > 0; e.pending-- {
		e.bw.writeBit(inv)
	}
}

func (e *encoder) encodeSymbol(s int) {
	cumLow, cumHigh := e.m.cumulative(s)
	r := e.high - e.low + 1
	e.high = e.low + (r*uint64(cumHigh))/uint64(e.m.total) - 1
	e.low = e.low + (r*uint64(cumLow))/uint64(e.m.total)
	for {
		switch {
		case e.high < half:
			e.emit(0)
		case e.low >= half:
			e.emit(1)
			e.low -= half
			e.high -= half
		case e.low >= firstQtr && e.high < thirdQtr:
			e.pending++
			e.low -= firstQtr
			e.high -= firstQtr
		default:
			e.m.update(s)
			return
		}
		e.low <<= 1
		e.high = e.high<<1 | 1
	}
}

func (e *encoder) finish() error {
	e.pending++
	if e.low < firstQtr {
		e.emit(0)
	} else {
		e.emit(1)
	}
	return e.bw.flush()
}

// decoder 是算术解码器，与 encoder 状态严格镜像。
type decoder struct {
	low, high, value uint64
	br               *bitReader
	m                *model
}

func newDecoder(payload []byte) *decoder {
	d := &decoder{high: topValue, br: &bitReader{data: payload}, m: newModel()}
	for i := 0; i < 32; i++ {
		d.value = d.value<<1 | d.br.readBit()
	}
	return d
}

func (d *decoder) decodeSymbol() int {
	r := d.high - d.low + 1
	scaled := uint32(((d.value-d.low+1)*uint64(d.m.total) - 1) / r)
	s, cumLow, cumHigh := d.m.find(scaled)
	d.high = d.low + (r*uint64(cumHigh))/uint64(d.m.total) - 1
	d.low = d.low + (r*uint64(cumLow))/uint64(d.m.total)
	for {
		switch {
		case d.high < half:
			// 无需操作
		case d.low >= half:
			d.low -= half
			d.high -= half
			d.value -= half
		case d.low >= firstQtr && d.high < thirdQtr:
			d.low -= firstQtr
			d.high -= firstQtr
			d.value -= firstQtr
		default:
			d.m.update(s)
			return s
		}
		d.low <<= 1
		d.high = d.high<<1 | 1
		d.value = d.value<<1 | d.br.readBit()
	}
}
