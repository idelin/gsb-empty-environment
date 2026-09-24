// Package bitplane compresses fixed-width integers by splitting them into
// bit planes and compressing each plane independently.
//
// Values are grouped into fixed-size blocks (the tail block may be shorter
// and is always preserved). Within a block every bit plane is encoded with
// the cheapest of three modes: literal, run-length, or delta. The encoder
// picks the block size and per-plane modes by minimizing the real total
// byte count, including the header, the block index, and per-plane record
// headers. A block index at the front of the stream allows random range
// decoding without touching unrelated blocks.
//
// Format layout (all integers little-endian or uvarint):
//
//	header:  magic "BPC1" | version u8 | flags u8 (bit0: signed) | bits u8 |
//	         reserved u8 | count uvarint | blockSize uvarint |
//	         numBlocks uvarint | headerCRC uint32 (CRC32 of all prior bytes)
//	index:   per block: payloadLen uvarint | payloadCRC uint32
//	blocks:  per block, per plane: mode u8 | payloadLen uvarint | payload
//	trailer: dataCRC uint32 (CRC32 of the decoded int64 values, 8-byte LE each)
package bitplane

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Version is the on-disk format version written by this package.
const Version = 1

// MaxBits is the maximum supported integer width in bits.
const MaxBits = 64

const (
	flagSigned     byte = 1
	maxBlockSearch      = 8192
	trailerLen          = 4
)

var magic = []byte{'B', 'P', 'C', '1'}

var (
	// ErrFormat is returned when the input does not follow the format.
	ErrFormat = errors.New("bitplane: invalid format")
	// ErrChecksum is returned when a header, block, or data CRC mismatches.
	ErrChecksum = errors.New("bitplane: checksum mismatch")
	// ErrQuota is returned when decoding would exceed the output quota.
	ErrQuota = errors.New("bitplane: output quota exceeded")
	// ErrRange is returned for an out-of-bounds decode range.
	ErrRange = errors.New("bitplane: range out of bounds")
)

// Options configures the encoder.
type Options struct {
	// Bits is the integer width in bits, 1..64. Only the low Bits of each
	// value are stored.
	Bits int
	// Signed enables zigzag mapping so negative values compress well.
	// Decoding sign-extends back, preserving the original bit pattern.
	Signed bool
	// MaxBlockSize caps the candidate block sizes. Zero uses the default
	// (8192). The search never considers blocks larger than 8192.
	MaxBlockSize int
}

// Header describes a parsed stream.
type Header struct {
	Version   int
	Bits      int
	Signed    bool
	Count     int
	BlockSize int
	NumBlocks int
}

// Encode compresses values and writes the stream to w.
func Encode(w io.Writer, values []int64, opts Options) error {
	data, err := EncodeBytes(values, opts)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// EncodeBytes compresses values and returns the encoded stream.
func EncodeBytes(values []int64, opts Options) ([]byte, error) {
	if opts.Bits < 1 || opts.Bits > MaxBits {
		return nil, fmt.Errorf("bitplane: Bits must be in [1,%d], got %d", MaxBits, opts.Bits)
	}
	n := len(values)
	mapped := mapValues(values, opts)
	planes := buildPlanes(mapped, opts.Bits)
	bestB, bestSize := 1, -1
	for _, b := range candidateBlockSizes(n, opts.MaxBlockSize) {
		if s := encodedSize(planes, opts.Bits, n, b); bestSize < 0 || s < bestSize {
			bestSize, bestB = s, b
		}
	}
	return encode(values, planes, opts, bestB), nil
}

// mapValues masks values to opts.Bits and applies zigzag mapping when signed.
func mapValues(values []int64, opts Options) []uint64 {
	mask := maskFor(opts.Bits)
	mapped := make([]uint64, len(values))
	for i, v := range values {
		u := uint64(v) & mask
		if opts.Signed {
			u = zigzag(signExtend(u, opts.Bits)) & mask
		}
		mapped[i] = u
	}
	return mapped
}

// buildPlanes transposes mapped values into one bitset per bit position.
func buildPlanes(mapped []uint64, bits int) [][]uint64 {
	n := len(mapped)
	nw := (n + 63) / 64
	planes := make([][]uint64, bits)
	for p := 0; p < bits; p++ {
		w := make([]uint64, nw)
		for i, v := range mapped {
			if (v>>uint(p))&1 == 1 {
				w[i>>6] |= 1 << uint(i&63)
			}
		}
		planes[p] = w
	}
	return planes
}

// encodedSize models the exact total stream size for a given block size,
// including header, index, per-plane record headers, and trailer.
func encodedSize(planes [][]uint64, bits, n, blockSize int) int {
	numBlocks := 0
	if n > 0 {
		numBlocks = (n-1)/blockSize + 1
	}
	size := 8 + uvarintLen(uint64(n)) + uvarintLen(uint64(blockSize)) +
		uvarintLen(uint64(numBlocks)) + 4 // header + header CRC
	for s := 0; s < n; s += blockSize {
		e := s + blockSize
		if e > n {
			e = n
		}
		payload := 0
		for p := 0; p < bits; p++ {
			ps, _ := planeCost(planes[p], s, e)
			payload += 1 + uvarintLen(uint64(ps)) + ps
		}
		size += uvarintLen(uint64(payload)) + 4 + payload // index entry + block
	}
	return size + trailerLen
}

// encode builds the actual stream; its length always equals encodedSize.
func encode(values []int64, planes [][]uint64, opts Options, blockSize int) []byte {
	n := len(values)
	numBlocks := 0
	if n > 0 {
		numBlocks = (n-1)/blockSize + 1
	}
	var flags byte
	if opts.Signed {
		flags = flagSigned
	}
	buf := []byte{'B', 'P', 'C', '1', Version, flags, byte(opts.Bits), 0}
	buf = binary.AppendUvarint(buf, uint64(n))
	buf = binary.AppendUvarint(buf, uint64(blockSize))
	buf = binary.AppendUvarint(buf, uint64(numBlocks))
	var crcb [4]byte
	binary.LittleEndian.PutUint32(crcb[:], crc32.ChecksumIEEE(buf))
	buf = append(buf, crcb[:]...)

	payloads := make([][]byte, 0, numBlocks)
	for s := 0; s < n; s += blockSize {
		e := s + blockSize
		if e > n {
			e = n
		}
		var blk []byte
		for p := 0; p < opts.Bits; p++ {
			size, mode := planeCost(planes[p], s, e)
			blk = append(blk, mode)
			blk = binary.AppendUvarint(blk, uint64(size))
			blk = appendPlanePayload(blk, planes[p], s, e, mode)
		}
		payloads = append(payloads, blk)
	}
	for _, blk := range payloads {
		buf = binary.AppendUvarint(buf, uint64(len(blk)))
		binary.LittleEndian.PutUint32(crcb[:], crc32.ChecksumIEEE(blk))
		buf = append(buf, crcb[:]...)
	}
	for _, blk := range payloads {
		buf = append(buf, blk...)
	}

	h := crc32.NewIEEE()
	var vb [8]byte
	for _, v := range values {
		binary.LittleEndian.PutUint64(vb[:], uint64(v))
		h.Write(vb[:])
	}
	binary.LittleEndian.PutUint32(crcb[:], h.Sum32())
	return append(buf, crcb[:]...)
}

// candidateBlockSizes lists block sizes the encoder evaluates. Every size
// up to 128 is tried exactly, which keeps small inputs fully optimal.
func candidateBlockSizes(n, maxBlock int) []int {
	if n == 0 {
		return []int{1}
	}
	cap := maxBlockSearch
	if maxBlock > 0 && maxBlock < cap {
		cap = maxBlock
	}
	seen := make(map[int]bool)
	var out []int
	add := func(b int) {
		if b >= 1 && b <= n && b <= cap && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	lim := n
	if lim > 128 {
		lim = 128
	}
	if lim > cap {
		lim = cap
	}
	for b := 1; b <= lim; b++ {
		add(b)
	}
	for b := 192; b <= n && b <= cap; b += 64 {
		add(b)
	}
	add(n)
	return out
}

func maskFor(bits int) uint64 {
	if bits >= 64 {
		return ^uint64(0)
	}
	return (1 << uint(bits)) - 1
}

func zigzag(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

// signExtend interprets the low bits of u as a two's complement value of
// the given width and returns it as a full int64.
func signExtend(u uint64, bits int) int64 {
	if bits >= 64 {
		return int64(u)
	}
	if u&(1<<uint(bits-1)) != 0 {
		return int64(u | ^maskFor(bits))
	}
	return int64(u)
}
