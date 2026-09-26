package eliasfano

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"math"
	"math/bits"
)

// Serialized format (self-describing, independently verifiable):
//
//	[0:8)    magic "EFANO001"
//	uvarint  n            value count
//	uvarint  universe     U, values in [0, U)
//	uvarint  lowBits      l, must equal max(0, floor(log2(U/n)))
//	uvarint  numBuckets   must equal ceil(U / 2^l)
//	uvarint  lowWords     must equal ceil(n*l / 64)
//	uvarint  highWords    must equal ceil((n+numBuckets) / 64)
//	lowWords*8   bytes    low bits, little-endian 64-bit words
//	highWords*8  bytes    high unary bitvector, little-endian words
//	[8 bytes]  CRC64-ISO of everything above
//
// Parse re-derives every parameter from (n, universe), checks the
// payload length exactly, verifies the high bitvector popcount equals
// n, requires unused tail bits to be zero, and validates the checksum.
// Any single-bit mutation anywhere in the blob is therefore detected.

var magic = [8]byte{'E', 'F', 'A', 'N', 'O', '0', '0', '1'}

var crcTable = crc64.MakeTable(crc64.ISO)

// headerUvarints is the number of uvarint fields after the magic.
const headerUvarints = 6

// Marshal serializes the index into the verifiable byte format.
func (ef *EliasFano) Marshal() []byte {
	out := make([]byte, 0, ef.SizeBytes())
	out = append(out, magic[:]...)
	out = binary.AppendUvarint(out, ef.n)
	out = binary.AppendUvarint(out, ef.universe)
	out = binary.AppendUvarint(out, ef.lowBits)
	out = binary.AppendUvarint(out, ef.numBuckets)
	out = binary.AppendUvarint(out, uint64(len(ef.low)))
	out = binary.AppendUvarint(out, uint64(len(ef.high)))
	var word [8]byte
	for _, w := range ef.low {
		binary.LittleEndian.PutUint64(word[:], w)
		out = append(out, word[:]...)
	}
	for _, w := range ef.high {
		binary.LittleEndian.PutUint64(word[:], w)
		out = append(out, word[:]...)
	}
	binary.LittleEndian.PutUint64(word[:], crc64.Checksum(out, crcTable))
	return append(out, word[:]...)
}

// SizeBytes returns the exact serialized size in bytes.
func (ef *EliasFano) SizeBytes() int {
	size := len(magic) + 8 // magic + checksum
	for _, v := range [headerUvarints]uint64{
		ef.n, ef.universe, ef.lowBits, ef.numBuckets,
		uint64(len(ef.low)), uint64(len(ef.high)),
	} {
		size += uvarintLen(v)
	}
	return size + 8*(len(ef.low)+len(ef.high))
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// Parse decodes and fully validates a serialized index. It never
// allocates based on unverified header claims: every length is first
// cross-checked against the actual byte count, so corrupt or hostile
// input (including absurd n or universe) cannot trigger huge
// allocations.
func Parse(data []byte) (*EliasFano, error) {
	if len(data) < len(magic)+8 {
		return nil, fmt.Errorf("%w: too short (%d bytes)", errCorrupt, len(data))
	}
	if !bytes.Equal(data[:len(magic)], magic[:]) {
		return nil, fmt.Errorf("%w: bad magic", errCorrupt)
	}
	body := data[:len(data)-8]
	wantCRC := binary.LittleEndian.Uint64(data[len(data)-8:])
	if got := crc64.Checksum(body, crcTable); got != wantCRC {
		return nil, fmt.Errorf("%w: checksum mismatch", errCorrupt)
	}
	rest := body[len(magic):]
	fields := make([]uint64, 0, headerUvarints)
	for i := 0; i < headerUvarints; i++ {
		v, k := binary.Uvarint(rest)
		if k <= 0 {
			return nil, fmt.Errorf("%w: truncated header", errCorrupt)
		}
		fields = append(fields, v)
		rest = rest[k:]
	}
	n, universe, lowBits := fields[0], fields[1], fields[2]
	numBuckets, lowWords, highWords := fields[3], fields[4], fields[5]

	// Re-derive parameters independently from (n, universe).
	if lowBits != lowBitsFor(n, universe) {
		return nil, fmt.Errorf("%w: lowBits=%d inconsistent with n=%d U=%d",
			errCorrupt, lowBits, n, universe)
	}
	if numBuckets != numBucketsFor(n, universe, lowBits) {
		return nil, fmt.Errorf("%w: numBuckets=%d inconsistent", errCorrupt, numBuckets)
	}
	// Overflow-safe bit accounting.
	var lowBitLen uint64
	if lowBits > 0 {
		if n > math.MaxUint64/lowBits {
			return nil, fmt.Errorf("%w: n*lowBits overflows", errCorrupt)
		}
		lowBitLen = n * lowBits
	}
	if n > math.MaxUint64-numBuckets {
		return nil, fmt.Errorf("%w: n+numBuckets overflows", errCorrupt)
	}
	highBitLen := n + numBuckets
	if lowWords != ceilDiv64(lowBitLen) {
		return nil, fmt.Errorf("%w: lowWords=%d, want %d", errCorrupt, lowWords, ceilDiv64(lowBitLen))
	}
	if highWords != ceilDiv64(highBitLen) {
		return nil, fmt.Errorf("%w: highWords=%d, want %d", errCorrupt, highWords, ceilDiv64(highBitLen))
	}
	if lowWords > math.MaxUint64-highWords {
		return nil, fmt.Errorf("%w: word count overflows", errCorrupt)
	}
	totalWords := lowWords + highWords
	if totalWords > math.MaxUint64/8 {
		return nil, fmt.Errorf("%w: payload size overflows", errCorrupt)
	}
	if uint64(len(rest)) != totalWords*8 {
		return nil, fmt.Errorf("%w: payload is %d bytes, header implies %d",
			errCorrupt, len(rest), totalWords*8)
	}

	low := make([]uint64, lowWords)
	high := make([]uint64, highWords)
	for i := range low {
		low[i] = binary.LittleEndian.Uint64(rest[i*8:])
	}
	rest = rest[lowWords*8:]
	for i := range high {
		high[i] = binary.LittleEndian.Uint64(rest[i*8:])
	}

	// Structural checks on the decoded bitvectors.
	var ones uint64
	for _, w := range high {
		ones += uint64(bits.OnesCount64(w))
	}
	if ones != n {
		return nil, fmt.Errorf("%w: high bitvector holds %d ones, want %d", errCorrupt, ones, n)
	}
	if err := checkTailBits(low, lowBitLen); err != nil {
		return nil, err
	}
	if err := checkTailBits(high, highBitLen); err != nil {
		return nil, err
	}
	return &EliasFano{
		n:          n,
		universe:   universe,
		lowBits:    lowBits,
		numBuckets: numBuckets,
		highBits:   highBitLen,
		low:        low,
		high:       high,
	}, nil
}

// ceilDiv64 returns ceil(v/64) without overflow.
func ceilDiv64(v uint64) uint64 {
	return v/64 + boolToUint(v%64 != 0)
}

func boolToUint(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// checkTailBits requires bits at positions >= totalBits to be zero.
func checkTailBits(words []uint64, totalBits uint64) error {
	if len(words) == 0 || totalBits%64 == 0 {
		return nil
	}
	if words[len(words)-1]>>(totalBits%64) != 0 {
		return fmt.Errorf("%w: non-zero bits past logical end", errCorrupt)
	}
	return nil
}

// Validate re-marshals and re-parses the index, confirming the in-memory
// structure satisfies every format invariant. Useful as an independent
// check after manual construction.
func (ef *EliasFano) Validate() error {
	if _, err := Parse(ef.Marshal()); err != nil {
		return err
	}
	return nil
}
