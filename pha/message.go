package pha

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// TLS 1.3 handshake content types (RFC 8446, Section 4).
const (
	typeCertificateRequest = 13
	typeCertificate        = 11
)

// TLS 1.3 alert descriptions (RFC 8446, Section 6).
const (
	alertUnexpectedMessage = 10
	alertIllegalParameter  = 47
	alertDecodeError       = 50
)

// extensionSignatureAlgorithms is RFC 8446, Section 4.2.3.
const extensionSignatureAlgorithms uint16 = 0x000d

// AlertError is a TLS 1.3 alert; Level 2 means fatal.
type AlertError struct {
	Level       byte
	Description byte
}

func (e *AlertError) Error() string {
	return "tls: alert level " + itoa(int(e.Level)) + " description " + itoa(int(e.Description))
}

// FatalIllegalParameter builds the alert required for context violations.
func FatalIllegalParameter() *AlertError {
	return &AlertError{Level: 2, Description: alertIllegalParameter}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func decodeErr() *AlertError { return &AlertError{Level: 2, Description: alertDecodeError} }

// FrameHandshake prepends the 4-byte TLS handshake header
// (msg_type, uint24 length) to an already-serialized handshake body.
func FrameHandshake(msgType byte, body []byte) []byte {
	out := make([]byte, 4+len(body))
	out[0] = msgType
	out[1] = byte(len(body) >> 16)
	out[2] = byte(len(body) >> 8)
	out[3] = byte(len(body))
	copy(out[4:], body)
	return out
}

// ParseHandshakeHeader splits a TLS handshake message (4-byte header + body)
// into its message type and body. Malformed framing yields alertDecodeError.
func ParseHandshakeHeader(record []byte) (msgType byte, body []byte, err error) {
	if len(record) < 4 {
		return 0, nil, decodeErr()
	}
	n := int(record[1])<<16 | int(record[2])<<8 | int(record[3])
	if n != len(record)-4 {
		return 0, nil, decodeErr()
	}
	return record[0], record[4:], nil
}

// MarshalCertificateRequest serializes a post-handshake CertificateRequest
// (RFC 8446, Section 4.3.2) carrying the non-empty certificate_request_context
// and the mandatory signature_algorithms extension.
func MarshalCertificateRequest(context []byte, signatureAlgorithms []uint16) ([]byte, error) {
	if len(context) == 0 || len(context) > 255 {
		return nil, errors.New("pha: certificate_request_context must be 1..255 bytes")
	}
	if len(signatureAlgorithms) == 0 {
		return nil, errors.New("pha: CertificateRequest requires signature_algorithms")
	}
	algVector := 2 * len(signatureAlgorithms)
	extData := 2 + algVector    // inner uint16 length + SignatureScheme list
	extBlock := 2 + 2 + extData // extensions uint16 length + type + extension_data
	body := make([]byte, 0, 1+len(context)+2+extBlock)
	body = append(body, byte(len(context)))
	body = append(body, context...)
	body = binary.BigEndian.AppendUint16(body, uint16(extBlock))
	body = binary.BigEndian.AppendUint16(body, extensionSignatureAlgorithms)
	body = binary.BigEndian.AppendUint16(body, uint16(extData))
	body = binary.BigEndian.AppendUint16(body, uint16(algVector))
	for _, alg := range signatureAlgorithms {
		body = binary.BigEndian.AppendUint16(body, alg)
	}
	return FrameHandshake(typeCertificateRequest, body), nil
}

// ParseCertificateRequest returns the certificate_request_context and the
// advertised signature algorithms of a CertificateRequest message.
func ParseCertificateRequest(record []byte) (context []byte, signatureAlgorithms []uint16, err error) {
	msgType, body, alert := ParseHandshakeHeader(record)
	if alert != nil {
		return nil, nil, alert
	}
	if msgType != typeCertificateRequest {
		return nil, nil, &AlertError{Level: 2, Description: alertUnexpectedMessage}
	}

	r := newReader(body)
	ctx, ok := r.uint8Vector()
	if !ok || len(ctx) == 0 {
		return nil, nil, decodeErr()
	}
	context = append([]byte(nil), ctx...)

	extWindow, ok := r.uint16Window()
	if !ok {
		return nil, nil, decodeErr()
	}
	er := newReader(extWindow[2:])
	for er.remaining() > 0 {
		extType, ok := er.uint16()
		if !ok {
			return nil, nil, decodeErr()
		}
		extData, ok := er.uint16Vector()
		if !ok {
			return nil, nil, decodeErr()
		}
		if extType != extensionSignatureAlgorithms {
			continue
		}
		// extData already excludes its uint16 length prefix; its first field is
		// the inner supported_signature_algorithms uint16 vector.
		dr := newReader(extData)
		algBytes, ok := dr.uint16Vector()
		if !ok || dr.remaining() != 0 || len(algBytes) == 0 || len(algBytes)%2 != 0 {
			return nil, nil, decodeErr()
		}
		ar := newReader(algBytes)
		for ar.remaining() >= 2 {
			alg, ok := ar.uint16()
			if !ok {
				return nil, nil, decodeErr()
			}
			signatureAlgorithms = append(signatureAlgorithms, alg)
		}
	}
	return context, signatureAlgorithms, nil
}

// MarshalCertificate serializes a TLS 1.3 Certificate message
// (RFC 8446, Section 4.4.2) with the given certificate_request_context and a
// single leaf certificate.
func MarshalCertificate(context, leafDER []byte) ([]byte, error) {
	if len(context) > 255 {
		return nil, errors.New("pha: certificate_request_context too long")
	}
	entryLen := 3 + len(leafDER) + 2 // cert uint24 vector + DER + empty extensions
	body := make([]byte, 0, 1+len(context)+3+entryLen)
	body = append(body, byte(len(context)))
	body = append(body, context...)
	body = append(body, byte(entryLen>>16), byte(entryLen>>8), byte(entryLen))
	body = append(body, byte(len(leafDER)>>16), byte(len(leafDER)>>8), byte(len(leafDER)))
	body = append(body, leafDER...)
	body = binary.BigEndian.AppendUint16(body, 0) // empty per-certificate extensions
	return FrameHandshake(typeCertificate, body), nil
}

// ParseCertificate returns the certificate_request_context and leaf DER of a
// Certificate message. A structurally invalid message yields alertDecodeError.
func ParseCertificate(record []byte) (context, leafDER []byte, err error) {
	msgType, body, alert := ParseHandshakeHeader(record)
	if alert != nil {
		return nil, nil, alert
	}
	if msgType != typeCertificate {
		return nil, nil, &AlertError{Level: 2, Description: alertUnexpectedMessage}
	}

	r := newReader(body)
	ctx, ok := r.uint8Vector()
	if !ok {
		return nil, nil, decodeErr()
	}
	context = append([]byte(nil), ctx...)

	certListWindow, ok := r.uint24Window()
	if !ok {
		return nil, nil, decodeErr()
	}
	certList := certListWindow[3:]

	lr := newReader(certList)
	certBytes, ok := lr.uint24Vector()
	if !ok {
		return nil, nil, decodeErr()
	}
	certExtWindow, ok := lr.uint16Window()
	if !ok || lr.remaining() != 0 || len(certExtWindow) != 2 {
		return nil, nil, decodeErr()
	}
	leafDER = append([]byte(nil), certBytes...)
	return context, leafDER, nil
}

// randomContext returns a fresh 16-byte context; 16 bytes comfortably exceeds
// the 8-byte minimum and makes context guessing/replay infeasible.
const contextBytes = 16

func randomContext() ([]byte, error) {
	ctx := make([]byte, contextBytes)
	if _, err := rand.Read(ctx); err != nil {
		return nil, err
	}
	return ctx, nil
}

// reader is a minimal length-prefixed vector parser used for TLS messages.
type reader struct {
	b []byte
}

func newReader(b []byte) *reader { return &reader{b: b} }

func (r *reader) remaining() int { return len(r.b) }

func (r *reader) uint16() (uint16, bool) {
	if len(r.b) < 2 {
		return 0, false
	}
	v := binary.BigEndian.Uint16(r.b)
	r.b = r.b[2:]
	return v, true
}

// vector reads a length-prefixed vector. When includePrefix is true the
// returned slice also covers its own length prefix, preserving exact
// on-the-wire byte counts for nested length checks.
func (r *reader) vector(prefix int, includePrefix bool) ([]byte, bool) {
	if len(r.b) < prefix {
		return nil, false
	}
	var n int
	switch prefix {
	case 1:
		n = int(r.b[0])
	case 2:
		n = int(binary.BigEndian.Uint16(r.b))
	case 3:
		n = int(r.b[0])<<16 | int(r.b[1])<<8 | int(r.b[2])
	default:
		return nil, false
	}
	if len(r.b) < prefix+n {
		return nil, false
	}
	start := prefix
	if includePrefix {
		start = 0
	}
	v := r.b[start : prefix+n]
	r.b = r.b[prefix+n:]
	return v, true
}

func (r *reader) uint8Vector() ([]byte, bool)  { return r.vector(1, false) }
func (r *reader) uint16Vector() ([]byte, bool) { return r.vector(2, false) }
func (r *reader) uint24Vector() ([]byte, bool) { return r.vector(3, false) }
func (r *reader) uint16Window() ([]byte, bool) { return r.vector(2, true) }
func (r *reader) uint24Window() ([]byte, bool) { return r.vector(3, true) }
