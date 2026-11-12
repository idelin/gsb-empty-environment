package pqpin

import (
	"net"
)

// TLS record and handshake content type constants (RFC 8446).
const (
	recordHandshake      = 22
	handshakeServerHello = 2
	extKeyShare          = 51
)

// helloRetryRequestRandom is the ServerHello.random value that marks the
// message as a HelloRetryRequest (RFC 8446 4.1.3).
var helloRetryRequestRandom = [32]byte{
	0xCF, 0x21, 0xAD, 0x74, 0xE5, 0x9A, 0x61, 0x11,
	0xBE, 0x1D, 0x8C, 0x02, 0x1E, 0x65, 0xB8, 0x91,
	0xC2, 0xA2, 0x11, 0x16, 0x7A, 0xBB, 0x8C, 0x5E,
	0x07, 0x9E, 0x09, 0xE2, 0xC8, 0xA8, 0x33, 0x9C,
}

// snoopConn is a net.Conn wrapper that transparently inspects the inbound TLS
// record stream for ServerHello and HelloRetryRequest messages, extracting the
// key_share group selected by the server. It stops parsing once the final
// ServerHello has been consumed and never alters the byte stream.
type snoopConn struct {
	net.Conn

	// recordBuf accumulates TLS record payloads.
	recordBuf []byte
	// done is true once the final ServerHello has been parsed.
	done bool

	hadHRR          bool
	hrrGroup        uint16
	negotiatedGroup uint16

	// onHRR, when set, is invoked as soon as an HRR is parsed. A non-nil
	// return value closes the underlying connection so the handshake aborts.
	onHRR func(group uint16) error
}

func newSnoopConn(conn net.Conn, onHRR func(uint16) error) *snoopConn {
	return &snoopConn{Conn: conn, onHRR: onHRR}
}

func (c *snoopConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && !c.done {
		c.feed(p[:n])
	}
	return n, err
}

// feed appends freshly read bytes and extracts every complete TLS record that
// is currently buffered.
func (c *snoopConn) feed(chunk []byte) {
	c.recordBuf = append(c.recordBuf, chunk...)
	for {
		consumed, ok := c.parseRecord()
		if !ok {
			return
		}
		c.recordBuf = c.recordBuf[consumed:]
		if c.done {
			c.recordBuf = nil
			return
		}
	}
}

// parseRecord parses one TLS record from recordBuf. It returns the number of
// bytes consumed and false when a full record is not buffered yet or when the
// bytes do not look like a TLS record (in which case snooping is abandoned).
func (c *snoopConn) parseRecord() (int, bool) {
	buf := c.recordBuf
	if len(buf) < 5 {
		return 0, false
	}
	// TLS records starting at this point of the handshake are plaintext:
	// contentType(1) legacyVersion(2) length(2).
	length := int(buf[3])<<8 | int(buf[4])
	if length == 0 || len(buf) < 5+length {
		return 0, false
	}
	payload := buf[5 : 5+length]
	if buf[0] == recordHandshake {
		c.parseHandshakePayload(payload)
	}
	return 5 + length, true
}

// parseHandshakePayload walks the handshake messages inside one record. TLS
// allows (and commonly uses) exactly one handshake message per record at this
// stage, but the loop also handles coalesced messages defensively.
func (c *snoopConn) parseHandshakePayload(payload []byte) {
	for len(payload) >= 4 {
		msgType := payload[0]
		msgLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
		if msgLen == 0 || len(payload) < 4+msgLen {
			return
		}
		if msgType == handshakeServerHello {
			group, isHRR, ok := parseServerHello(payload[4 : 4+msgLen])
			if !ok {
				return
			}
			if isHRR {
				c.hadHRR = true
				c.hrrGroup = group
				if c.onHRR != nil {
					if err := c.onHRR(group); err != nil {
						if c.Conn != nil {
							_ = c.Close()
						}
						return
					}
				}
			} else {
				c.negotiatedGroup = group
				c.done = true
			}
		}
		payload = payload[4+msgLen:]
	}
}

// parseServerHello extracts the selected key_share group from a ServerHello or
// HelloRetryRequest. The returned isHRR flag distinguishes the two via the
// fixed HRR random.
//
// ServerHello layout (RFC 8446 4.1.3):
//
//	uint16 legacy_version;
//	opaque random[32];
//	opaque legacy_session_id<0..32>;
//	CipherSuite cipher_suite;
//	uint8 legacy_compression_method;
//	Extension extensions<6..2^16-1>;
func parseServerHello(msg []byte) (group uint16, isHRR bool, ok bool) {
	const fixedHeader = 2 + 32 + 1 // version + random + session_id length
	if len(msg) < fixedHeader {
		return 0, false, false
	}
	var random [32]byte
	copy(random[:], msg[2:34])
	isHRR = random == helloRetryRequestRandom

	sessionIDLen := int(msg[34])
	pos := fixedHeader + sessionIDLen + 2 + 1 // session id + cipher suite + compression
	if len(msg) < pos+2 {
		return 0, isHRR, false
	}
	extTotal := int(msg[pos])<<8 | int(msg[pos+1])
	pos += 2
	if len(msg) < pos+extTotal {
		return 0, isHRR, false
	}
	extensions := msg[pos : pos+extTotal]

	for len(extensions) >= 4 {
		extType := uint16(extensions[0])<<8 | uint16(extensions[1])
		extLen := int(extensions[2])<<8 | int(extensions[3])
		if len(extensions) < 4+extLen {
			return 0, isHRR, false
		}
		data := extensions[4 : 4+extLen]
		extensions = extensions[4+extLen:]
		if extType != extKeyShare {
			continue
		}
		if isHRR {
			// HRR key_share extension contains only the selected NamedGroup.
			if extLen != 2 {
				return 0, true, false
			}
			return uint16(data[0])<<8 | uint16(data[1]), true, true
		}
		// ServerHello key_share: NamedGroup followed by key_exchange<1..2^16-1>.
		if extLen < 4 {
			return 0, false, false
		}
		group = uint16(data[0])<<8 | uint16(data[1])
		return group, false, true
	}
	return 0, isHRR, true
}

// observation converts the snooped handshake evidence into an Observation.
func (c *snoopConn) observation(serverID string) Observation {
	return Observation{
		ServerID:        serverID,
		NegotiatedGroup: c.negotiatedGroup,
		HRRGroup:        c.hrrGroup,
		HadHRR:          c.hadHRR,
	}
}

// Compile-time assertion that snoopConn stays a net.Conn.
var _ net.Conn = (*snoopConn)(nil)
