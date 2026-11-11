// Package hellopeek peeks at the TLS ClientHello on a connection without
// terminating TLS. It buffers the exact wire bytes needed to locate the
// ClientHello, extracts SNI and ALPN, and hands every buffered byte back to
// the forwarding path unchanged through a replay-enabled connection wrapper.
package hellopeek

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	contentTypeHandshake byte = 0x16

	handshakeClientHello byte = 1

	// maxRecordSize is the maximum TLSPlaintext.fragment length (RFC 5246).
	maxRecordSize = 1 << 14
	// maxClientHelloSize bounds the buffered ClientHello to avoid unbounded
	// memory use when a peer declares a huge handshake length.
	maxClientHelloSize = 1 << 20
)

var (
	// ErrNotTLS is returned when the first record on the connection is not a
	// TLS handshake record.
	ErrNotTLS = errors.New("hellopeek: first record is not a TLS handshake record")
	// ErrNonHandshakeRecord is returned when a record with a content type
	// other than handshake (22) appears before the ClientHello is complete.
	ErrNonHandshakeRecord = errors.New("hellopeek: non-handshake record inserted before ClientHello completion")
	// ErrMalformedClientHello is returned for any structural or length
	// inconsistency in the record layer, handshake header or ClientHello.
	ErrMalformedClientHello = errors.New("hellopeek: malformed ClientHello")
	// ErrDuplicateExtension is returned when the same extension type appears
	// more than once in the ClientHello extensions block.
	ErrDuplicateExtension = errors.New("hellopeek: duplicate extension")
	// ErrClientHelloTooLarge is returned when the ClientHello exceeds
	// maxClientHelloSize.
	ErrClientHelloTooLarge = errors.New("hellopeek: ClientHello exceeds size limit")
)

var errTruncated = malformed("truncated ClientHello")

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedClientHello, fmt.Sprintf(format, args...))
}

// Info holds the values extracted from the ClientHello.
type Info struct {
	// ServerName is the host_name from the server_name extension, or empty.
	ServerName string
	// ALPNProtocols are the protocols from the ALPN extension, in order.
	ALPNProtocols []string
}

// Conn wraps a net.Conn whose first bytes have already been consumed by
// PeekClientHello. Read replays the buffered prefix byte-for-byte first and
// then transparently reads from the underlying connection, so the forwarding
// target sees the exact byte stream the client sent.
type Conn struct {
	net.Conn

	prefix []byte
	pos    int
}

// Read returns buffered peeked bytes first, then delegates to the wrapped
// connection once the prefix has been fully replayed.
func (c *Conn) Read(p []byte) (int, error) {
	if c.pos < len(c.prefix) {
		n := copy(p, c.prefix[c.pos:])
		c.pos += n
		return n, nil
	}
	return c.Conn.Read(p)
}

// Buffered reports how many peeked bytes have not yet been replayed.
func (c *Conn) Buffered() int {
	return len(c.prefix) - c.pos
}

// PeekClientHello reads TLS records from conn until the complete ClientHello
// handshake message has been received, parses SNI and ALPN out of it, and
// returns a *Conn that replays every consumed wire byte before reading any
// further bytes from conn. The TLS session itself is never terminated.
func PeekClientHello(conn net.Conn) (*Info, *Conn, error) {
	info, raw, err := readClientHello(conn)
	if err != nil {
		return nil, nil, err
	}
	return info, &Conn{Conn: conn, prefix: raw}, nil
}

// readClientHello assembles the ClientHello across any number of TLS records.
// raw accumulates every byte read from r (record headers included) so the
// caller can replay the exact wire stream; handshake accumulates only the
// record fragments, which concatenate to handshake message bytes.
func readClientHello(r io.Reader) (*Info, []byte, error) {
	var raw []byte
	var handshake []byte

	// need is the total handshake message size (4-byte header + body) and
	// stays unknown until the first fragment reveals the handshake header.
	need := -1

	for need == -1 || len(handshake) < need {
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, nil, fmt.Errorf("hellopeek: reading record header: %w", err)
		}

		contentType := hdr[0]
		recordLen := int(binary.BigEndian.Uint16(hdr[3:5]))

		// Every record up to ClientHello completion must be a handshake
		// record: a ChangeCipherSpec, alert or application data record in
		// between is a protocol violation.
		if len(raw) == 0 {
			if contentType != contentTypeHandshake {
				return nil, nil, ErrNotTLS
			}
		} else if contentType != contentTypeHandshake {
			return nil, nil, fmt.Errorf("%w: content type %d", ErrNonHandshakeRecord, contentType)
		}

		// Require SSL 3.0 / TLS major version 3; the minor version is
		// negotiated separately inside the ClientHello.
		if hdr[1] != 3 {
			return nil, nil, malformed("unsupported record layer version %d.%d", hdr[1], hdr[2])
		}
		// RFC 5246: zero-length fragments of handshake records MUST NOT be
		// sent, and fragments MUST NOT exceed 2^14 bytes.
		if recordLen == 0 {
			return nil, nil, malformed("zero-length TLS record")
		}
		if recordLen > maxRecordSize {
			return nil, nil, malformed("TLS record length %d exceeds %d", recordLen, maxRecordSize)
		}

		raw = append(raw, hdr[:]...)

		fragment := make([]byte, recordLen)
		if _, err := io.ReadFull(r, fragment); err != nil {
			return nil, nil, fmt.Errorf("hellopeek: reading record fragment: %w", err)
		}
		raw = append(raw, fragment...)
		handshake = append(handshake, fragment...)

		if need == -1 && len(handshake) >= 4 {
			if handshake[0] != handshakeClientHello {
				return nil, nil, malformed("first handshake message has type %d, want ClientHello(1)", handshake[0])
			}
			bodyLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
			if bodyLen == 0 {
				return nil, nil, malformed("zero-length ClientHello")
			}
			need = 4 + bodyLen
			if need > maxClientHelloSize {
				return nil, nil, fmt.Errorf("%w: declared size %d bytes", ErrClientHelloTooLarge, need)
			}
		}
	}

	info, err := parseClientHello(handshake[4:need])
	if err != nil {
		return nil, nil, err
	}
	return info, raw, nil
}
