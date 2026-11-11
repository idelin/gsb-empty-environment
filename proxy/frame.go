package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
)

// HTTP/2 frame types (RFC 7540 section 6).
const (
	FrameData         uint8 = 0x0
	FrameHeaders      uint8 = 0x1
	FramePriority     uint8 = 0x2
	FrameRSTStream    uint8 = 0x3
	FrameSettings     uint8 = 0x4
	FramePushPromise  uint8 = 0x5
	FramePing         uint8 = 0x6
	FrameGoAway       uint8 = 0x7
	FrameWindowUpdate uint8 = 0x8
	FrameContinuation uint8 = 0x9
)

// Frame flags.
const (
	FlagEndStream  uint8 = 0x1
	FlagAck        uint8 = 0x1
	FlagEndHeaders uint8 = 0x4
	FlagPadded     uint8 = 0x8
	FlagPriority   uint8 = 0x20
)

// SETTINGS parameter identifiers.
const (
	SettingHeaderTableSize uint16 = 0x1
)

const frameHeaderLen = 9

// FrameHeader is the 9-octet HTTP/2 frame header.
type FrameHeader struct {
	Length   uint32
	Type     uint8
	Flags    uint8
	StreamID uint32
}

// appendFrame appends a complete frame (header + payload) to dst.
func appendFrame(dst []byte, typ, flags uint8, streamID uint32, payload []byte) []byte {
	var hdr [frameHeaderLen]byte
	hdr[0] = byte(len(payload) >> 16)
	hdr[1] = byte(len(payload) >> 8)
	hdr[2] = byte(len(payload))
	hdr[3] = typ
	hdr[4] = flags
	binary.BigEndian.PutUint32(hdr[5:], streamID&0x7fffffff)
	dst = append(dst, hdr[:]...)
	return append(dst, payload...)
}

// readFrame reads one frame, rejecting payloads larger than maxPayload.
func readFrame(r io.Reader, maxPayload uint32) (FrameHeader, []byte, error) {
	var hdr [frameHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return FrameHeader{}, nil, err
	}
	h := FrameHeader{
		Length:   uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2]),
		Type:     hdr[3],
		Flags:    hdr[4],
		StreamID: binary.BigEndian.Uint32(hdr[5:]) & 0x7fffffff,
	}
	if h.Length > maxPayload {
		return h, nil, fmt.Errorf("proxy: frame of %d bytes exceeds limit %d", h.Length, maxPayload)
	}
	payload := make([]byte, h.Length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return h, nil, err
	}
	return h, payload, nil
}
