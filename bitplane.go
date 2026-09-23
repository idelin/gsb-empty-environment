// Package bitplane compresses fixed-width integers by splitting them into
// bit planes and encoding each plane chunk with the cheapest of literal,
// run-length, or delta coding. Blocks are independently decodable so a
// random interval can be read without expanding unrelated blocks.
//
// Wire format (all integers little-endian):
//
//	Header (44 bytes):
//	  0  magic[4]   "BPC1"
//	  4  version    uint8   (== Version)
//	  5  flags      uint8   (bit0: signed zigzag mapping)
//	  6  width      uint8   (bits per value, 1..64)
//	  7  reserved   uint8   (must be 0)
//	  8  count      uint64  (number of values)
//	  16 blockSize  uint32  (values per block)
//	  20 numBlocks  uint32  (== ceil(count/blockSize))
//	  24 indexLen   uint64  (== numBlocks * 16)
//	  32 dataLen    uint64  (bytes of block payloads)
//	  40 headerCRC  uint32  (CRC32-IEEE of bytes 0..39)
//	Index (numBlocks entries, 16 bytes each):
//	  offset uint64 (relative to start of data section)
//	  length uint32
//	  crc32  uint32 (CRC32-IEEE of the block payload)
//	Data: concatenated block payloads.
//
//	Block payload: for each plane p in 0..width-1:
//	  mode uint8 (0=literal, 1=rle, 2=delta) followed by the payload.
package bitplane

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Version is the on-disk format version.
const Version = 1

const (
	headerSize     = 44
	indexEntrySize = 16
	maxWidth       = 64
	flagSigned     = 1 << 0
)

var magic = [4]byte{'B', 'P', 'C', '1'}

// ErrQuotaExceeded is returned when the decoded output would exceed the
// caller-supplied value quota.
var ErrQuotaExceeded = errors.New("bitplane: output quota exceeded")

// Options configures the encoder.
type Options struct {
	// Width is the number of significant bits per value, 1..64.
	// Zero means 64.
	Width int
	// Signed applies a zigzag mapping before plane splitting so that
	// small-magnitude negative values produce sparse high planes.
	Signed bool
}

// Stats reports the real byte cost of an encoding, header included.
type Stats struct {
	Values      int
	Width       int
	Signed      bool
	BlockSize   int
	NumBlocks   int
	HeaderBytes int
	IndexBytes  int
	DataBytes   int
	TotalBytes  int
	RawBytes    int // Values * 8, the uncompressed int64 size
	// ModeCounts tallies how many plane chunks use each mode
	// (literal, rle, delta).
	ModeCounts [3]int
}

// Ratio returns TotalBytes / RawBytes.
func (s Stats) Ratio() float64 {
	if s.RawBytes == 0 {
		return 0
	}
	return float64(s.TotalBytes) / float64(s.RawBytes)
}

func (o Options) normalized() (int, error) {
	w := o.Width
	if w == 0 {
		w = maxWidth
	}
	if w < 1 || w > maxWidth {
		return 0, fmt.Errorf("bitplane: width %d out of range [1,64]", w)
	}
	return w, nil
}

func mask(w int) uint64 {
	if w >= maxWidth {
		return ^uint64(0)
	}
	return (1 << uint(w)) - 1
}

// zigzagEncode maps the signed w-bit value in x to an unsigned w-bit
// pattern so small magnitudes (positive or negative) stay small.
func zigzagEncode(x uint64, w int) uint64 {
	s := signExtend(x, w)
	return ((uint64(s) << 1) ^ uint64(s>>63)) & mask(w)
}

func zigzagDecode(z uint64, w int) uint64 {
	z &= mask(w)
	s := int64(z>>1) ^ -int64(z&1)
	return uint64(s) & mask(w)
}

func signExtend(x uint64, w int) int64 {
	if w >= maxWidth {
		return int64(x)
	}
	return int64(x<<uint(maxWidth-w)) >> uint(maxWidth-w)
}

func bitAt(plane []uint64, i int) uint64 {
	return (plane[i>>6] >> uint(i&63)) & 1
}

func setBit(plane []uint64, i int) {
	plane[i>>6] |= 1 << uint(i&63)
}

// buildPlanes splits the low w bits of every value into w bit planes.
func buildPlanes(vals []int64, w int, signed bool) [][]uint64 {
	n := len(vals)
	planes := make([][]uint64, w)
	for p := range planes {
		planes[p] = make([]uint64, (n+63)/64)
	}
	m := mask(w)
	for i, v := range vals {
		x := uint64(v) & m
		if signed {
			x = zigzagEncode(x, w)
		}
		for p := 0; p < w; p++ {
			if x>>uint(p)&1 == 1 {
				setBit(planes[p], i)
			}
		}
	}
	return planes
}

// candidateBlockSizes returns the block sizes the encoder evaluates.
// For small inputs it is exhaustive (1..n); for large inputs it uses a
// representative set so encoding stays fast.
func candidateBlockSizes(n int) []int {
	if n <= 0 {
		return []int{1}
	}
	if n <= 1024 {
		all := make([]int, n)
		for i := range all {
			all[i] = i + 1
		}
		return all
	}
	seen := make(map[int]bool)
	var out []int
	add := func(b int) {
		if b >= 1 && b <= n && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	for b := 1; b <= 8192; b *= 2 {
		add(b)
	}
	for k := 1; k <= 128; k++ {
		add((n + k - 1) / k)
	}
	add(n)
	return out
}

// totalCost computes the exact on-disk size in bytes for a block size,
// including header and index.
func totalCost(planes [][]uint64, n, w, blockSize int) int {
	numBlocks := (n + blockSize - 1) / blockSize
	total := headerSize + numBlocks*indexEntrySize
	for b := 0; b < numBlocks; b++ {
		start := b * blockSize
		bl := blockSize
		if rem := n - start; rem < bl {
			bl = rem
		}
		for p := 0; p < w; p++ {
			_, size := bestPlaneMode(planes[p], start, bl)
			total += 1 + size
		}
	}
	return total
}

// Encode compresses vals, choosing the block size and per-plane modes that
// minimize the real total byte count (header + index + data). It returns
// the encoded buffer together with honest statistics.
func Encode(vals []int64, opts Options) ([]byte, Stats, error) {
	w, err := opts.normalized()
	if err != nil {
		return nil, Stats{}, err
	}
	planes := buildPlanes(vals, w, opts.Signed)
	bestB, bestCost := 1, int(^uint(0)>>1)
	for _, b := range candidateBlockSizes(len(vals)) {
		if c := totalCost(planes, len(vals), w, b); c < bestCost {
			bestCost, bestB = c, b
		}
	}
	return encodeWithBlockSize(planes, len(vals), w, opts.Signed, bestB)
}

// encodeWithBlockSize encodes with a fixed block size (used by Encode and
// by tests that need deterministic layouts).
func encodeWithBlockSize(planes [][]uint64, n, w int, signed bool, blockSize int) ([]byte, Stats, error) {
	if blockSize < 1 {
		return nil, Stats{}, fmt.Errorf("bitplane: invalid block size %d", blockSize)
	}
	numBlocks := (n + blockSize - 1) / blockSize
	indexLen := numBlocks * indexEntrySize

	var data []byte
	type entry struct {
		off, ln uint64
		crc     uint32
	}
	entries := make([]entry, 0, numBlocks)
	stats := Stats{
		Values:    n,
		Width:     w,
		Signed:    signed,
		BlockSize: blockSize,
		NumBlocks: numBlocks,
		RawBytes:  n * 8,
	}
	for b := 0; b < numBlocks; b++ {
		start := b * blockSize
		bl := blockSize
		if rem := n - start; rem < bl {
			bl = rem
		}
		var block []byte
		for p := 0; p < w; p++ {
			mode, _ := bestPlaneMode(planes[p], start, bl)
			stats.ModeCounts[mode]++
			block = append(block, byte(mode))
			switch mode {
			case modeLiteral:
				block = appendLiteral(block, planes[p], start, bl)
			case modeRLE:
				block = appendRLE(block, planes[p], start, bl)
			case modeDelta:
				block = appendDelta(block, planes[p], start, bl)
			}
		}
		entries = append(entries, entry{
			off: uint64(len(data)),
			ln:  uint64(len(block)),
			crc: crc32.ChecksumIEEE(block),
		})
		data = append(data, block...)
	}

	buf := make([]byte, headerSize+indexLen, headerSize+indexLen+len(data))
	copy(buf[0:4], magic[:])
	buf[4] = Version
	if signed {
		buf[5] = flagSigned
	}
	buf[6] = byte(w)
	buf[7] = 0
	binary.LittleEndian.PutUint64(buf[8:], uint64(n))
	binary.LittleEndian.PutUint32(buf[16:], uint32(blockSize))
	binary.LittleEndian.PutUint32(buf[20:], uint32(numBlocks))
	binary.LittleEndian.PutUint64(buf[24:], uint64(indexLen))
	binary.LittleEndian.PutUint64(buf[32:], uint64(len(data)))
	binary.LittleEndian.PutUint32(buf[40:], crc32.ChecksumIEEE(buf[0:40]))
	for i, e := range entries {
		o := headerSize + i*indexEntrySize
		binary.LittleEndian.PutUint64(buf[o:], e.off)
		binary.LittleEndian.PutUint32(buf[o+8:], uint32(e.ln))
		binary.LittleEndian.PutUint32(buf[o+12:], e.crc)
	}
	buf = append(buf, data...)

	stats.HeaderBytes = headerSize
	stats.IndexBytes = indexLen
	stats.DataBytes = len(data)
	stats.TotalBytes = len(buf)
	return buf, stats, nil
}
