package adaptive

import "io"

// bitWriter 以 MSB-first 顺序向 io.Writer 写位。
type bitWriter struct {
	w   io.Writer
	buf byte  // 已累积但未写出的位，位于低 n 位
	n   uint8 // buf 中有效位数
	err error
}

func (bw *bitWriter) writeBit(bit byte) {
	if bw.err != nil {
		return
	}
	bw.buf = bw.buf<<1 | (bit & 1)
	bw.n++
	if bw.n == 8 {
		if _, err := bw.w.Write([]byte{bw.buf}); err != nil {
			bw.err = err
		}
		bw.buf, bw.n = 0, 0
	}
}

// writeByte 以 MSB-first 写 8 个原始位（escape 后的字面字节）。
func (bw *bitWriter) writeByte(b byte) {
	for i := 7; i >= 0; i-- {
		bw.writeBit(b >> uint(i))
	}
}

// flush 用 0 位填充到字节边界（帧末填充规则）。
func (bw *bitWriter) flush() error {
	if bw.err != nil {
		return bw.err
	}
	if bw.n > 0 {
		bw.buf <<= 8 - bw.n
		if _, err := bw.w.Write([]byte{bw.buf}); err != nil {
			bw.err = err
			return err
		}
		bw.buf, bw.n = 0, 0
	}
	return nil
}

// bitReader 以 MSB-first 顺序从 io.Reader 读位，支持分块（短读）输入。
type bitReader struct {
	r   io.Reader
	buf byte   // 当前字节，未消费位在低 n 位
	n   uint8  // buf 中剩余位数
	pos uint64 // 已消费的总位数
}

func (br *bitReader) readBit() (byte, error) {
	if br.n == 0 {
		var one [1]byte
		if _, err := io.ReadFull(br.r, one[:]); err != nil {
			return 0, err
		}
		br.buf, br.n = one[0], 8
	}
	br.n--
	bit := (br.buf >> br.n) & 1
	br.pos++
	return bit, nil
}

// readByte 读 8 个原始位（MSB-first）。
func (br *bitReader) readByte() (byte, error) {
	var b byte
	for i := 0; i < 8; i++ {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		b = b<<1 | bit
	}
	return b, nil
}
