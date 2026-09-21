// Package frameq implements a message scheduler that packs fragments of
// pending messages into size-limited frames, plus a streaming parser that
// decodes those frames from arbitrarily chunked byte input.
package frameq

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Wire layout of a frame:
//
//	| magic(2) | bodyLen(4) | body: fragments... | crc32(4) |
//
// The crc covers magic+bodyLen+body. FrameHeaderSize counts toward the
// frame size limit, so a frame never exceeds MaxFrame bytes on the wire.
//
// Wire layout of a fragment inside the body:
//
//	| msgID(4) | offset(4) | total(4) | flags(1) | dataLen(2) | data... |
const (
	MagicValue uint16 = 0xFA5C

	// FrameHeaderSize is magic + bodyLen + crc32.
	FrameHeaderSize = 2 + 4 + 4
	// FragHeaderSize is msgID + offset + total + flags + dataLen.
	FragHeaderSize = 4 + 4 + 4 + 1 + 2
)

const flagLast byte = 1 << 0

var (
	// ErrIncomplete means the buffer does not yet hold a full frame;
	// feeding more bytes may succeed. It is not corruption.
	ErrIncomplete = errors.New("frameq: incomplete data, need more bytes")
	// ErrBadMagic means the frame magic is wrong (corrupt data).
	ErrBadMagic = errors.New("frameq: bad magic")
	// ErrFrameTooBig means the declared frame length exceeds the
	// configured limit. Checked before any allocation happens.
	ErrFrameTooBig = errors.New("frameq: declared frame length exceeds limit")
	// ErrBadCRC means the checksum does not match (corrupt data).
	ErrBadCRC = errors.New("frameq: crc mismatch")
	// ErrBadFragment means the fragment area is malformed (corrupt data).
	ErrBadFragment = errors.New("frameq: malformed fragment")
)

// Fragment is one chunk of a message carried inside a frame.
type Fragment struct {
	MsgID  uint32
	Offset uint32 // byte offset of Data within the message
	Total  uint32 // total message length
	Last   bool   // true on the final fragment of the message
	Data   []byte
}

// Frame is a set of fragments that together fit within the frame limit.
type Frame struct {
	Fragments []Fragment
}

// Len returns the encoded size of the frame in bytes, header included.
func (f *Frame) Len() int {
	n := FrameHeaderSize
	for _, fr := range f.Fragments {
		n += FragHeaderSize + len(fr.Data)
	}
	return n
}

// Encode serializes the frame. The result is exactly f.Len() bytes.
func (f *Frame) Encode() []byte {
	bodyLen := f.Len() - FrameHeaderSize
	buf := make([]byte, f.Len())
	binary.BigEndian.PutUint16(buf[0:2], MagicValue)
	binary.BigEndian.PutUint32(buf[2:6], uint32(bodyLen))
	off := 6
	for _, fr := range f.Fragments {
		binary.BigEndian.PutUint32(buf[off:], fr.MsgID)
		binary.BigEndian.PutUint32(buf[off+4:], fr.Offset)
		binary.BigEndian.PutUint32(buf[off+8:], fr.Total)
		if fr.Last {
			buf[off+12] = flagLast
		}
		binary.BigEndian.PutUint16(buf[off+13:], uint16(len(fr.Data)))
		copy(buf[off+FragHeaderSize:], fr.Data)
		off += FragHeaderSize + len(fr.Data)
	}
	binary.BigEndian.PutUint32(buf[6+bodyLen:], crc32.ChecksumIEEE(buf[:6+bodyLen]))
	return buf
}

// ParseFrame decodes one frame from the head of buf. It returns the frame
// and the number of bytes consumed. maxFrame is the length quota: the
// declared frame length is validated against it before any allocation.
//
// Errors: ErrIncomplete (not enough bytes yet, retry with more data),
// or a corruption error (ErrBadMagic, ErrFrameTooBig, ErrBadCRC,
// ErrBadFragment) meaning the stream is invalid at this point.
func ParseFrame(buf []byte, maxFrame int) (*Frame, int, error) {
	if len(buf) < 6 {
		return nil, 0, ErrIncomplete
	}
	if binary.BigEndian.Uint16(buf[0:2]) != MagicValue {
		return nil, 0, ErrBadMagic
	}
	bodyLen := binary.BigEndian.Uint32(buf[2:6])
	if bodyLen > uint32(maxFrame-FrameHeaderSize) {
		// Length quota is enforced before allocating anything.
		return nil, 0, fmt.Errorf("%w: %d > %d", ErrFrameTooBig,
			FrameHeaderSize+int(bodyLen), maxFrame)
	}
	total := FrameHeaderSize + int(bodyLen)
	if len(buf) < total {
		return nil, 0, ErrIncomplete
	}
	if crc32.ChecksumIEEE(buf[:6+int(bodyLen)]) != binary.BigEndian.Uint32(buf[6+int(bodyLen):total]) {
		return nil, 0, ErrBadCRC
	}
	body := buf[6 : 6+bodyLen]
	frags := make([]Fragment, 0, 4)
	for off := 0; off < len(body); {
		if len(body)-off < FragHeaderSize {
			return nil, 0, ErrBadFragment
		}
		dataLen := int(binary.BigEndian.Uint16(body[off+13 : off+15]))
		if FragHeaderSize+dataLen > len(body)-off {
			return nil, 0, ErrBadFragment
		}
		data := make([]byte, dataLen)
		copy(data, body[off+FragHeaderSize:off+FragHeaderSize+dataLen])
		frags = append(frags, Fragment{
			MsgID:  binary.BigEndian.Uint32(body[off:]),
			Offset: binary.BigEndian.Uint32(body[off+4:]),
			Total:  binary.BigEndian.Uint32(body[off+8:]),
			Last:   body[off+12]&flagLast != 0,
			Data:   data,
		})
		off += FragHeaderSize + dataLen
	}
	return &Frame{Fragments: frags}, total, nil
}
