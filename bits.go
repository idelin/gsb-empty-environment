package parquetcol

// This file implements Parquet's RLE/bit-packed hybrid encoding (used for
// definition/repetition levels and dictionary indices) together with the
// LSB-first bit packing primitives it shares with PLAIN-encoded booleans.

// bitWidth returns the smallest bit width that can represent all values.
func bitWidth(maxValue int) int {
	if maxValue <= 0 {
		return 0
	}
	w := 0
	for (1 << w) <= maxValue {
		w++
	}
	return w
}

// packBitsLSB packs vals (each fitting in width bits) LSB-first into bytes.
// Every full group of 8 values occupies exactly width bytes.
func packBitsLSB(vals []int, width int) []byte {
	if width == 0 {
		return nil
	}
	out := make([]byte, (len(vals)*width+7)/8)
	bitPos := 0
	for _, v := range vals {
		for b := 0; b < width; b++ {
			if (v>>b)&1 == 1 {
				out[bitPos/8] |= 1 << (bitPos % 8)
			}
			bitPos++
		}
	}
	return out
}

// unpackBitsLSB reverses packBitsLSB for count values of the given width.
func unpackBitsLSB(data []byte, width, count int) []int {
	out := make([]int, count)
	if width == 0 {
		return out
	}
	bitPos := 0
	for i := 0; i < count; i++ {
		v := 0
		for b := 0; b < width; b++ {
			idx := bitPos / 8
			if idx < len(data) && data[idx]&(1<<(bitPos%8)) != 0 {
				v |= 1 << b
			}
			bitPos++
		}
		out[i] = v
	}
	return out
}

// encodeRLEBitPackedHybrid encodes vals using Parquet's hybrid encoding. Runs
// of eight or more equal values use RLE; everything else is emitted in
// bit-packed groups of eight.
func encodeRLEBitPackedHybrid(vals []int, width int) []byte {
	if width == 0 || len(vals) == 0 {
		return nil
	}
	var out []byte
	for i := 0; i < len(vals); {
		run := 1
		for i+run < len(vals) && vals[i+run] == vals[i] {
			run++
		}
		if run >= 8 {
			out = appendUvarint(out, uint64(run<<1))
			byteLen := (width + 7) / 8
			for b := 0; b < byteLen; b++ {
				out = append(out, byte(vals[i]>>(8*b)))
			}
			i += run
			continue
		}
		// Emit exactly one bit-packed group of up to eight values, padding
		// the remainder with zeros. The decoder limits emission to the real
		// value count.
		n := len(vals) - i
		if n > 8 {
			n = 8
		}
		group := make([]int, 8)
		copy(group, vals[i:i+n])
		out = appendUvarint(out, uint64((1<<1)|1))
		packed := packBitsLSB(group, width)
		out = append(out, packed...)
		// packBitsLSB yields exactly width bytes for eight values; ensure the
		// length the decoder expects even when the final byte is zero.
		for len(packed) < width {
			out = append(out, 0)
			packed = append(packed, 0)
		}
		i += 8
		if i > len(vals) {
			i = len(vals)
		}
	}
	return out
}

// hybridDecoder incrementally decodes an RLE/bit-packed hybrid stream.
type hybridDecoder struct {
	data    []byte
	pos     int
	width   int
	rleVal  int
	rleLeft int
	packed  []int // decoded values of the current bit-packed run
	packAt  int
	limit   int // maximum values to emit (padding excluded)
	emitted int
}

func newHybridDecoder(data []byte, width int) *hybridDecoder {
	return &hybridDecoder{data: data, width: width, limit: -1}
}

// next returns the next value or false when the stream is exhausted.
func (d *hybridDecoder) next() (int, bool) {
	for {
		if d.limit >= 0 && d.emitted >= d.limit {
			return 0, false
		}
		if d.rleLeft > 0 {
			d.rleLeft--
			d.emitted++
			return d.rleVal, true
		}
		if d.packAt < len(d.packed) {
			v := d.packed[d.packAt]
			d.packAt++
			if d.packAt == len(d.packed) {
				d.packed = nil
				d.packAt = 0
			}
			d.emitted++
			return v, true
		}
		if d.pos >= len(d.data) {
			return 0, false
		}
		header, n := consumeUvarint(d.data[d.pos:])
		if n == 0 {
			return 0, false
		}
		d.pos += n
		if header&1 == 0 {
			// RLE run.
			d.rleLeft = int(header >> 1)
			byteLen := (d.width + 7) / 8
			if d.pos+byteLen > len(d.data) {
				return 0, false
			}
			v := 0
			for b := 0; b < byteLen; b++ {
				v |= int(d.data[d.pos+b]) << (8 * b)
			}
			d.rleVal = v
			d.pos += byteLen
		} else {
			groups := int(header >> 1)
			count := groups * 8
			byteLen := groups * d.width
			if d.pos+byteLen > len(d.data) {
				return 0, false
			}
			src := make([]byte, byteLen)
			copy(src, d.data[d.pos:d.pos+byteLen])
			d.packed = unpackBitsLSB(src, d.width, count)
			d.packAt = 0
			d.pos += byteLen
		}
	}
}

// decodeAll returns exactly count decoded values.
func (d *hybridDecoder) decodeAll(count int) ([]int, error) {
	out := make([]int, 0, count)
	if d.width == 0 {
		for len(out) < count {
			out = append(out, 0)
		}
		return out, nil
	}
	d.limit = count
	for len(out) < count {
		v, ok := d.next()
		if !ok {
			return nil, errCorrupt("hybrid encoding ended early")
		}
		out = append(out, v)
	}
	return out, nil
}

// appendUvarint appends an unsigned LEB128 varint.
func appendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// consumeUvarint reads an unsigned LEB128 varint, returning the value and the
// number of bytes consumed.
func consumeUvarint(buf []byte) (uint64, int) {
	var x uint64
	var shift uint
	for i, b := range buf {
		if i >= 10 {
			return 0, 0
		}
		if b < 0x80 {
			return x | uint64(b)<<shift, i + 1
		}
		x |= uint64(b&0x7f) << shift
		shift += 7
	}
	return 0, 0
}

type corruptError string

func errCorrupt(msg string) error { return corruptError(msg) }

func (e corruptError) Error() string { return "parquetcol: corrupt file: " + string(e) }
