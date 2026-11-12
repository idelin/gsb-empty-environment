package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// hpackEncoder is a minimal HPACK encoder used only by the tests. It emits
// literal-without-indexing representations (section 6.2.2) so no dynamic table
// state is shared between entries, plus optional indexed references.
type hpackEncoder struct {
	buf []byte
}

func (e *hpackEncoder) appendInt(value uint64, prefixBits uint, firstByte byte) {
	mask := byte(1<<prefixBits - 1)
	if value < uint64(mask) {
		e.buf = append(e.buf, firstByte|byte(value))
		return
	}
	e.buf = append(e.buf, firstByte|mask)
	value -= uint64(mask)
	for value >= 128 {
		e.buf = append(e.buf, byte(value%128)|0x80)
		value /= 128
	}
	e.buf = append(e.buf, byte(value))
}

func (e *hpackEncoder) appendString(s string, huffman bool) {
	if huffman {
		encoded := huffmanEncodeForTest([]byte(s))
		e.appendInt(uint64(len(encoded)), 7, 0x80)
		e.buf = append(e.buf, encoded...)
		return
	}
	e.appendInt(uint64(len(s)), 7, 0)
	e.buf = append(e.buf, s...)
}

// literal emits a literal header field without indexing (0x00 prefix).
func (e *hpackEncoder) literal(name, value string) {
	e.appendInt(0, 4, 0)
	e.appendString(name, false)
	e.appendString(value, false)
}

// indexed emits an indexed header field representation.
func (e *hpackEncoder) indexed(index uint64) {
	e.appendInt(index, 7, 0x80)
}

// indexedLiteral emits a literal whose name references a static/dynamic entry,
// without indexing.
func (e *hpackEncoder) indexedLiteral(index uint64, value string, huffman bool) {
	e.appendInt(index, 4, 0)
	e.appendString(value, huffman)
}

// tableSizeUpdate emits a dynamic table size update (section 6.3).
func (e *hpackEncoder) tableSizeUpdate(size uint64) {
	e.appendInt(size, 5, 0x20)
}

func buildFrame(typ FrameType, flags FrameFlags, stream uint32, payload []byte) []byte {
	frame := make([]byte, 9+len(payload))
	frame[0] = byte(len(payload) >> 16)
	frame[1] = byte(len(payload) >> 8)
	frame[2] = byte(len(payload))
	frame[3] = byte(typ)
	frame[4] = byte(flags)
	binary.BigEndian.PutUint32(frame[5:], stream)
	copy(frame[9:], payload)
	return frame
}

func headersFrame(stream uint32, flags FrameFlags, block []byte) []byte {
	return buildFrame(FrameHeaders, flags, stream, block)
}

func continuationFrame(stream uint32, flags FrameFlags, block []byte) []byte {
	return buildFrame(FrameContinuation, flags, stream, block)
}

func dataFrame(stream uint32, endStream bool, payload []byte) []byte {
	var flags FrameFlags
	if endStream {
		flags |= flagEndStream
	}
	return buildFrame(FrameData, flags, stream, payload)
}

func paddedDataFrame(stream uint32, endStream bool, payload, padding []byte) []byte {
	body := make([]byte, 0, 1+len(payload)+len(padding))
	body = append(body, byte(len(padding)))
	body = append(body, payload...)
	body = append(body, padding...)
	var flags FrameFlags = flagPadded
	if endStream {
		flags |= flagEndStream
	}
	return buildFrame(FrameData, flags, stream, body)
}

// runFrames feeds every frame contained in the byte stream to a new converter
// and returns it for inspection.
func runFrames(streamID uint32, frames ...[]byte) (*Converter, error) {
	var raw []byte
	for _, f := range frames {
		raw = append(raw, f...)
	}
	reader := NewFrameReader(bytes.NewReader(raw))
	conv := NewConverter(streamID, 4096)
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return conv, nil
			}
			return conv, err
		}
		if err := conv.HandleFrame(frame); err != nil {
			return conv, err
		}
	}
}
