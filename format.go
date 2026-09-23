package secded

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// On-disk interleaved block format (all integers big-endian):
//
//	Offset  Size  Field
//	0       4     magic "SCE1"
//	4       1     format version (= FormatVersion)
//	5       1     k, data bits per block
//	6       1     interleave depth D
//	7       1     flags (reserved, 0)
//	8       4     block count B
//	12      8     payload length in bytes L
//	20      4     CRC-32/IEEE of the original payload
//	24      4     CRC-32/IEEE of header bytes 0..23
//
// The header is followed by the body: ceil(B/D) frames, each frame
// holding D blocks of n bits in bit-interleaved order. Inside frame f,
// bit position pos of block b lives at body bit offset
//
//	f*(n*D) + pos*D + b        (body bits are MSB-first within bytes)
//
// so any burst of <= D consecutive body bits damages at most one bit per
// block, which SECDED then corrects. The final frame is zero-padded to a
// full frame. Version, length and checksums are explicit; decoders must
// reject unknown versions, bad header CRC, truncated bodies, and payload
// lengths above the caller's output bound.
const (
	FormatMagic   = "SCE1"
	FormatVersion = 1
	HeaderSize    = 28
)

// Header is the parsed, validated file header.
type Header struct {
	Version    uint8
	DataBits   uint8  // k
	Depth      uint8  // interleave depth D
	BlockCount uint32 // number of SECDED blocks B
	PayloadLen uint64 // original payload bytes L
	PayloadCRC uint32 // CRC-32/IEEE of the original payload
}

// Params derives the code parameters from the header.
func (h Header) Params() (Params, error) { return NewParams(int(h.DataBits)) }

// FrameCount returns the number of interleave frames in the body.
func (h Header) FrameCount() uint64 {
	if h.Depth == 0 {
		return 0
	}
	return (uint64(h.BlockCount) + uint64(h.Depth) - 1) / uint64(h.Depth)
}

// BodyBits returns the exact body size in bits (zero-padded final frame).
func (h Header) BodyBits() (uint64, error) {
	p, err := h.Params()
	if err != nil {
		return 0, err
	}
	return h.FrameCount() * uint64(p.TotalBits) * uint64(h.Depth), nil
}

// BitOffset returns the body bit offset of codeword bit pos of block
// blockIdx — the random-access primitive of the interleaved layout.
func (h Header) BitOffset(blockIdx uint32, pos int) (uint64, error) {
	p, err := h.Params()
	if err != nil {
		return 0, err
	}
	if blockIdx >= h.BlockCount {
		return 0, fmt.Errorf("secded: block %d out of range [0,%d)", blockIdx, h.BlockCount)
	}
	if pos < 0 || pos >= p.TotalBits {
		return 0, fmt.Errorf("secded: codeword position %d out of range [0,%d)", pos, p.TotalBits)
	}
	d := uint64(h.Depth)
	frame := uint64(blockIdx) / d
	slot := uint64(blockIdx) % d
	return frame*uint64(p.TotalBits)*d + uint64(pos)*d + slot, nil
}

func (h Header) marshal() []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:4], FormatMagic)
	buf[4] = h.Version
	buf[5] = h.DataBits
	buf[6] = h.Depth
	buf[7] = 0
	binary.BigEndian.PutUint32(buf[8:12], h.BlockCount)
	binary.BigEndian.PutUint64(buf[12:20], h.PayloadLen)
	binary.BigEndian.PutUint32(buf[20:24], h.PayloadCRC)
	binary.BigEndian.PutUint32(buf[24:28], crc32.ChecksumIEEE(buf[0:24]))
	return buf
}

// UnmarshalHeader parses and validates a 28-byte header.
func UnmarshalHeader(buf []byte) (Header, error) {
	var h Header
	if len(buf) < HeaderSize {
		return h, errors.New("secded: short header")
	}
	if string(buf[0:4]) != FormatMagic {
		return h, errors.New("secded: bad magic")
	}
	if crc32.ChecksumIEEE(buf[0:24]) != binary.BigEndian.Uint32(buf[24:28]) {
		return h, errors.New("secded: header CRC mismatch")
	}
	h.Version = buf[4]
	if h.Version != FormatVersion {
		return h, fmt.Errorf("secded: unsupported format version %d", h.Version)
	}
	h.DataBits = buf[5]
	h.Depth = buf[6]
	h.BlockCount = binary.BigEndian.Uint32(buf[8:12])
	h.PayloadLen = binary.BigEndian.Uint64(buf[12:20])
	h.PayloadCRC = binary.BigEndian.Uint32(buf[20:24])
	if _, err := h.Params(); err != nil {
		return h, err
	}
	if h.Depth == 0 {
		return h, errors.New("secded: interleave depth must be >= 1")
	}
	return h, nil
}

// BitSet is a growable set of bit indices, used for erasure markers.
type BitSet struct{ words []uint64 }

// Set marks bit i.
func (b *BitSet) Set(i uint64) {
	w := i / 64
	for uint64(len(b.words)) <= w {
		b.words = append(b.words, 0)
	}
	b.words[w] |= 1 << (i % 64)
}

// Has reports whether bit i is marked.
func (b *BitSet) Has(i uint64) bool {
	w := i / 64
	return w < uint64(len(b.words)) && b.words[w]&(1<<(i%64)) != 0
}

// BitSource records the provenance of one payload bit: which block,
// frame, codeword position and body bit it was decoded from.
type BitSource struct {
	PayloadBit  uint64 `json:"payloadBit"`
	Block       uint32 `json:"block"`
	Frame       uint64 `json:"frame"`
	CodewordPos int    `json:"codewordPos"`
	BodyBit     uint64 `json:"bodyBit"`
}

// SourceMap is the full bit-provenance mapping for an encoded stream.
type SourceMap struct {
	Version       uint8       `json:"version"`
	DataBits      int         `json:"dataBits"`
	ParityBits    int         `json:"parityBits"`
	TotalBits     int         `json:"totalBits"`
	Depth         int         `json:"depth"`
	BlockCount    uint32      `json:"blockCount"`
	PayloadLen    uint64      `json:"payloadLen"`
	DataPositions []int       `json:"dataPositions"`
	Bits          []BitSource `json:"bits"`
}

// SourceMap builds the provenance mapping for every payload bit.
func (h Header) SourceMap() (SourceMap, error) {
	p, err := h.Params()
	if err != nil {
		return SourceMap{}, err
	}
	sm := SourceMap{
		Version:       h.Version,
		DataBits:      p.DataBits,
		ParityBits:    p.ParityBits,
		TotalBits:     p.TotalBits,
		Depth:         int(h.Depth),
		BlockCount:    h.BlockCount,
		PayloadLen:    h.PayloadLen,
		DataPositions: p.DataPositions(),
	}
	total := h.PayloadLen * 8
	d := uint64(h.Depth)
	for blk := uint32(0); blk < h.BlockCount; blk++ {
		for j, pos := range sm.DataPositions {
			pb := uint64(blk)*uint64(p.DataBits) + uint64(j)
			if pb >= total {
				break
			}
			off, err := h.BitOffset(blk, pos)
			if err != nil {
				return SourceMap{}, err
			}
			sm.Bits = append(sm.Bits, BitSource{
				PayloadBit:  pb,
				Block:       blk,
				Frame:       uint64(blk) / d,
				CodewordPos: pos,
				BodyBit:     off,
			})
		}
	}
	return sm, nil
}
