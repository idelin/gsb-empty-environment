// Package utf8chunk splits a byte stream into chunks of at most a given
// number of bytes, cutting only on UTF-8 character boundaries.
//
// Invalid bytes are each replaced by U+FFFD in the output, while chunk
// provenance (SrcOffset/SrcLen) always refers to the original source
// positions. A multi-byte character that would straddle the budget
// boundary is moved whole into the next chunk; it is never truncated
// and never mistaken for bad bytes. A single character whose output
// size alone exceeds the budget is reported as an OversizeRuneError.
package utf8chunk

import (
	"fmt"
	"unicode/utf8"
)

// replacement is the UTF-8 encoding of U+FFFD.
var replacement = []byte("�")

// Chunk is one segment of output together with its provenance in the
// original source.
type Chunk struct {
	Data      []byte // chunk payload, always valid UTF-8, len(Data) <= limit
	SrcOffset int    // offset in the source of the first byte this chunk covers
	SrcLen    int    // number of source bytes this chunk covers
}

// SrcEnd returns the source offset one past the last byte covered.
func (c Chunk) SrcEnd() int { return c.SrcOffset + c.SrcLen }

// OversizeRuneError reports a single character whose output encoding is
// larger than the chunk budget, so it can never fit in any chunk.
type OversizeRuneError struct {
	SrcOffset int // source offset of the offending character
	Size      int // output size of the character in bytes
	Limit     int // the chunk budget it exceeds
}

func (e *OversizeRuneError) Error() string {
	return fmt.Sprintf("utf8chunk: character at source offset %d encodes to %d bytes, exceeds chunk limit %d",
		e.SrcOffset, e.Size, e.Limit)
}

// Segment splits src into chunks of at most limit output bytes, cutting
// only on UTF-8 character boundaries.
//
// Each invalid source byte is emitted as U+FFFD (3 output bytes) but
// consumes exactly 1 byte of source provenance. Chunks are non-empty,
// cover the source contiguously and in order. If a single character's
// output size exceeds limit, Segment returns an *OversizeRuneError.
func Segment(src []byte, limit int) ([]Chunk, error) {
	if limit < 1 {
		return nil, fmt.Errorf("utf8chunk: chunk limit must be >= 1, got %d", limit)
	}

	var chunks []Chunk
	var buf []byte
	start, covered := 0, 0

	flush := func() {
		if len(buf) == 0 {
			return
		}
		chunks = append(chunks, Chunk{Data: buf, SrcOffset: start, SrcLen: covered})
		buf = nil
		covered = 0
	}

	pos := 0
	for pos < len(src) {
		r, size := utf8.DecodeRune(src[pos:])
		var out []byte
		if r == utf8.RuneError && size == 1 {
			// Invalid source byte: replace, but keep 1-byte provenance.
			out = replacement
		} else {
			out = src[pos : pos+size]
		}

		if len(out) > limit {
			return nil, &OversizeRuneError{SrcOffset: pos, Size: len(out), Limit: limit}
		}

		if len(buf)+len(out) > limit {
			// The whole character moves to the next chunk; its bytes
			// are never split, so a straddling sequence at the chunk
			// tail is never misread as invalid bytes.
			flush()
			start = pos
		}
		if len(buf) == 0 {
			start = pos
		}

		buf = append(buf, out...)
		covered += size
		pos += size
	}
	flush()
	return chunks, nil
}
