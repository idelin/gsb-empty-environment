package mux

import "encoding/binary"

// Frame layout on the wire:
//
//	+-----------------------------------+---
//	|  Length (24 bits)                 |  payload length, excludes header
//	+------+--------+-------------------+---
//	| Type | Flags  | Stream ID (32)    |  1 + 1 + 4 bytes
//	+------+--------+-------------------+---
//	|  Payload ...
//	+---------------------------------------
const frameHeaderLen = 9

type FrameType uint8

const (
	FrameData FrameType = iota
	FrameWindowUpdate
	FrameRstStream
)

func (t FrameType) String() string {
	switch t {
	case FrameData:
		return "DATA"
	case FrameWindowUpdate:
		return "WINDOW_UPDATE"
	case FrameRstStream:
		return "RST_STREAM"
	}
	return "UNKNOWN"
}

// FlagEndStream marks the final DATA frame of a send direction (half-close).
const FlagEndStream uint8 = 0x1

type Frame struct {
	Type     FrameType
	Flags    uint8
	StreamID uint32
	Payload  []byte
}

func encodeFrame(f *Frame) []byte {
	n := len(f.Payload)
	b := make([]byte, frameHeaderLen+n)
	b[0] = byte(n >> 16)
	b[1] = byte(n >> 8)
	b[2] = byte(n)
	b[3] = byte(f.Type)
	b[4] = f.Flags
	binary.BigEndian.PutUint32(b[5:9], f.StreamID)
	copy(b[frameHeaderLen:], f.Payload)
	return b
}

// WindowUpdatePayload builds the payload of a WINDOW_UPDATE frame: the
// sender's cumulative consumed-bytes watermark. Because the value is an
// absolute watermark rather than a delta, replaying the same frame is
// idempotent and cannot extend the peer's send window twice.
func WindowUpdatePayload(watermark uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, watermark)
	return b
}

func rstPayload(code ErrCode) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	return b
}
