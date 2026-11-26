package tls13sm

import (
	"encoding/binary"
	"fmt"
)

// TLS 1.3 content type values (RFC 8446 section 5.1).
const (
	ContentTypeInvalid          uint8 = 0
	ContentTypeChangeCipherSpec uint8 = 20
	ContentTypeAlert            uint8 = 21
	ContentTypeHandshake        uint8 = 22
	ContentTypeApplicationData  uint8 = 23
)

// TLS 1.3 handshake message types (RFC 8446 section 4).
const (
	HandshakeClientHello         uint8 = 1
	HandshakeServerHello         uint8 = 2
	HandshakeNewSessionTicket    uint8 = 4
	HandshakeEndOfEarlyData      uint8 = 5
	HandshakeEncryptedExtensions uint8 = 8
	HandshakeCertificate         uint8 = 11
	HandshakeCertificateVerify   uint8 = 15
	HandshakeFinished            uint8 = 20
	HandshakeKeyUpdate           uint8 = 24
	HandshakeMessageHash         uint8 = 254 // synthetic, never sent on the wire
)

// Extension type values used by the state machine.
const (
	ExtSupportedGroups     uint16 = 10
	ExtSignatureAlgorithms uint16 = 13
	ExtKeyShare            uint16 = 51
	ExtSupportedVersions   uint16 = 43
	ExtPSKKeyExchangeModes uint16 = 45
	ExtEarlyData           uint16 = 42
	ExtCookie              uint16 = 44
	ExtPSK                 uint16 = 41
)

// TLS 1.3 legacy version and the supported_versions negotiation value.
const (
	LegacyVersionTLS12 uint16 = 0x0303
	SupportedTLS13     uint16 = 0x0304
)

// KeyUpdate request_update values (RFC 8446 section 4.6.3).
const (
	KeyUpdateNotRequested uint8 = 0
	KeyUpdateRequested    uint8 = 1
)

// TLS 1.3 downgrade sentinels, RFC 8446 section 4.1.3. A compliant
// ServerHello.random MUST NOT end in either value.
var (
	DowngradeSentinelTLS12 = []byte{0x44, 0x4F, 0x57, 0x4E, 0x47, 0x52, 0x44, 0x01}
	DowngradeSentinelTLS11 = []byte{0x44, 0x4F, 0x57, 0x4E, 0x47, 0x52, 0x44, 0x00}
)

func appendU8(b []byte, v byte) []byte {
	return append(b, v)
}

func appendU16(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendU24(b []byte, v uint32) []byte {
	return append(b, byte(v>>16), byte(v>>8), byte(v))
}

func appendU32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendLength16(b, payload []byte) []byte {
	b = appendU16(b, uint16(len(payload)))
	return append(b, payload...)
}

// reader is a bounds-checked cursor over a fixed byte slice. Every accessor
// returns an error instead of panicking on truncated input.
type reader struct {
	data []byte
	pos  int
}

func newReader(data []byte) *reader { return &reader{data: data} }

func (r *reader) remaining() int { return len(r.data) - r.pos }

func (r *reader) u8() (byte, error) {
	if r.remaining() < 1 {
		return 0, fmt.Errorf("%w: truncated u8", ErrProtocol)
	}
	v := r.data[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, fmt.Errorf("%w: truncated u16", ErrProtocol)
	}
	v := binary.BigEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) u24() (uint32, error) {
	if r.remaining() < 3 {
		return 0, fmt.Errorf("%w: truncated u24", ErrProtocol)
	}
	v := uint32(r.data[r.pos])<<16 | uint32(r.data[r.pos+1])<<8 | uint32(r.data[r.pos+2])
	r.pos += 3
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, fmt.Errorf("%w: truncated u32", ErrProtocol)
	}
	v := binary.BigEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, fmt.Errorf("%w: truncated opaque<%d>", ErrProtocol, n)
	}
	out := make([]byte, n)
	copy(out, r.data[r.pos:r.pos+n])
	r.pos += n
	return out, nil
}

func (r *reader) vector16() ([]byte, error) {
	n, err := r.u16()
	if err != nil {
		return nil, err
	}
	return r.bytes(int(n))
}

// vector16Raw returns the u16-prefixed vector including its two length bytes.
func (r *reader) vector16Raw() ([]byte, error) {
	if r.remaining() < 2 {
		return nil, fmt.Errorf("%w: truncated u16 prefix", ErrProtocol)
	}
	n := int(uint16(r.data[r.pos])<<8 | uint16(r.data[r.pos+1]))
	start := r.pos
	r.pos += 2
	if r.remaining() < n {
		return nil, fmt.Errorf("%w: truncated opaque<%d>", ErrProtocol, n)
	}
	r.pos += n
	return r.data[start:r.pos], nil
}

func (r *reader) vector8() ([]byte, error) {
	n, err := r.u8()
	if err != nil {
		return nil, err
	}
	return r.bytes(int(n))
}

// handshakeHeader is the four-byte RFC 8446 handshake header.
type handshakeHeader struct {
	Type   uint8
	Length uint32
}

// parseHandshakeHeader parses a handshake header and returns the body slice
// without copying it.
func parseHandshakeHeader(msg []byte) (handshakeHeader, []byte, error) {
	r := newReader(msg)
	t, err := r.u8()
	if err != nil {
		return handshakeHeader{}, nil, err
	}
	n, err := r.u24()
	if err != nil {
		return handshakeHeader{}, nil, err
	}
	body, err := r.bytes(int(n))
	if err != nil {
		return handshakeHeader{}, nil, fmt.Errorf("%w: handshake body shorter than declared length", ErrProtocol)
	}
	if r.remaining() != 0 {
		return handshakeHeader{}, nil, fmt.Errorf("%w: trailing bytes after handshake body", ErrProtocol)
	}
	return handshakeHeader{Type: t, Length: n}, body, nil
}

// marshalHandshake wraps a body in the four-byte handshake header.
func marshalHandshake(msgType uint8, body []byte) []byte {
	out := make([]byte, 0, 4+len(body))
	out = appendU8(out, msgType)
	out = appendU24(out, uint32(len(body)))
	out = append(out, body...)
	return out
}

// extension is a parsed TLS extension pair.
type extension struct {
	Type uint16
	Data []byte
}

// parseExtensions parses the standard extension list carried by ClientHello
// and ServerHello.
func parseExtensions(raw []byte) ([]extension, error) {
	// raw is the serialized Extension extensions<..> vector, including its
	// outer u16 length prefix.
	r := newReader(raw)
	list, err := r.vector16()
	if err != nil {
		return nil, err
	}
	r = newReader(list)
	var exts []extension
	for r.remaining() > 0 {
		t, err := r.u16()
		if err != nil {
			return nil, err
		}
		data, err := r.vector16()
		if err != nil {
			return nil, err
		}
		exts = append(exts, extension{Type: t, Data: data})
	}
	return exts, nil
}

func extensionData(exts []extension, typ uint16) ([]byte, bool) {
	for _, ext := range exts {
		if ext.Type == typ {
			return ext.Data, true
		}
	}
	return nil, false
}

// marshalExtensions serializes an extension list, including the outer u16
// length prefix.
func marshalExtensions(exts []extension) []byte {
	var list []byte
	for _, ext := range exts {
		list = appendU16(list, ext.Type)
		list = appendLength16(list, ext.Data)
	}
	return appendLength16(nil, list)
}
