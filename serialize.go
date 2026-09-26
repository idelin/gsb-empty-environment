package efano

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"math/bits"
)

// CorruptError reports a failed structural or checksum verification.
type CorruptError struct{ What string }

func (e *CorruptError) Error() string { return "efano: corrupt encoding: " + e.What }

// Serialized layout (all integers little-endian), 48-byte header:
//
//	[0:4]   magic "EFAN"
//	[4]     version = 1
//	[5]     low-bit width l (0..63)
//	[6:8]   reserved, must be zero
//	[8:16]  n, number of values
//	[16:24] max value (0 when n == 0)
//	[24:32] high vector length in bits
//	[32:40] low vector length in bits (must equal n*l)
//	[40:44] reserved, must be zero
//	[44:48] CRC-32/IEEE of header[0:44] || payload
//	[48:]   high bytes (ceil(highBits/8)), then low bytes (ceil(lowBits/8))
//
// Parse validates magic, version, reserved bytes, exact total length,
// lowBits == n*l, the high-vector population count == n, zero padding
// bits, and the CRC, before any allocation sized by the header fields,
// so corrupt or hostile inputs cannot trigger huge allocations.
const (
	headerSize = 48
	formatVer  = 1
	magic      = "EFAN"
)

// SerializedSize returns the exact byte size of MarshalBinary output.
func (ef *EliasFano) SerializedSize() uint64 {
	return headerSize + (ef.high.nbits+7)/8 + (ef.low.nbits+7)/8
}

// MarshalBinary serializes the index to its canonical byte form.
func (ef *EliasFano) MarshalBinary() []byte {
	hb, lb := ef.high.bytes(), ef.low.bytes()
	out := make([]byte, headerSize, headerSize+len(hb)+len(lb))
	copy(out[0:4], magic)
	out[4] = formatVer
	out[5] = byte(ef.l)
	binary.LittleEndian.PutUint64(out[8:], ef.n)
	binary.LittleEndian.PutUint64(out[16:], ef.maxVal)
	binary.LittleEndian.PutUint64(out[24:], ef.high.nbits)
	binary.LittleEndian.PutUint64(out[32:], ef.low.nbits)
	out = append(out, hb...)
	out = append(out, lb...)
	crc := crc32.NewIEEE()
	crc.Write(out[:44])
	crc.Write(out[headerSize:])
	binary.LittleEndian.PutUint32(out[44:], crc.Sum32())
	return out
}

// Parse decodes and structurally verifies a serialized index.
func Parse(data []byte) (*EliasFano, error) {
	if uint64(len(data)) < headerSize {
		return nil, &CorruptError{What: "shorter than header"}
	}
	if string(data[0:4]) != magic {
		return nil, &CorruptError{What: "bad magic"}
	}
	if data[4] != formatVer {
		return nil, &CorruptError{What: fmt.Sprintf("unsupported version %d", data[4])}
	}
	l := uint(data[5])
	if l > 63 {
		return nil, &CorruptError{What: "low-bit width > 63"}
	}
	if data[6] != 0 || data[7] != 0 || binary.LittleEndian.Uint32(data[40:]) != 0 {
		return nil, &CorruptError{What: "reserved header bytes not zero"}
	}
	n := binary.LittleEndian.Uint64(data[8:])
	maxVal := binary.LittleEndian.Uint64(data[16:])
	highBits := binary.LittleEndian.Uint64(data[24:])
	lowBits := binary.LittleEndian.Uint64(data[32:])

	crc := crc32.NewIEEE()
	crc.Write(data[:44])
	crc.Write(data[headerSize:])
	if binary.LittleEndian.Uint32(data[44:]) != crc.Sum32() {
		return nil, &CorruptError{What: "CRC mismatch"}
	}
	// Length arithmetic below is overflow-safe: reject absurd bit counts
	// before deriving byte counts, and never allocate from them until the
	// exact total length has been confirmed against the input.
	if highBits > math.MaxUint64-7 || lowBits > math.MaxUint64-7 {
		return nil, &CorruptError{What: "bit length overflow"}
	}
	want := uint64(headerSize) + (highBits+7)/8 + (lowBits+7)/8
	if uint64(len(data)) != want {
		return nil, &CorruptError{What: "payload length mismatch"}
	}
	if l > 0 && n > math.MaxUint64/uint64(l) {
		return nil, &CorruptError{What: "n*l overflow"}
	}
	if lowBits != n*uint64(l) {
		return nil, &CorruptError{What: "low length != n*l"}
	}
	if n == 0 && highBits != 0 {
		return nil, &CorruptError{What: "empty sequence with high bits"}
	}
	if n > 0 && highBits < n {
		return nil, &CorruptError{What: "high vector shorter than n ones"}
	}

	off := uint64(headerSize)
	high := bitvecFromBytes(data[off:off+(highBits+7)/8], highBits)
	off += (highBits + 7) / 8
	low := bitvecFromBytes(data[off:off+(lowBits+7)/8], lowBits)
	if !high.paddingZero() || !low.paddingZero() {
		return nil, &CorruptError{What: "non-zero padding bits"}
	}
	var ones uint64
	for _, w := range high.words {
		ones += uint64(bits.OnesCount64(w))
	}
	if ones != n {
		return nil, &CorruptError{What: "high population count != n"}
	}

	ef := &EliasFano{n: n, l: l, maxVal: maxVal, high: high, low: low}
	ef.buildRank()
	return ef, nil
}
