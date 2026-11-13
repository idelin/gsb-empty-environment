package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// FrameTypeData is the HTTP/2 DATA frame type (RFC 7540 section 6.1).
	FrameTypeData uint8 = 0x0

	// FlagDataPadded indicates a DATA frame carrying a Pad Length field.
	FlagDataPadded uint8 = 0x8

	frameHeaderLen = 9
)

// ConnectionError reports a protocol violation that requires the
// connection to be torn down (an RFC 7540 connection error).
type ConnectionError struct {
	Reason string
}

func (e *ConnectionError) Error() string {
	return "netsec: connection error: " + e.Reason
}

// IsConnectionError reports whether err requires connection teardown.
func IsConnectionError(err error) bool {
	var ce *ConnectionError
	return errors.As(err, &ce)
}

// Frame is a decoded HTTP/2 frame. For DATA frames, Payload contains
// only the application data; padding is stripped and never delivered.
type Frame struct {
	Type     uint8
	Flags    uint8
	StreamID uint32
	Payload  []byte
}

// ParseDataFrame adjudicates a DATA frame body. The pad length byte must
// be strictly less than the remaining frame length; otherwise the length
// accounting underflows and later frame boundaries shift, so the
// connection fails. On success only the application payload is returned.
func ParseDataFrame(flags uint8, body []byte) ([]byte, error) {
	if flags&FlagDataPadded == 0 {
		return body, nil
	}
	if len(body) < 1 {
		return nil, &ConnectionError{Reason: "padded DATA frame missing pad length byte"}
	}
	padLen := int(body[0])
	remaining := len(body) - 1
	if padLen >= remaining {
		return nil, &ConnectionError{
			Reason: fmt.Sprintf("pad length %d not less than remaining frame length %d", padLen, remaining),
		}
	}
	return body[1 : len(body)-padLen], nil
}

// Decoder reads HTTP/2 frames from a connection and adjudicates them.
// Padding never counts toward ContentLength.
type Decoder struct {
	r             io.Reader
	contentLength int64
}

// NewDecoder returns a Decoder reading frames from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// ContentLength returns the number of application payload bytes
// delivered by DATA frames so far, excluding any padding.
func (d *Decoder) ContentLength() int64 {
	return d.contentLength
}

// ReadFrame reads and adjudicates the next frame. A padding violation
// returns a *ConnectionError and the connection must be closed.
func (d *Decoder) ReadFrame() (Frame, error) {
	var hdr [frameHeaderLen]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return Frame{}, err
	}
	length := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
	typ := hdr[3]
	flags := hdr[4]
	streamID := binary.BigEndian.Uint32(hdr[5:9]) & 0x7fffffff

	body := make([]byte, length)
	if _, err := io.ReadFull(d.r, body); err != nil {
		return Frame{}, err
	}

	payload := body
	if typ == FrameTypeData {
		p, err := ParseDataFrame(flags, body)
		if err != nil {
			return Frame{}, err
		}
		payload = p
		d.contentLength += int64(len(p))
	}
	return Frame{Type: typ, Flags: flags, StreamID: streamID, Payload: payload}, nil
}
