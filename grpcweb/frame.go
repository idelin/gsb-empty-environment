// Package grpcweb converts between upstream HTTP/2 gRPC responses and
// gRPC-Web byte streams for a proxy.
//
// A gRPC-Web frame is a 5-byte header followed by a payload:
//
//	byte 0       frame flag: 0x00 for data, 0x80 for trailers
//	bytes 1..4   big-endian uint32 payload length
//
// The trailer frame payload is an HTTP/1.1 style header block whose lines
// are "key: value\r\n". Because upstream metadata is attacker controlled,
// every byte is validated before it is emitted or trusted. In particular a
// CRLF inside a key or value cannot create an extra trailer line, custom
// metadata is never allowed to claim the reserved "grpc-" prefix, and the
// authoritative grpc-status is taken only from real HTTP/2 trailers.
package grpcweb

import (
	"encoding/binary"
	"errors"
)

const (
	// FrameHeaderSize is the size in bytes of a gRPC-Web frame header.
	FrameHeaderSize = 5

	// FlagData marks a frame that carries an application data message.
	FlagData byte = 0x00

	// FlagTrailer marks a frame that carries the trailer block.
	FlagTrailer byte = 0x80
)

var (
	// ErrTruncatedFrame means the stream ended before the advertised frame
	// payload was fully received.
	ErrTruncatedFrame = errors.New("grpcweb: truncated frame")

	// ErrUnknownFrame means a frame flag other than FlagData or FlagTrailer
	// was encountered.
	ErrUnknownFrame = errors.New("grpcweb: unknown frame flag")

	// ErrTrailerOrder means a trailer frame was found more than once or was
	// not the final frame in the stream.
	ErrTrailerOrder = errors.New("grpcweb: trailer frame must occur at most once as the final frame")
)

// Frame is a single decoded gRPC-Web frame. Payload aliases the input
// buffer of SplitFrames and must not be modified by the caller.
type Frame struct {
	Flag    byte
	Payload []byte
}

// PutFrameHeader writes the 5-byte gRPC-Web frame header for flag and
// payload length into dst, which must have at least FrameHeaderSize bytes.
func PutFrameHeader(dst []byte, flag byte, payloadLen int) {
	dst[0] = flag
	binary.BigEndian.PutUint32(dst[1:FrameHeaderSize], uint32(payloadLen))
}

// MarshalDataFrame wraps a data message in a single FlagData frame.
func MarshalDataFrame(data []byte) []byte {
	frame := make([]byte, FrameHeaderSize+len(data))
	PutFrameHeader(frame, FlagData, len(data))
	copy(frame[FrameHeaderSize:], data)
	return frame
}

// ReadFrame decodes one frame from the head of buf. It returns the frame,
// the number of bytes consumed, and ErrTruncatedFrame when buf does not yet
// hold a complete frame.
func ReadFrame(buf []byte) (Frame, int, error) {
	if len(buf) < FrameHeaderSize {
		return Frame{}, 0, ErrTruncatedFrame
	}
	flag := buf[0]
	if flag != FlagData && flag != FlagTrailer {
		return Frame{}, 0, ErrUnknownFrame
	}
	payloadLen := int(binary.BigEndian.Uint32(buf[1:FrameHeaderSize]))
	end := FrameHeaderSize + payloadLen
	if len(buf) < end {
		return Frame{}, 0, ErrTruncatedFrame
	}
	return Frame{Flag: flag, Payload: buf[FrameHeaderSize:end]}, end, nil
}

// SplitFrames decodes every frame in buf. At most one FlagTrailer frame is
// allowed and it must be the final frame; any other ordering fails with
// ErrTrailerOrder. The returned frame payloads alias buf.
func SplitFrames(buf []byte) ([]Frame, error) {
	var frames []Frame
	consumed := 0
	for consumed < len(buf) {
		frame, n, err := ReadFrame(buf[consumed:])
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
		consumed += n
	}

	for i, frame := range frames {
		if frame.Flag == FlagTrailer && i != len(frames)-1 {
			return nil, ErrTrailerOrder
		}
	}

	return frames, nil
}
