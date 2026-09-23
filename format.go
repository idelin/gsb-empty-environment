// Package dzblock implements a self-contained block compression format.
//
// Design properties:
//   - A stream is a sequence of independent blocks; decoding any block
//     requires only the shared read-only dictionary, never prior blocks.
//   - Every block header carries the SHA-256 digest of the dictionary it
//     was compressed against, so a wrong dictionary is rejected.
//   - Format version, lengths and CRC-32C checksums are explicit in the
//     headers; decoded output is bounded by configurable quotas.
//   - A corrupt block can be skipped by resynchronising on the next block
//     header (magic + header CRC), so one bad block never hides the next.
//
// Only the Go standard library is used; no external compression library
// is called. The entropy coder is a small LZ77 variant written here.
package dzblock

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Format constants.
const (
	// Version is the on-disk format version written into every header.
	Version = 1

	streamMagic = 0x31425A44 // "DZB\x01" little-endian
	blockMagic  = 0x4B421BDB // 0xDB 0x1B 'B' 'K', distinctive for resync

	// StreamHeaderSize is the fixed size of the stream header in bytes.
	StreamHeaderSize = 4 + 1 + 32 + 8 + 4 // magic, version, dictID, dictLen, crc
	// BlockHeaderSize is the fixed size of a block header in bytes.
	BlockHeaderSize = 4 + 1 + 32 + 4 + 4 + 4 + 4 // magic, version, dictID, rawLen, encLen, payloadCRC, headerCRC

	// MaxBlockRaw is the maximum uncompressed size of one block.
	MaxBlockRaw = 1 << 20 // 1 MiB
	// MaxBlockEncoded is the maximum encoded payload size of one block.
	// Worst case for the LZ encoder is 9/8 expansion plus one byte.
	MaxBlockEncoded = MaxBlockRaw + MaxBlockRaw/8 + 64
)

// Errors returned by this package.
var (
	ErrBadMagic      = errors.New("dzblock: bad magic")
	ErrBadVersion    = errors.New("dzblock: unsupported format version")
	ErrDictMismatch  = errors.New("dzblock: dictionary digest mismatch")
	ErrBlockTooLarge = errors.New("dzblock: block exceeds size limit")
	ErrCorrupt       = errors.New("dzblock: corrupt data")
	ErrOutputLimit   = errors.New("dzblock: decoded output limit exceeded")
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// DictID returns the digest that binds a block to its dictionary.
// The empty dictionary has a well-defined ID (SHA-256 of nothing),
// so encoding without a dictionary works unchanged.
func DictID(dict []byte) [32]byte { return sha256.Sum256(dict) }

// BlockHeader is the parsed, validated header of one independent block.
type BlockHeader struct {
	DictID     [32]byte // dictionary this block was compressed against
	RawLen     uint32   // uncompressed length of the block
	EncLen     uint32   // encoded payload length following the header
	PayloadCRC uint32   // CRC-32C of the encoded payload
}

// AppendStreamHeader appends a stream header for the given dictionary.
func AppendStreamHeader(dst, dict []byte) []byte {
	var hdr [StreamHeaderSize]byte
	id := DictID(dict)
	binary.LittleEndian.PutUint32(hdr[0:4], streamMagic)
	hdr[4] = Version
	copy(hdr[5:37], id[:])
	binary.LittleEndian.PutUint64(hdr[37:45], uint64(len(dict)))
	binary.LittleEndian.PutUint32(hdr[45:49], crc32.Checksum(hdr[:45], castagnoli))
	return append(dst, hdr[:]...)
}

// AppendBlock compresses block (optionally against dict) and appends one
// framed, self-describing block to dst. Blocks are independent: any block
// can later be decoded given only the dictionary.
func AppendBlock(dst, dict, block []byte) ([]byte, error) {
	if len(block) > MaxBlockRaw {
		return nil, ErrBlockTooLarge
	}
	payload := lzEncode(nil, dict, block)
	if len(payload) > MaxBlockEncoded {
		return nil, ErrBlockTooLarge
	}
	id := DictID(dict)
	var hdr [BlockHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], blockMagic)
	hdr[4] = Version
	copy(hdr[5:37], id[:])
	binary.LittleEndian.PutUint32(hdr[37:41], uint32(len(block)))
	binary.LittleEndian.PutUint32(hdr[41:45], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[45:49], crc32.Checksum(payload, castagnoli))
	binary.LittleEndian.PutUint32(hdr[49:53], crc32.Checksum(hdr[:49], castagnoli))
	dst = append(dst, hdr[:]...)
	return append(dst, payload...), nil
}

// ParseBlockHeader parses and validates a block header. It checks the
// magic, version, header CRC and the declared lengths, but not the
// dictionary ID (compare h.DictID with DictID(dict) for that).
func ParseBlockHeader(b []byte) (*BlockHeader, error) {
	if len(b) < BlockHeaderSize {
		return nil, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(b[0:4]) != blockMagic {
		return nil, ErrBadMagic
	}
	if b[4] != Version {
		return nil, ErrBadVersion
	}
	if crc32.Checksum(b[:49], castagnoli) != binary.LittleEndian.Uint32(b[49:53]) {
		return nil, ErrCorrupt
	}
	h := &BlockHeader{
		RawLen:     binary.LittleEndian.Uint32(b[37:41]),
		EncLen:     binary.LittleEndian.Uint32(b[41:45]),
		PayloadCRC: binary.LittleEndian.Uint32(b[45:49]),
	}
	copy(h.DictID[:], b[5:37])
	if h.RawLen > MaxBlockRaw || h.EncLen > MaxBlockEncoded {
		return nil, ErrBlockTooLarge
	}
	if h.RawLen == 0 && h.EncLen != 0 || h.RawLen != 0 && h.EncLen == 0 {
		return nil, ErrCorrupt
	}
	return h, nil
}

// DecodeBlock decodes one block payload against dict. The dictionary ID
// is verified, the payload checksum is verified, and the decoded output
// is bounded: it must be exactly h.RawLen bytes and at most limit bytes.
func DecodeBlock(dict []byte, h *BlockHeader, payload []byte, limit int) ([]byte, error) {
	if h.DictID != DictID(dict) {
		return nil, ErrDictMismatch
	}
	if int(h.RawLen) > limit {
		return nil, ErrOutputLimit
	}
	if uint32(len(payload)) != h.EncLen {
		return nil, ErrCorrupt
	}
	if crc32.Checksum(payload, castagnoli) != h.PayloadCRC {
		return nil, ErrCorrupt
	}
	return lzDecode(nil, dict, payload, int(h.RawLen))
}
