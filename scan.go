package jpegrotate

import (
	"bytes"
	"errors"
	"io"
)

// bitReader reads bits MSB-first from a byte slice.
type bitReader struct {
	data []byte
	pos  int // current byte
	bit  int // next bit within current byte, 0..7
}

func (b *bitReader) readBit() (int32, error) {
	if b.pos >= len(b.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := int32(b.data[b.pos]>>(7-b.bit)) & 1
	b.bit++
	if b.bit == 8 {
		b.bit = 0
		b.pos++
	}
	return v, nil
}

func (b *bitReader) readBits(n int) (int32, error) {
	var v int32
	for i := 0; i < n; i++ {
		bit, err := b.readBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | bit
	}
	return v, nil
}

// align discards bits up to the next byte boundary (used at restart
// intervals).
func (b *bitReader) align() {
	if b.bit != 0 {
		b.bit = 0
		b.pos++
	}
}

// bitWriter writes bits MSB-first, inserting a 0x00 byte after every 0xFF
// data byte.
type bitWriter struct {
	w   *bytes.Buffer
	acc byte
	n   int
}

func (b *bitWriter) writeBits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		b.acc = b.acc<<1 | byte(v>>i&1)
		b.n++
		if b.n == 8 {
			b.emit()
		}
	}
}

func (b *bitWriter) emit() {
	b.w.WriteByte(b.acc)
	if b.acc == 0xFF {
		b.w.WriteByte(0x00)
	}
	b.acc = 0
	b.n = 0
}

// flush pads the current byte with 1 bits, as required by the JPEG spec.
func (b *bitWriter) flush() {
	if b.n > 0 {
		b.acc <<= 8 - b.n
		b.acc |= 1<<(8-b.n) - 1
		b.emit()
	}
}

func (h *huffTable) decode(br *bitReader) (byte, error) {
	var code int32
	for l := 0; l < 16; l++ {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		code = code<<1 | bit
		if h.maxcode[l] >= 0 && code <= h.maxcode[l] {
			return h.values[h.valptr[l]+code-h.mincode[l]], nil
		}
	}
	return 0, errors.New("jpegrotate: invalid Huffman code")
}

// receiveExtend reads s bits and converts them to a signed difference
// value per the JPEG EXTEND procedure.
func receiveExtend(br *bitReader, s int) (int32, error) {
	v, err := br.readBits(s)
	if err != nil {
		return 0, err
	}
	if v < 1<<(s-1) {
		v -= 1<<s - 1
	}
	return v, nil
}

// category returns the magnitude category (bit size) of v.
func category(v int32) int {
	if v < 0 {
		v = -v
	}
	s := 0
	for v > 0 {
		s++
		v >>= 1
	}
	return s
}

// decodeScan decodes all MCUs of the scan into per-component coefficient
// block grids.
func (j *jpegImage) decodeScan() error {
	mcusX, mcusY := j.mcuCounts()
	j.coefs = make([]compCoeffs, len(j.comps))
	for i, c := range j.comps {
		bw, bh := mcusX*c.h, mcusY*c.v
		j.coefs[i] = compCoeffs{bw: bw, bh: bh, blocks: make([]block, bw*bh)}
	}
	total := mcusX * mcusY
	expectedRST := 0
	if j.restartInterval > 0 {
		expectedRST = (total - 1) / j.restartInterval
	}
	if j.rstCount != expectedRST {
		return errors.New("jpegrotate: restart marker count does not match restart interval")
	}

	br := &bitReader{data: j.entropy}
	dcPred := make([]int32, len(j.comps))
	n := 0
	for my := 0; my < mcusY; my++ {
		for mx := 0; mx < mcusX; mx++ {
			if j.restartInterval > 0 && n > 0 && n%j.restartInterval == 0 {
				br.align()
				for i := range dcPred {
					dcPred[i] = 0
				}
			}
			for ci, c := range j.comps {
				co := &j.coefs[ci]
				for v := 0; v < c.v; v++ {
					for h := 0; h < c.h; h++ {
						b := &co.blocks[(my*c.v+v)*co.bw+mx*c.h+h]
						if err := decodeBlock(br, c.dc, c.ac, b, &dcPred[ci]); err != nil {
							return err
						}
					}
				}
			}
			n++
		}
	}
	return nil
}

func decodeBlock(br *bitReader, dc, ac *huffTable, b *block, pred *int32) error {
	s, err := dc.decode(br)
	if err != nil {
		return err
	}
	if s > 0 {
		diff, err := receiveExtend(br, int(s))
		if err != nil {
			return err
		}
		*pred += diff
	}
	b[0] = *pred
	k := 1
	for k < 64 {
		rs, err := ac.decode(br)
		if err != nil {
			return err
		}
		r, ss := int(rs>>4), int(rs&15)
		if ss == 0 {
			if r != 15 {
				break // EOB
			}
			k += 16 // ZRL
			continue
		}
		k += r
		if k > 63 {
			return errors.New("jpegrotate: coefficient index out of range")
		}
		v, err := receiveExtend(br, ss)
		if err != nil {
			return err
		}
		b[zigzag[k]] = v
		k++
	}
	return nil
}

// encodeScan entropy-encodes the coefficient block grids into buf.
func (j *jpegImage) encodeScan(buf *bytes.Buffer) {
	mcusX := j.coefs[0].bw / j.comps[0].h
	mcusY := j.coefs[0].bh / j.comps[0].v
	bw := &bitWriter{w: buf}
	dcPred := make([]int32, len(j.comps))
	rst := 0
	n := 0
	for my := 0; my < mcusY; my++ {
		for mx := 0; mx < mcusX; mx++ {
			if j.restartInterval > 0 && n > 0 && n%j.restartInterval == 0 {
				bw.flush()
				buf.WriteByte(0xFF)
				buf.WriteByte(0xD0 + byte(rst%8))
				rst++
				for i := range dcPred {
					dcPred[i] = 0
				}
			}
			for ci, c := range j.comps {
				co := &j.coefs[ci]
				for v := 0; v < c.v; v++ {
					for h := 0; h < c.h; h++ {
						b := &co.blocks[(my*c.v+v)*co.bw+mx*c.h+h]
						encodeBlock(bw, c.dc, c.ac, b, &dcPred[ci])
					}
				}
			}
			n++
		}
	}
	bw.flush()
}

func encodeBlock(bw *bitWriter, dc, ac *huffTable, b *block, pred *int32) {
	diff := b[0] - *pred
	*pred = b[0]
	s := category(diff)
	bw.writeBits(uint32(dc.encCodes[s]), int(dc.encSizes[s]))
	if s > 0 {
		v := diff
		if v < 0 {
			v += 1<<s - 1
		}
		bw.writeBits(uint32(v), s)
	}
	run := 0
	for k := 1; k < 64; k++ {
		c := b[zigzag[k]]
		if c == 0 {
			run++
			continue
		}
		for run >= 16 {
			bw.writeBits(uint32(ac.encCodes[0xF0]), int(ac.encSizes[0xF0])) // ZRL
			run -= 16
		}
		s := category(c)
		sym := run<<4 | s
		bw.writeBits(uint32(ac.encCodes[sym]), int(ac.encSizes[sym]))
		v := c
		if v < 0 {
			v += 1<<s - 1
		}
		bw.writeBits(uint32(v), s)
		run = 0
	}
	if run > 0 {
		bw.writeBits(uint32(ac.encCodes[0x00]), int(ac.encSizes[0x00])) // EOB
	}
}
