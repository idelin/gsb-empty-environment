package hellopeek

import (
	"encoding/binary"
	"fmt"
)

const (
	extensionServerName uint16 = 0
	extensionALPN       uint16 = 16

	hostNameType byte = 0
)

// cursor is a strict, bounds-checked big-endian reader. Every read that would
// run past the end of the buffer fails instead of silently truncating data.
type cursor struct {
	b   []byte
	pos int
}

func (c *cursor) u8() (byte, error) {
	if c.pos+1 > len(c.b) {
		return 0, errTruncated
	}
	v := c.b[c.pos]
	c.pos++
	return v, nil
}

func (c *cursor) u16() (uint16, error) {
	if c.pos+2 > len(c.b) {
		return 0, errTruncated
	}
	v := binary.BigEndian.Uint16(c.b[c.pos:])
	c.pos += 2
	return v, nil
}

func (c *cursor) take(n int) ([]byte, error) {
	if n < 0 || c.pos+n > len(c.b) {
		return nil, errTruncated
	}
	v := c.b[c.pos : c.pos+n]
	c.pos += n
	return v, nil
}

func (c *cursor) vec8() ([]byte, error) {
	n, err := c.u8()
	if err != nil {
		return nil, err
	}
	return c.take(int(n))
}

func (c *cursor) vec16() ([]byte, error) {
	n, err := c.u16()
	if err != nil {
		return nil, err
	}
	return c.take(int(n))
}

// parseClientHello parses the body of a single ClientHello handshake message
// (RFC 5246 Section 7.4.1.2, RFC 8446 Section 4.1.2). The body must be
// consumed exactly: every declared length has to match the bytes present and
// every extension type may appear at most once.
func parseClientHello(body []byte) (*Info, error) {
	c := &cursor{b: body}

	if _, err := c.take(2); err != nil { // client_version
		return nil, err
	}
	if _, err := c.take(32); err != nil { // random
		return nil, err
	}
	sessionID, err := c.vec8()
	if err != nil {
		return nil, err
	}
	if len(sessionID) > 32 {
		return nil, malformed("session_id length %d exceeds 32 bytes", len(sessionID))
	}
	cipherSuites, err := c.vec16()
	if err != nil {
		return nil, err
	}
	if len(cipherSuites) < 2 || len(cipherSuites)%2 != 0 {
		return nil, malformed("invalid cipher_suites length %d", len(cipherSuites))
	}
	compressionMethods, err := c.vec8()
	if err != nil {
		return nil, err
	}
	if len(compressionMethods) < 1 {
		return nil, malformed("compression_methods is empty")
	}

	info := &Info{}

	// A TLS 1.2 ClientHello may omit the extensions block entirely.
	if c.pos == len(body) {
		return info, nil
	}

	extBlock, err := c.vec16()
	if err != nil {
		return nil, err
	}
	if c.pos != len(body) {
		return nil, malformed("extensions block length does not match ClientHello body")
	}

	ec := &cursor{b: extBlock}
	seen := make(map[uint16]struct{})
	for ec.pos < len(ec.b) {
		extType, err := ec.u16()
		if err != nil {
			return nil, err
		}
		extData, err := ec.vec16()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[extType]; dup {
			return nil, fmt.Errorf("%w: extension type %d", ErrDuplicateExtension, extType)
		}
		seen[extType] = struct{}{}

		switch extType {
		case extensionServerName:
			host, err := parseServerName(extData)
			if err != nil {
				return nil, err
			}
			info.ServerName = host
		case extensionALPN:
			protos, err := parseALPN(extData)
			if err != nil {
				return nil, err
			}
			info.ALPNProtocols = protos
		}
	}

	return info, nil
}

// parseServerName parses the server_name extension (RFC 6066 Section 3).
func parseServerName(data []byte) (string, error) {
	c := &cursor{b: data}

	// The extension_data of server_name is exactly the ServerNameList:
	// a u16 length followed by the Name entries. The outer extension length
	// has already been consumed by the generic extension loop.
	listLen, err := c.u16()
	if err != nil {
		return "", err
	}
	if c.pos+int(listLen) != len(data) {
		return "", malformed("server_name list length %d does not match extension data", listLen)
	}

	var host string
	for c.pos < len(data) {
		nameType, err := c.u8()
		if err != nil {
			return "", err
		}
		name, err := c.vec16()
		if err != nil {
			return "", err
		}
		if nameType == hostNameType {
			if host != "" {
				return "", malformed("duplicate host_name entry in server_name extension")
			}
			if len(name) == 0 {
				return "", malformed("empty host_name in server_name extension")
			}
			host = string(name)
		}
		// Unknown name types are permitted by RFC 6066 and are skipped,
		// but their lengths are still validated by the cursor above.
	}

	return host, nil
}

// parseALPN parses the application_layer_protocol_negotiation extension
// (RFC 7301 Section 3.1).
func parseALPN(data []byte) ([]string, error) {
	c := &cursor{b: data}

	// extension_data = u16 protocol_name_list length + ProtocolName entries.
	listLen, err := c.u16()
	if err != nil {
		return nil, err
	}
	if c.pos+int(listLen) != len(data) {
		return nil, malformed("protocol_name_list length does not match ALPN extension data")
	}

	var protos []string
	for c.pos < len(data) {
		proto, err := c.vec8()
		if err != nil {
			return nil, err
		}
		if len(proto) == 0 {
			return nil, malformed("empty ProtocolName in ALPN extension")
		}
		protos = append(protos, string(proto))
	}

	return protos, nil
}
