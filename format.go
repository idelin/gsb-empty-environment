// Package slz implements a sliding-window LZ77 compressor with an
// optimal (minimum-bit) parser, a bounded match index, and a checksummed
// block format. It uses only the standard library.
//
// Token format (bits are packed LSB-first):
//
//	literal: 0b0              + 8 bits raw byte        -> 9 bits
//	match:   0b1              + WindowBits(dist-1)
//	                          + LenBits(len-MinMatch)  -> 1+12+6 = 19 bits
//
// File layout:
//
//	header:  magic "SLZ1" (4) | version u8 | reserved u8 |
//	         totalLen u64le | crc32le(u4, IEEE, of decoded output)
//	blocks:  rawLen u32le | bitLen u32le | flags u8 | payload ceil(bitLen/8)
//
// flags bit0 (flagResetWindow) marks a checkpoint: the decoder drops all
// dictionary history, so no match in this or later blocks may reference
// bytes produced before the checkpoint. Blocks without the flag continue
// the sliding window; distances are always bounded by WindowSize and by
// the number of bytes produced since the last checkpoint.
package slz

import "errors"

const (
	// Magic identifies the format; Version must match exactly.
	Magic   = "SLZ1"
	Version = 1

	// WindowBits bounds the match distance; WindowSize is the dictionary.
	WindowBits = 12
	WindowSize = 1 << WindowBits // 4096

	// LenBits bounds the match length.
	LenBits  = 6
	MinMatch = 3
	MaxMatch = MinMatch + (1 << LenBits) - 1 // 66

	// Bit costs used by the optimal parser.
	LiteralCost = 1 + 8                    // 9
	MatchCost   = 1 + WindowBits + LenBits // 19

	maxChain = 32 // bounded match-index search depth
	hashBits = 15
	hashSize = 1 << hashBits

	flagResetWindow = 1 << 0

	headerSize      = 4 + 1 + 1 + 8 + 4
	blockHeaderSize = 4 + 4 + 1
)

var (
	errCorrupt     = errors.New("slz: corrupt input")
	errBadVersion  = errors.New("slz: unsupported version")
	errBadDistance = errors.New("slz: match references unavailable or evicted bytes")
	errTooLarge    = errors.New("slz: decoded size exceeds output budget")
	errChecksum    = errors.New("slz: crc32 mismatch")
	errBadLength   = errors.New("slz: decoded length mismatch")
)
