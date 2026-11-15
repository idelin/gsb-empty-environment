package netsec

import (
	"encoding/binary"
	"fmt"
)

// CertificateEntry is one entry of a TLS Certificate message.
type CertificateEntry struct {
	Certificate []byte
	Extensions  []byte
}

// CertificateMessage is the decoded body of a TLS Certificate handshake
// message. This is the byte sequence that RFC 8879 compresses.
type CertificateMessage struct {
	RequestContext []byte
	Certificates   []CertificateEntry
}

// MarshalCertificateMessage builds a Certificate message body from DER
// certificates. Certificate-specific extensions are omitted.
func MarshalCertificateMessage(requestContext []byte, certificates [][]byte) ([]byte, error) {
	entries := make([]CertificateEntry, 0, len(certificates))
	for _, certificate := range certificates {
		entries = append(entries, CertificateEntry{Certificate: certificate})
	}
	return MarshalCertificateMessageEntries(requestContext, entries)
}

// MarshalCertificateMessageEntries builds a Certificate message body with
// per-certificate extensions preserved.
func MarshalCertificateMessageEntries(requestContext []byte, entries []CertificateEntry) ([]byte, error) {
	if len(requestContext) > 0xff {
		return nil, fmt.Errorf("%w: request context too long", ErrInvalidCertificateMessage)
	}

	listLength := 0
	for _, entry := range entries {
		if len(entry.Certificate) == 0 || len(entry.Certificate) > MaxUncompressedLength {
			return nil, fmt.Errorf("%w: invalid certificate length %d", ErrInvalidCertificateMessage, len(entry.Certificate))
		}
		if len(entry.Extensions) > 0xffff {
			return nil, fmt.Errorf("%w: certificate extensions too long", ErrInvalidCertificateMessage)
		}
		listLength += 3 + len(entry.Certificate) + 2 + len(entry.Extensions)
	}
	if listLength > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: certificate list too long", ErrInvalidCertificateMessage)
	}
	bodyLength := 1 + len(requestContext) + 3 + listLength
	if bodyLength > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: certificate list too long", ErrInvalidCertificateMessage)
	}

	body := make([]byte, 0, bodyLength)
	body = append(body, byte(len(requestContext)))
	body = append(body, requestContext...)
	body = appendUint24(body, listLength)
	for _, entry := range entries {
		body = appendUint24(body, len(entry.Certificate))
		body = append(body, entry.Certificate...)
		body = append(body, byte(len(entry.Extensions)>>8), byte(len(entry.Extensions)))
		body = append(body, entry.Extensions...)
	}
	return body, nil
}

// ParseCertificateMessage decodes a Certificate message body.
func ParseCertificateMessage(body []byte) (*CertificateMessage, error) {
	if len(body) < 4 {
		return nil, fmt.Errorf("%w: message too short", ErrInvalidCertificateMessage)
	}
	contextLength := int(body[0])
	offset := 1
	if len(body) < offset+contextLength+3 {
		return nil, fmt.Errorf("%w: truncated request context", ErrInvalidCertificateMessage)
	}
	requestContext := append([]byte(nil), body[offset:offset+contextLength]...)
	offset += contextLength

	listLength, ok := readUint24(body[offset : offset+3])
	if !ok {
		return nil, fmt.Errorf("%w: invalid certificate list length", ErrInvalidCertificateMessage)
	}
	offset += 3
	if len(body)-offset != listLength {
		return nil, fmt.Errorf("%w: certificate list length %d does not match body", ErrInvalidCertificateMessage, listLength)
	}

	message := &CertificateMessage{RequestContext: requestContext}
	end := offset + listLength
	for offset < end {
		if end-offset < 3 {
			return nil, fmt.Errorf("%w: truncated certificate length", ErrInvalidCertificateMessage)
		}
		certificateLength, _ := readUint24(body[offset : offset+3])
		offset += 3
		if certificateLength == 0 || end-offset < certificateLength+2 {
			return nil, fmt.Errorf("%w: invalid certificate length %d", ErrInvalidCertificateMessage, certificateLength)
		}
		certificate := append([]byte(nil), body[offset:offset+certificateLength]...)
		offset += certificateLength

		extensionsLength := int(binary.BigEndian.Uint16(body[offset : offset+2]))
		offset += 2
		if end-offset < extensionsLength {
			return nil, fmt.Errorf("%w: truncated certificate extensions", ErrInvalidCertificateMessage)
		}
		extensions := append([]byte(nil), body[offset:offset+extensionsLength]...)
		offset += extensionsLength
		message.Certificates = append(message.Certificates, CertificateEntry{
			Certificate: certificate,
			Extensions:  extensions,
		})
	}
	return message, nil
}

func appendUint24(destination []byte, value int) []byte {
	return append(destination, byte(value>>16), byte(value>>8), byte(value))
}

func readUint24(data []byte) (int, bool) {
	if len(data) < 3 {
		return 0, false
	}
	return int(data[0])<<16 | int(data[1])<<8 | int(data[2]), true
}
