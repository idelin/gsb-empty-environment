package bitpack

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Archive file layout:
//
//	Header (29 bytes):
//	  magic      4B  "BPK1"
//	  version    1B  = 1
//	  blockSize  4B  uint32 LE, values per block
//	  totalCount 8B  uint64 LE
//	  blockCount 4B  uint32 LE
//	  indexOff   8B  uint64 LE, offset of the block index
//	Block records (back to back):
//	  payloadLen 4B  uint32 LE
//	  payload    payloadLen bytes (see block.go)
//	  crc32      4B  IEEE, over payload -> per-block corruption isolation
//	Index (at indexOff), blockCount entries of 12 bytes:
//	  offset 8B uint64 LE (record start), length 4B uint32 LE (whole record)
//
// Every byte above, including header, per-record length prefixes, CRCs,
// padding inside packed payloads and the index, is counted in Size().
const (
	archiveMagic   = "BPK1"
	archiveVersion = 1
	HeaderSize     = 4 + 1 + 4 + 8 + 4 + 8
	indexEntrySize = 8 + 4
	recordOverhead = 4 + 4 // length prefix + CRC
)

var errBadArchive = errors.New("bitpack: malformed archive")

type indexEntry struct {
	offset uint64
	length uint32
}

// Encode compresses vals into an archive with the given block size.
func Encode(vals []int64, blockSize int) []byte {
	if blockSize < 1 || blockSize > maxBlockCount {
		panic("bitpack: invalid block size")
	}
	n := len(vals)
	nblocks := (n + blockSize - 1) / blockSize

	out := make([]byte, HeaderSize)
	copy(out, archiveMagic)
	out[4] = archiveVersion
	binary.LittleEndian.PutUint32(out[5:], uint32(blockSize))
	binary.LittleEndian.PutUint64(out[9:], uint64(n))
	binary.LittleEndian.PutUint32(out[17:], uint32(nblocks))
	// indexOff patched after blocks are written.

	index := make([]byte, 0, nblocks*indexEntrySize)
	var fixed [8]byte
	for b := 0; b < nblocks; b++ {
		lo := b * blockSize
		hi := lo + blockSize
		if hi > n {
			hi = n
		}
		payload := encodeBlock(vals[lo:hi])
		recOff := uint64(len(out))
		binary.LittleEndian.PutUint32(fixed[:4], uint32(len(payload)))
		out = append(out, fixed[:4]...)
		out = append(out, payload...)
		binary.LittleEndian.PutUint32(fixed[:4], crc32.ChecksumIEEE(payload))
		out = append(out, fixed[:4]...)
		binary.LittleEndian.PutUint64(fixed[:8], recOff)
		index = append(index, fixed[:8]...)
		binary.LittleEndian.PutUint32(fixed[:4], uint32(4+len(payload)+4))
		index = append(index, fixed[:4]...)
	}
	binary.LittleEndian.PutUint64(out[21:], uint64(len(out)))
	out = append(out, index...)
	return out
}

// Reader gives random and range access to an encoded archive.
type Reader struct {
	data      []byte
	blockSize int
	total     int
	index     []indexEntry
}

// Open parses and validates the archive header and index.
func Open(data []byte) (*Reader, error) {
	if len(data) < HeaderSize || string(data[:4]) != archiveMagic {
		return nil, errBadArchive
	}
	if data[4] != archiveVersion {
		return nil, errBadVersion
	}
	blockSize := binary.LittleEndian.Uint32(data[5:])
	total := binary.LittleEndian.Uint64(data[9:])
	nblocks := binary.LittleEndian.Uint32(data[17:])
	indexOff := binary.LittleEndian.Uint64(data[21:])
	if blockSize == 0 || blockSize > maxBlockCount {
		return nil, errBadArchive
	}
	wantBlocks := uint64(0)
	if total > 0 {
		wantBlocks = (total + uint64(blockSize) - 1) / uint64(blockSize)
	}
	if uint64(nblocks) != wantBlocks {
		return nil, errBadArchive
	}
	if indexOff < HeaderSize || indexOff+uint64(nblocks)*indexEntrySize != uint64(len(data)) {
		return nil, errBadArchive
	}
	index := make([]indexEntry, nblocks)
	prevEnd := uint64(HeaderSize)
	for i := range index {
		off := indexOff + uint64(i)*indexEntrySize
		e := indexEntry{
			offset: binary.LittleEndian.Uint64(data[off:]),
			length: binary.LittleEndian.Uint32(data[off+8:]),
		}
		if e.offset != prevEnd || uint64(e.length) < recordOverhead || e.offset+uint64(e.length) > indexOff {
			return nil, errBadArchive
		}
		prevEnd = e.offset + uint64(e.length)
		index[i] = e
	}
	if prevEnd != indexOff {
		return nil, errBadArchive
	}
	return &Reader{data: data, blockSize: int(blockSize), total: int(total), index: index}, nil
}

// Size reports the exact on-disk size of the archive in bytes.
func (r *Reader) Size() int { return len(r.data) }

// Len returns the total number of values.
func (r *Reader) Len() int { return r.total }

// NumBlocks returns the number of blocks.
func (r *Reader) NumBlocks() int { return len(r.index) }

// BlockInfo describes one block's real on-disk layout.
type BlockInfo struct {
	Index       int
	Count       int
	Width       int
	Base        int64
	Exceptions  int
	Bitmap      bool
	PayloadSize int // payload bytes only
	RecordSize  int // length prefix + payload + CRC
	Offset      int
}

// Layout parses block headers (without decoding values) and reports
// the real encoded layout of every block.
func (r *Reader) Layout() ([]BlockInfo, error) {
	infos := make([]BlockInfo, 0, len(r.index))
	for i, e := range r.index {
		payload, err := r.blockPayload(i)
		if err != nil {
			return nil, err
		}
		c := &byteCursor{buf: payload}
		flags, _ := c.u8()
		width, _ := c.u8()
		count, _ := c.u32()
		baseU, err := c.uvarint()
		if err != nil {
			return nil, err
		}
		excCount, err := c.uvarint()
		if err != nil {
			return nil, err
		}
		infos = append(infos, BlockInfo{
			Index:       i,
			Count:       int(count),
			Width:       int(width),
			Base:        zigzagDecode(baseU),
			Exceptions:  int(excCount),
			Bitmap:      flags&flagPosBitmap != 0,
			PayloadSize: len(payload),
			RecordSize:  int(e.length),
			Offset:      int(e.offset),
		})
	}
	return infos, nil
}

func (r *Reader) blockPayload(i int) ([]byte, error) {
	e := r.index[i]
	rec := r.data[e.offset : e.offset+uint64(e.length)]
	payloadLen := binary.LittleEndian.Uint32(rec)
	if uint64(payloadLen)+recordOverhead != uint64(e.length) {
		return nil, fmt.Errorf("bitpack: block %d: %w", i, errBadArchive)
	}
	payload := rec[4 : 4+payloadLen]
	crc := binary.LittleEndian.Uint32(rec[4+payloadLen:])
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, fmt.Errorf("bitpack: block %d: CRC mismatch (corrupted)", i)
	}
	return payload, nil
}

// Block decodes and returns the values of block i, verifying its CRC.
func (r *Reader) Block(i int) ([]int64, error) {
	if i < 0 || i >= len(r.index) {
		return nil, fmt.Errorf("bitpack: block %d out of range", i)
	}
	payload, err := r.blockPayload(i)
	if err != nil {
		return nil, err
	}
	return decodeBlock(payload, uint32(r.blockSize))
}

// At returns the value at index i, decoding only the containing block.
func (r *Reader) At(i int) (int64, error) {
	if i < 0 || i >= r.total {
		return 0, fmt.Errorf("bitpack: index %d out of range", i)
	}
	vals, err := r.Block(i / r.blockSize)
	if err != nil {
		return 0, err
	}
	return vals[i%r.blockSize], nil
}

// DecodeRange decodes values in [start, end), touching only overlapping
// blocks. Corrupted blocks are skipped and reported in the failed list;
// all other blocks still decode (corruption isolation).
func (r *Reader) DecodeRange(start, end int) (vals []int64, failed []int) {
	if start < 0 {
		start = 0
	}
	if end > r.total {
		end = r.total
	}
	if start >= end {
		return nil, nil
	}
	b0, b1 := start/r.blockSize, (end-1)/r.blockSize
	for b := b0; b <= b1; b++ {
		blk, err := r.Block(b)
		if err != nil {
			failed = append(failed, b)
			continue
		}
		lo, hi := 0, len(blk)
		if b == b0 {
			lo = start - b0*r.blockSize
		}
		if b == b1 {
			hi = end - b1*r.blockSize
		}
		vals = append(vals, blk[lo:hi]...)
	}
	return vals, failed
}

// DecodeAll decodes the whole archive, failing if any block is corrupt.
func (r *Reader) DecodeAll() ([]int64, error) {
	vals, failed := r.DecodeRange(0, r.total)
	if len(failed) > 0 {
		return vals, fmt.Errorf("bitpack: corrupted blocks: %v", failed)
	}
	return vals, nil
}
