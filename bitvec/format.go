// Package bitvec implements a compressed bitmap index with hierarchical
// rank1/select1 sampling, a self-describing serialized format, and a
// read-only mapped view whose query cost is measured in bytes actually
// touched and blocks actually decoded. Queries never decompress the
// whole bitmap.
//
// Serialized layout (all integers little-endian):
//
//	header    116 B   magic "RSB1", version, bitLen, counts, sampling
//	                  parameters, section offsets/lengths, per-section
//	                  CRC32, header CRC32
//	directory 8 B/blk per-block: type u8, popcount u16, reserved u8,
//	                  data offset u32 (relative to data section)
//	superindex 8 B/sb cumulative ones before each superblock
//	selindex  16 B/s  per select sample: block u32, reserved u32,
//	                  rank base u64 (cumulative ones before that block)
//	data      var     block payloads: zero/one blocks store nothing,
//	                  sparse blocks store u8 count + u16 positions,
//	                  raw blocks store 64 bytes
//
// Query boundary semantics (unified):
//   - Rank1(i) counts ones in [0, min(i, bitLen)); i is clamped.
//   - Select1(k) returns the position of the k-th one (0-based);
//     ok=false when k >= total ones (e.g. an all-zero bitmap).
//   - Tail padding bits beyond bitLen never participate in queries.
package bitvec

const (
	// Version is the format version written by Encode and accepted by Map.
	Version = 1

	// BlockBits is the number of bits per compressed block.
	BlockBits = 512
	// BlockBytes is the payload size of a raw block.
	BlockBytes = BlockBits / 8
	// BlocksPerSuper is the rank-sampling rate: one superblock entry
	// covers this many blocks.
	BlocksPerSuper = 32
	// SelectRate is the select-sampling rate: one sample per this many
	// one-bits.
	SelectRate = 64

	// sparseMax is the largest popcount stored in sparse form.
	// 1 + 2*24 = 49 bytes < 64 bytes raw.
	sparseMax = 24

	headerSize      = 116
	dirEntrySize    = 8
	superEntrySize  = 8
	sampleEntrySize = 16
)

// Block payload types.
const (
	blkZero   = 0 // 512 zero bits, no payload
	blkOne    = 1 // 512 one bits, no payload
	blkSparse = 2 // u8 count + count*u16 bit positions
	blkRaw    = 3 // 64 raw bytes
)

var magic = [4]byte{'R', 'S', 'B', '1'}

// Metrics counts the actual query cost on a mapped bitmap: bytes read
// from the mapping and compressed blocks decoded. A correct query
// touches O(1) blocks, never the whole bitmap.
type Metrics struct {
	BytesRead     uint64
	BlocksTouched uint64
}

// Reset zeroes the counters.
func (m *Metrics) Reset() { *m = Metrics{} }

func (m *Metrics) addBytes(n uint64) {
	if m != nil {
		m.BytesRead += n
	}
}

func (m *Metrics) addBlock() {
	if m != nil {
		m.BlocksTouched++
	}
}

// Layout reports the real byte occupancy of each serialized section.
type Layout struct {
	Header      int
	Directory   int
	SuperIndex  int
	SelectIndex int
	Data        int
	Total       int
}
