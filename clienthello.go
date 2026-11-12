package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ExtensionServerName           uint16 = 0x0000
	ExtensionEncryptedClientHello uint16 = 0xfe0d
	ExtensionECHOuterExtensions   uint16 = 0xfd00
)

const (
	ECHTypeOuter byte = 0
	ECHTypeInner byte = 1
)

var (
	ErrMalformedClientHello  = errors.New("malformed ClientHello")
	ErrMalformedEncodedInner = errors.New("malformed EncodedClientHelloInner")
	ErrNonZeroInnerPadding   = errors.New("non-zero EncodedClientHelloInner padding")
)

type Extension struct {
	Type uint16
	Data []byte
}

type ClientHello struct {
	LegacyVersion      uint16
	Random             [32]byte
	SessionID          []byte
	CipherSuites       []uint16
	CompressionMethods []byte
	Extensions         []Extension
}

func ParseClientHello(raw []byte) (*ClientHello, error) {
	body, err := clientHelloBody(raw)
	if err != nil {
		return nil, err
	}
	ch, consumed, err := parseClientHelloPrefix(body)
	if err != nil {
		return nil, err
	}
	if consumed != len(body) {
		return nil, fmt.Errorf("%w: trailing bytes", ErrMalformedClientHello)
	}
	return ch, nil
}

func (ch *ClientHello) Marshal() ([]byte, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil ClientHello", ErrMalformedClientHello)
	}
	if len(ch.SessionID) > 32 {
		return nil, fmt.Errorf("%w: legacy_session_id is too long", ErrMalformedClientHello)
	}
	if len(ch.CipherSuites) > 0xffff/2 {
		return nil, fmt.Errorf("%w: too many cipher suites", ErrMalformedClientHello)
	}
	if len(ch.CompressionMethods) > 0xff {
		return nil, fmt.Errorf("%w: too many compression methods", ErrMalformedClientHello)
	}

	var extensionBytes bytes.Buffer
	for i, ext := range ch.Extensions {
		if len(ext.Data) > 0xffff {
			return nil, fmt.Errorf("%w: extension %d is too long", ErrMalformedClientHello, i)
		}
		extensionBytes.WriteByte(byte(ext.Type >> 8))
		extensionBytes.WriteByte(byte(ext.Type))
		extensionBytes.WriteByte(byte(len(ext.Data) >> 8))
		extensionBytes.WriteByte(byte(len(ext.Data)))
		extensionBytes.Write(ext.Data)
	}
	if extensionBytes.Len() > 0xffff {
		return nil, fmt.Errorf("%w: extensions are too long", ErrMalformedClientHello)
	}

	var out bytes.Buffer
	out.WriteByte(byte(ch.LegacyVersion >> 8))
	out.WriteByte(byte(ch.LegacyVersion))
	out.Write(ch.Random[:])
	out.WriteByte(byte(len(ch.SessionID)))
	out.Write(ch.SessionID)
	out.WriteByte(byte((len(ch.CipherSuites) * 2) >> 8))
	out.WriteByte(byte(len(ch.CipherSuites) * 2))
	for _, suite := range ch.CipherSuites {
		out.WriteByte(byte(suite >> 8))
		out.WriteByte(byte(suite))
	}
	out.WriteByte(byte(len(ch.CompressionMethods)))
	out.Write(ch.CompressionMethods)
	out.WriteByte(byte(extensionBytes.Len() >> 8))
	out.WriteByte(byte(extensionBytes.Len()))
	out.Write(extensionBytes.Bytes())
	return out.Bytes(), nil
}

func (ch *ClientHello) Clone() *ClientHello {
	if ch == nil {
		return nil
	}
	clone := *ch
	clone.SessionID = append([]byte(nil), ch.SessionID...)
	clone.CipherSuites = append([]uint16(nil), ch.CipherSuites...)
	clone.CompressionMethods = append([]byte(nil), ch.CompressionMethods...)
	clone.Extensions = cloneExtensions(ch.Extensions)
	return &clone
}

func (ch *ClientHello) Extension(typ uint16) ([]byte, bool) {
	if ch == nil {
		return nil, false
	}
	for _, ext := range ch.Extensions {
		if ext.Type == typ {
			return ext.Data, true
		}
	}
	return nil, false
}

func (ch *ClientHello) ExtensionCount(typ uint16) int {
	if ch == nil {
		return 0
	}
	count := 0
	for _, ext := range ch.Extensions {
		if ext.Type == typ {
			count++
		}
	}
	return count
}

func (ch *ClientHello) ServerName() (string, error) {
	if ch == nil {
		return "", fmt.Errorf("%w: nil ClientHello", ErrMalformedClientHello)
	}
	count := ch.ExtensionCount(ExtensionServerName)
	if count == 0 {
		return "", nil
	}
	if count != 1 {
		return "", fmt.Errorf("%w: duplicate server_name extension", ErrMalformedClientHello)
	}
	data, _ := ch.Extension(ExtensionServerName)
	if len(data) < 2 {
		return "", fmt.Errorf("%w: truncated server_name extension", ErrMalformedClientHello)
	}
	listLength := int(binary.BigEndian.Uint16(data[:2]))
	if listLength != len(data)-2 {
		return "", fmt.Errorf("%w: bad server_name list length", ErrMalformedClientHello)
	}

	var hostName string
	for pos := 2; pos < len(data); {
		if pos+3 > len(data) {
			return "", fmt.Errorf("%w: truncated server_name entry", ErrMalformedClientHello)
		}
		nameType := data[pos]
		nameLength := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		pos += 3
		if pos+nameLength > len(data) {
			return "", fmt.Errorf("%w: truncated server_name value", ErrMalformedClientHello)
		}
		if nameType == 0 {
			if hostName != "" {
				return "", fmt.Errorf("%w: duplicate host_name", ErrMalformedClientHello)
			}
			if nameLength == 0 {
				return "", fmt.Errorf("%w: empty host_name", ErrMalformedClientHello)
			}
			hostName = string(data[pos : pos+nameLength])
		}
		pos += nameLength
	}
	return hostName, nil
}

func clientHelloBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedClientHello)
	}
	if len(raw) >= 4 && raw[0] == 1 {
		length := int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
		if length != len(raw)-4 {
			return nil, fmt.Errorf("%w: bad handshake message length", ErrMalformedClientHello)
		}
		return raw[4:], nil
	}
	return raw, nil
}

func parseClientHelloPrefix(data []byte) (*ClientHello, int, error) {
	if len(data) < 2+32+1 {
		return nil, 0, fmt.Errorf("%w: truncated ClientHello", ErrMalformedClientHello)
	}
	ch := &ClientHello{LegacyVersion: binary.BigEndian.Uint16(data[:2])}
	copy(ch.Random[:], data[2:34])
	pos := 34

	sessionIDLength := int(data[pos])
	pos++
	if sessionIDLength > 32 {
		return nil, 0, fmt.Errorf("%w: legacy_session_id is too long", ErrMalformedClientHello)
	}
	if pos+sessionIDLength > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated legacy_session_id", ErrMalformedClientHello)
	}
	ch.SessionID = append([]byte(nil), data[pos:pos+sessionIDLength]...)
	pos += sessionIDLength

	if pos+2 > len(data) {
		return nil, 0, fmt.Errorf("%w: missing cipher_suites length", ErrMalformedClientHello)
	}
	cipherSuitesLength := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if cipherSuitesLength%2 != 0 {
		return nil, 0, fmt.Errorf("%w: odd cipher_suites length", ErrMalformedClientHello)
	}
	if pos+cipherSuitesLength > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated cipher_suites", ErrMalformedClientHello)
	}
	for end := pos + cipherSuitesLength; pos < end; pos += 2 {
		ch.CipherSuites = append(ch.CipherSuites, binary.BigEndian.Uint16(data[pos:pos+2]))
	}

	if pos >= len(data) {
		return nil, 0, fmt.Errorf("%w: missing compression_methods", ErrMalformedClientHello)
	}
	compressionLength := int(data[pos])
	pos++
	if pos+compressionLength > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated compression_methods", ErrMalformedClientHello)
	}
	ch.CompressionMethods = append([]byte(nil), data[pos:pos+compressionLength]...)
	pos += compressionLength

	if pos == len(data) {
		return ch, pos, nil
	}
	if pos+2 > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated extensions length", ErrMalformedClientHello)
	}
	extensionsLength := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	end := pos + extensionsLength
	if end > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated extensions", ErrMalformedClientHello)
	}
	for pos < end {
		if pos+4 > end {
			return nil, 0, fmt.Errorf("%w: truncated extension header", ErrMalformedClientHello)
		}
		typ := binary.BigEndian.Uint16(data[pos : pos+2])
		length := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if pos+length > end {
			return nil, 0, fmt.Errorf("%w: truncated extension body", ErrMalformedClientHello)
		}
		ch.Extensions = append(ch.Extensions, Extension{
			Type: typ,
			Data: append([]byte(nil), data[pos:pos+length]...),
		})
		pos += length
	}
	return ch, end, nil
}

func cloneExtensions(extensions []Extension) []Extension {
	clone := make([]Extension, len(extensions))
	for i, ext := range extensions {
		clone[i] = Extension{
			Type: ext.Type,
			Data: append([]byte(nil), ext.Data...),
		}
	}
	return clone
}
