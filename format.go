// Package rlecomp implements a small run-length compression format with
// two block kinds (literal and repeat), an optimal offline block splitter
// (dynamic programming over real header costs), and a bounded-buffer
// streaming encoder with explicit flush semantics.
//
// Stream format (all integers big-endian unless noted):
//
//	stream  := frame*
//	frame   := magic(2) version(1) payloadLen(uvarint) payload crc32(4)
//	payload := block*
//	block   := tag(1) body
//	  tag 0x00 literal: uvarint length, then length raw bytes
//	  tag 0x01 repeat:  uvarint count,  then 1 byte value
//
// crc32 is IEEE CRC of the payload. A frame is the flush unit: Flush
// finalizes exactly one frame (an empty one if no data is pending and no
// frame was ever emitted), so an empty frame is distinguishable from no
// frame at all (a zero-byte stream). Flush never ends the stream; the
// stream ends at EOF after Close.
package rlecomp

import "encoding/binary"

const (
	// FormatVersion is the current format version byte.
	FormatVersion = 1

	// MaxBlockLen is the maximum number of source bytes covered by one
	// block (literal length or repeat count).
	MaxBlockLen = 1 << 24

	// MaxHeaderLen is the worst-case block header size in bytes:
	// 1 tag byte + uvarint(MaxBlockLen) which needs 4 bytes.
	MaxHeaderLen = 5

	// MaxFramePayload caps the encoded payload of a single frame; the
	// encoder starts a new frame when a frame grows past this.
	MaxFramePayload = 1 << 22

	// MaxFrameLen bounds payloadLen accepted by the decoder. The encoder
	// ends a frame once its payload reaches MaxFramePayload, so a
	// legitimate frame holds at most one more maximal block on top.
	MaxFrameLen = MaxFramePayload + MaxBlockLen + MaxHeaderLen

	// frameOverhead is the worst-case per-frame overhead:
	// magic(2) + version(1) + uvarint(payloadLen)(<=5) + crc32(4).
	frameOverhead = 12
)

var magic = [2]byte{'R', 'L'}

const (
	tagLiteral = 0x00
	tagRepeat  = 0x01
)

// headerLen returns the exact encoded header size for a block covering n
// source bytes, 1 <= n <= MaxBlockLen. This is the real cost used by the
// optimal splitter.
func headerLen(n int) int {
	return 1 + uvarintLen(uint64(n))
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// seg is one chosen block: src[start:start+length] encoded either as a
// literal or as a repeat of src[start].
type seg struct {
	repeat bool
	start  int
	length int
}

// segCost is the exact encoded size of one block.
func (s seg) segCost() int {
	if s.repeat {
		return headerLen(s.length) + 1
	}
	return headerLen(s.length) + s.length
}

// appendBlock encodes one block into dst.
func appendBlock(dst, src []byte, s seg) []byte {
	if s.repeat {
		dst = append(dst, tagRepeat)
		dst = binary.AppendUvarint(dst, uint64(s.length))
		return append(dst, src[s.start])
	}
	dst = append(dst, tagLiteral)
	dst = binary.AppendUvarint(dst, uint64(s.length))
	return append(dst, src[s.start:s.start+s.length]...)
}

// appendFrame wraps payload in a frame and appends it to dst.
func appendFrame(dst, payload []byte) []byte {
	dst = append(dst, magic[0], magic[1], FormatVersion)
	dst = binary.AppendUvarint(dst, uint64(len(payload)))
	dst = append(dst, payload...)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32Of(payload))
	return append(dst, sum[:]...)
}
