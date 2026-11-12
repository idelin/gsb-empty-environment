package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedDatagram reports an HTTP Datagram whose QUIC varint Context
	// ID is truncated or invalid.
	ErrMalformedDatagram = errors.New("netsec: malformed HTTP Datagram")

	// ErrUnsupportedContextID reports a non-zero Context ID. RFC 9298
	// reserves Context ID 0 for plain UDP payloads; this package has no
	// registered semantics for any other context.
	ErrUnsupportedContextID = errors.New("netsec: unsupported HTTP Datagram Context ID")
)

// HTTPDatagram is a decoded RFC 9298 HTTP Datagram payload.
type HTTPDatagram struct {
	ContextID uint64
	Payload   []byte
}

// ParseHTTPDatagram decodes the Context ID varint at the start of a CONNECT-UDP
// HTTP Datagram Payload. The QUIC DATAGRAM Quarter Stream ID, when present, has
// already been consumed by the HTTP/3 stack. It returns ErrUnsupportedContextID
// for every non-zero context. The Payload aliases datagram; callers that retain
// it across reads must copy it.
func ParseHTTPDatagram(datagram []byte) (HTTPDatagram, error) {
	contextID, n, err := decodeQUICVarint(datagram)
	if err != nil {
		return HTTPDatagram{}, err
	}
	if contextID != 0 {
		return HTTPDatagram{}, fmt.Errorf("%w: %d", ErrUnsupportedContextID, contextID)
	}
	return HTTPDatagram{ContextID: contextID, Payload: datagram[n:]}, nil
}

// UDPPayload returns the UDP payload from an HTTP Datagram with Context ID 0.
// It returns ok=false for malformed datagrams and all non-zero Context IDs;
// such datagrams must be discarded, not forwarded as UDP.
func UDPPayload(datagram []byte) (payload []byte, ok bool) {
	parsed, err := ParseHTTPDatagram(datagram)
	if err != nil {
		return nil, false
	}
	return parsed.Payload, true
}

func decodeQUICVarint(b []byte) (value uint64, n int, err error) {
	if len(b) == 0 {
		return 0, 0, fmt.Errorf("%w: empty payload", ErrMalformedDatagram)
	}

	switch b[0] >> 6 {
	case 0:
		n = 1
	case 1:
		n = 2
	case 2:
		n = 4
	case 3:
		n = 8
	}
	if len(b) < n {
		return 0, 0, fmt.Errorf("%w: truncated Context ID", ErrMalformedDatagram)
	}

	var raw uint64
	for i := 0; i < n; i++ {
		raw = raw<<8 | uint64(b[i])
	}
	mask := (uint64(1) << (uint(n)*8 - 2)) - 1
	value = raw & mask
	return value, n, nil
}
