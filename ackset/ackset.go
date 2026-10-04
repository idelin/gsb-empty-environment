// Package ackset encodes and decodes sets of uint32 sequence numbers
// (e.g. batch acknowledgements) as a sequence of segments, where each
// segment is either a continuous range or a finite bitmap. The encoder
// picks the byte-minimal segmentation for the wire format; the decoder
// rejects overlapping segments and any non-canonical form.
//
// Wire format (all integers big-endian, varints are unsigned LEB128):
//
//	empty set:     [0x00]
//	non-empty set: [0x01] seg_count(varint, >= 1) seg_1 ... seg_k
//
//	segment:
//	  range:  [0x00] start(uint32) count(varint >= 1)
//	          covers start, start+1, ..., start+count-1 (mod 2^32)
//	  bitmap: [0x01] start(uint32) nbits(varint, 2..MaxBitmapBits)
//	          data(ceil(nbits/8) bytes)
//	          bit i (byte i/8, bit i%8, LSB first) set <=> start+i in S
//
// Canonicality rules enforced by Decode:
//  1. segments appear in strictly increasing order (mod 2^32) and never
//     overlap; the total span (last covered - first start) is < MaxWindow;
//  2. adjacent segments (no missing number between them) appear only
//     when merging them would not be smaller (verified via rule 4);
//  3. a bitmap has bit 0 and bit nbits-1 set, zero unused tail bits, and
//     is never all-ones (a range is always smaller);
//  4. the encoding is byte-minimal: Decode re-encodes the decoded set and
//     requires the result to be byte-identical to the input (this also
//     rejects overlong varints and suboptimal segmentations);
//  5. no trailing bytes.
package ackset

import "errors"

const (
	// MaxWindow is the maximum span (last-first+1, mod 2^32) a set may
	// cover. It is half the sequence space so modular order stays
	// unambiguous across sequence number wraparound.
	MaxWindow = 1 << 31
	// MaxBitmapBits bounds a single bitmap segment (1 KiB of data).
	MaxBitmapBits = 1 << 13
	// MaxElements bounds the number of elements in one set.
	MaxElements = 1 << 20
)

const (
	modeEmpty    = 0x00
	modeSegments = 0x01
)

const (
	segRange  = 0x00
	segBitmap = 0x01
)

var (
	// ErrOverlap reports segments that cover the same sequence number,
	// or segments listed out of (modular) order.
	ErrOverlap = errors.New("ackset: overlapping segments")
	// ErrNotCanonical reports a valid-looking but non-minimal encoding.
	ErrNotCanonical = errors.New("ackset: non-canonical encoding")
	// ErrTruncated reports an encoding that ends prematurely.
	ErrTruncated = errors.New("ackset: truncated encoding")
	// ErrTrailing reports extra bytes after a complete encoding.
	ErrTrailing = errors.New("ackset: trailing bytes")
	// ErrMalformed reports structurally invalid encodings.
	ErrMalformed = errors.New("ackset: malformed encoding")
	// ErrTooSparse reports a set whose minimal covering window is at
	// least half the sequence space.
	ErrTooSparse = errors.New("ackset: set spans half the sequence space")
	// ErrTooLarge reports a set with more than MaxElements elements.
	ErrTooLarge = errors.New("ackset: too many elements")
)
