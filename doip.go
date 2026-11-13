package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// HeaderSize is the fixed size of a DoIP message header.
	HeaderSize = 8

	PayloadTypeRoutingActivationRequest  uint16 = 0x0005
	PayloadTypeRoutingActivationResponse uint16 = 0x0006
	PayloadTypeDiagnosticMessage         uint16 = 0x8001

	ActivationTypeDefault byte = 0x00
	ActivationTypeWWHOBD  byte = 0x01

	// Routing activation response codes from ISO 13400-2.
	RoutingActivationSuccess               byte = 0x00
	RoutingActivationUnknownSourceAddress  byte = 0x01
	RoutingActivationAllConnectionsInUse   byte = 0x02
	RoutingActivationSourceAlreadyActive   byte = 0x03
	RoutingActivationInvalidActivationType byte = 0x07
)

var (
	// ErrMalformedFrame indicates that a frame is incomplete or has an
	// inconsistent declared length.
	ErrMalformedFrame = errors.New("doip: malformed frame")

	// ErrVersionInverseMismatch indicates that the protocol version and its
	// inverse byte are not bitwise complements.
	ErrVersionInverseMismatch = errors.New("doip: protocol version and inverse mismatch")

	// ErrPayloadTooLarge indicates that the declared payload exceeds the
	// caller-provided maximum.
	ErrPayloadTooLarge = errors.New("doip: payload exceeds maximum")
)

// Header is the fixed eight-byte DoIP message header.
type Header struct {
	Version        byte
	InverseVersion byte
	PayloadType    uint16
	PayloadLength  uint32
}

// ValidVersionInverse reports whether Version and InverseVersion are bitwise
// complements of each other.
func (h Header) ValidVersionInverse() bool {
	return h.Version^h.InverseVersion == 0xff
}

// ParseHeader parses and validates an eight-byte DoIP header.
func ParseHeader(data []byte) (Header, error) {
	if len(data) < HeaderSize {
		return Header{}, fmt.Errorf("%w: header is %d bytes, need %d", ErrMalformedFrame, len(data), HeaderSize)
	}

	h := Header{
		Version:        data[0],
		InverseVersion: data[1],
		PayloadType:    binary.BigEndian.Uint16(data[2:4]),
		PayloadLength:  binary.BigEndian.Uint32(data[4:8]),
	}
	if !h.ValidVersionInverse() {
		return Header{}, fmt.Errorf(
			"%w: version 0x%02x and inverse 0x%02x",
			ErrVersionInverseMismatch,
			h.Version,
			h.InverseVersion,
		)
	}
	return h, nil
}

// ParseFrame parses exactly one complete DoIP frame. The declared payload
// length is checked against maxPayload before it is trusted.
func ParseFrame(frame []byte, maxPayload uint32) (Header, []byte, error) {
	h, err := ParseHeader(frame)
	if err != nil {
		return Header{}, nil, err
	}
	if h.PayloadLength > maxPayload {
		return Header{}, nil, fmt.Errorf(
			"%w: declared %d, maximum %d",
			ErrPayloadTooLarge,
			h.PayloadLength,
			maxPayload,
		)
	}

	want := int64(HeaderSize) + int64(h.PayloadLength)
	if int64(len(frame)) != want {
		return Header{}, nil, fmt.Errorf(
			"%w: header declares %d total bytes, got %d",
			ErrMalformedFrame,
			want,
			len(frame),
		)
	}
	return h, frame[HeaderSize:], nil
}

// ReadFrame reads one complete frame from a TCP or byte stream. It validates
// the header and rejects an oversized payload before allocating or reading the
// payload body.
func ReadFrame(r io.Reader, maxPayload uint32) (Header, []byte, error) {
	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return Header{}, nil, err
	}

	h, err := ParseHeader(headerBytes)
	if err != nil {
		return Header{}, nil, err
	}
	if h.PayloadLength > maxPayload {
		return Header{}, nil, fmt.Errorf(
			"%w: declared %d, maximum %d",
			ErrPayloadTooLarge,
			h.PayloadLength,
			maxPayload,
		)
	}

	payload := make([]byte, int(h.PayloadLength))
	if _, err := io.ReadFull(r, payload); err != nil {
		return Header{}, nil, err
	}
	return h, payload, nil
}

func encodeFrame(version byte, payloadType uint16, payload []byte) []byte {
	frame := make([]byte, HeaderSize+len(payload))
	frame[0] = version
	frame[1] = ^version
	binary.BigEndian.PutUint16(frame[2:4], payloadType)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[HeaderSize:], payload)
	return frame
}
