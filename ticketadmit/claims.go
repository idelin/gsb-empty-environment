package ticketadmit

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"time"
)

// claimsVersion is the plaintext claims encoding version.
const claimsVersion byte = 1

// fingerprintSize is the length of a SHA-256 certificate fingerprint.
const fingerprintSize = sha256.Size

// ErrMalformedTicket is returned when a decrypted ticket does not contain a
// well-formed claims payload.
var ErrMalformedTicket = errors.New("ticketadmit: malformed ticket claims")

// Claims are the virtual-host-bound values sealed inside every ticket.
type Claims struct {
	// SNI is the server name from the ClientHello that produced the ticket.
	SNI string

	// ALPN is the negotiated application-layer protocol; empty when ALPN was
	// not negotiated.
	ALPN string

	// ClientCertFingerprint is the SHA-256 of the DER-encoded leaf client
	// certificate. It is nil (and encoded as absent) when the client
	// presented no certificate.
	ClientCertFingerprint []byte

	// CipherSuite is the TLS cipher suite identifier (for example a
	// crypto/tls CipherSuite ID) negotiated on the original handshake.
	CipherSuite uint16

	// IssuedAt is the time at which the ticket was issued.
	IssuedAt time.Time
}

// CertificateFingerprint returns the SHA-256 fingerprint of a client leaf
// certificate's DER encoding, or nil when no certificate was provided.
func CertificateFingerprint(leaf *x509.Certificate) []byte {
	if leaf == nil || len(leaf.Raw) == 0 {
		return nil
	}
	sum := sha256.Sum256(leaf.Raw)
	return sum[:]
}

// marshal encodes the claims. The format is length-prefixed so future
// versions can extend it without ambiguity:
//
//	version        uint8
//	sni            uint16-length-prefixed bytes
//	alpn           uint16-length-prefixed bytes
//	fingerprint    uint8 length (0 absent, otherwise 32) + bytes
//	cipher_suite   uint16
//	issued_at_ms   uint64 (UNIX milliseconds)
func (c Claims) marshal() ([]byte, error) {
	if len(c.SNI) > 0xffff || len(c.ALPN) > 0xffff {
		return nil, ErrMalformedTicket
	}
	if fpLen := len(c.ClientCertFingerprint); fpLen != 0 && fpLen != fingerprintSize {
		return nil, ErrMalformedTicket
	}

	out := make([]byte, 0, 1+2+len(c.SNI)+2+len(c.ALPN)+1+fingerprintSize+2+8)
	out = append(out, claimsVersion)

	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(c.SNI)))
	out = append(out, lenBuf[:]...)
	out = append(out, c.SNI...)

	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(c.ALPN)))
	out = append(out, lenBuf[:]...)
	out = append(out, c.ALPN...)

	out = append(out, byte(len(c.ClientCertFingerprint)))
	out = append(out, c.ClientCertFingerprint...)

	binary.BigEndian.PutUint16(lenBuf[:], c.CipherSuite)
	out = append(out, lenBuf[:]...)

	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(c.IssuedAt.UnixMilli()))
	out = append(out, tsBuf[:]...)
	return out, nil
}

// unmarshalClaims parses the output of Claims.marshal.
func unmarshalClaims(data []byte) (Claims, error) {
	var c Claims
	if len(data) < 1 || data[0] != claimsVersion {
		return c, ErrMalformedTicket
	}
	b := data[1:]

	var ok bool
	if c.SNI, b, ok = readLP16(b); !ok {
		return c, ErrMalformedTicket
	}
	if c.ALPN, b, ok = readLP16(b); !ok {
		return c, ErrMalformedTicket
	}
	if len(b) < 1 {
		return c, ErrMalformedTicket
	}
	fpLen := int(b[0])
	b = b[1:]
	if fpLen != 0 && fpLen != fingerprintSize || len(b) < fpLen {
		return c, ErrMalformedTicket
	}
	if fpLen > 0 {
		c.ClientCertFingerprint = append([]byte(nil), b[:fpLen]...)
		b = b[fpLen:]
	}
	if len(b) < 10 {
		return c, ErrMalformedTicket
	}
	c.CipherSuite = binary.BigEndian.Uint16(b[:2])
	issuedMillis := binary.BigEndian.Uint64(b[2:10])
	b = b[10:]
	if len(b) != 0 {
		return c, ErrMalformedTicket
	}
	c.IssuedAt = time.UnixMilli(int64(issuedMillis)).UTC()
	return c, nil
}

// readLP16 reads a uint16-length-prefixed string field.
func readLP16(b []byte) (string, []byte, bool) {
	if len(b) < 2 {
		return "", b, false
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	b = b[2:]
	if len(b) < n {
		return "", b, false
	}
	return string(b[:n]), b[n:], true
}
