package netsec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ClientSSL is the MySQL capability flag (0x0800) a server sets when it is
// willing to upgrade the connection to TLS via an SSLRequest.
const ClientSSL uint32 = 0x00000800

const (
	packetHeaderLen      = 4
	sslRequestPayloadLen = 32
	protocolVersion10    = 0x0a
)

var (
	// ErrTLSRequired is returned when the server handshake does not
	// advertise CLIENT_SSL but the configuration requires TLS. This is
	// the classic TLS-stripping signal: a MITM cleared the capability bit
	// to talk the client into a cleartext login.
	ErrTLSRequired = errors.New("netsec: server does not advertise CLIENT_SSL but TLS is required")

	// ErrPrematureServerBytes is returned when server bytes are already
	// buffered after the SSLRequest was sent but before the TLS handshake
	// completed. A compliant server stays silent until it sees the TLS
	// ClientHello, so any such bytes are an injection.
	ErrPrematureServerBytes = errors.New("netsec: server bytes arrived before TLS handshake completed")

	// ErrAuthBeforeTLS is returned when the driver tries to build an
	// authentication response while the connection is still cleartext and
	// TLS is required.
	ErrAuthBeforeTLS = errors.New("netsec: refusing to send auth response before TLS is established")

	// ErrUnexpectedPacket is returned for plaintext packets that do not
	// belong to the current handshake stage.
	ErrUnexpectedPacket = errors.New("netsec: unexpected plaintext packet for handshake stage")

	// ErrBadSequence is returned when a packet carries a sequence id other
	// than the one the protocol dictates for this stage.
	ErrBadSequence = errors.New("netsec: unexpected packet sequence id")

	// ErrMalformedHandshake is returned when the server handshake packet
	// cannot be parsed.
	ErrMalformedHandshake = errors.New("netsec: malformed server handshake packet")
)

// Config controls how strict the handshake admission is.
type Config struct {
	// RequireTLS aborts the handshake unless the server advertises
	// CLIENT_SSL, and forbids emitting any authentication reply while the
	// connection is still cleartext.
	RequireTLS bool
}

type stage int

const (
	stageInit stage = iota
	stageHandshaken
	stageSSLRequested
	stageTLS
)

// Guard is a client-side admission controller for the MySQL handshake. It
// is a strict state machine: initial handshake -> SSLRequest -> TLS -> auth
// response. Any deviation (missing CLIENT_SSL, early server bytes, packets
// out of order, auth material requested in cleartext) aborts the login.
//
// Guard is not safe for concurrent use; a handshake is inherently sequential.
type Guard struct {
	cfg            Config
	stage          stage
	serverCaps     uint32
	authPluginData []byte
}

// NewGuard returns a Guard enforcing cfg.
func NewGuard(cfg Config) *Guard {
	return &Guard{cfg: cfg}
}

// ServerCapabilities returns the capability flags parsed from the server
// handshake. It is only valid after ReadServerHandshake succeeded.
func (g *Guard) ServerCapabilities() uint32 {
	return g.serverCaps
}

// ReadServerHandshake consumes the server's initial handshake packet (raw
// bytes including the 4-byte header; sequence id must be 0). When
// Config.RequireTLS is set and the server capability flags lack CLIENT_SSL,
// it fails with ErrTLSRequired.
func (g *Guard) ReadServerHandshake(pkt []byte) error {
	if g.stage != stageInit {
		return ErrUnexpectedPacket
	}
	payload, err := parsePacket(pkt, 0)
	if err != nil {
		return err
	}
	caps, authData, err := parseHandshake(payload)
	if err != nil {
		return err
	}
	if g.cfg.RequireTLS && caps&ClientSSL == 0 {
		return fmt.Errorf("%w (server capabilities: 0x%08x)", ErrTLSRequired, caps)
	}
	g.serverCaps = caps
	g.authPluginData = authData
	g.stage = stageHandshaken
	return nil
}

// BuildSSLRequest returns the SSLRequest packet: a 4-byte header (sequence
// id 1) followed by exactly 32 payload bytes carrying the client capability
// flags. It never contains auth_plugin_data or any credential material.
func (g *Guard) BuildSSLRequest(clientCaps uint32) ([]byte, error) {
	if g.stage != stageHandshaken {
		return nil, ErrUnexpectedPacket
	}
	if g.cfg.RequireTLS {
		clientCaps |= ClientSSL
	}
	payload := make([]byte, sslRequestPayloadLen)
	binary.LittleEndian.PutUint32(payload[0:4], clientCaps)
	binary.LittleEndian.PutUint32(payload[4:8], 0) // max packet size: unset
	payload[8] = 33                                // utf8_general_ci
	// payload[9:32] stays zero (reserved).
	g.stage = stageSSLRequested
	return wrapPacket(payload, 1), nil
}

// BeginTLS must be called after the SSLRequest has been flushed to the wire
// and immediately before the TLS client handshake starts. r is the buffered
// reader wrapping the connection; if it already holds server bytes, the
// login is aborted with ErrPrematureServerBytes.
func (g *Guard) BeginTLS(r *bufio.Reader) error {
	if g.stage != stageSSLRequested {
		return ErrUnexpectedPacket
	}
	if r != nil && r.Buffered() > 0 {
		return fmt.Errorf("%w (%d bytes buffered)", ErrPrematureServerBytes, r.Buffered())
	}
	g.stage = stageTLS
	return nil
}

// AuthResponseData releases the auth_plugin_data captured from the server
// handshake so the driver can mix it into the login reply. While TLS is
// required but not yet established, it refuses with ErrAuthBeforeTLS: the
// salt must never feed a cleartext reply.
func (g *Guard) AuthResponseData() ([]byte, error) {
	if g.stage < stageHandshaken {
		return nil, ErrUnexpectedPacket
	}
	if g.cfg.RequireTLS && g.stage != stageTLS {
		return nil, ErrAuthBeforeTLS
	}
	out := make([]byte, len(g.authPluginData))
	copy(out, g.authPluginData)
	return out, nil
}

// ProcessPlaintextPacket admits a further cleartext packet from the server.
// After the SSLRequest has been sent, any plaintext server packet is by
// definition premature or out of order and is rejected.
func (g *Guard) ProcessPlaintextPacket(pkt []byte) error {
	switch g.stage {
	case stageSSLRequested, stageTLS:
		return fmt.Errorf("%w: server sent %d cleartext bytes after SSLRequest", ErrPrematureServerBytes, len(pkt))
	default:
		return ErrUnexpectedPacket
	}
}

// parsePacket splits a raw packet into header and payload, verifying the
// sequence id and the declared length.
func parsePacket(pkt []byte, wantSeq byte) ([]byte, error) {
	if len(pkt) < packetHeaderLen {
		return nil, fmt.Errorf("%w: packet shorter than header", ErrMalformedHandshake)
	}
	declared := int(pkt[0]) | int(pkt[1])<<8 | int(pkt[2])<<16
	if pkt[3] != wantSeq {
		return nil, fmt.Errorf("%w: got sequence %d, want %d", ErrBadSequence, pkt[3], wantSeq)
	}
	if declared != len(pkt)-packetHeaderLen {
		return nil, fmt.Errorf("%w: declared payload %d bytes, got %d", ErrMalformedHandshake, declared, len(pkt)-packetHeaderLen)
	}
	return pkt[packetHeaderLen:], nil
}

// wrapPacket prepends a 4-byte header (3-byte little-endian length, 1-byte
// sequence id) to payload.
func wrapPacket(payload []byte, seq byte) []byte {
	pkt := make([]byte, packetHeaderLen+len(payload))
	pkt[0] = byte(len(payload))
	pkt[1] = byte(len(payload) >> 8)
	pkt[2] = byte(len(payload) >> 16)
	pkt[3] = seq
	copy(pkt[packetHeaderLen:], payload)
	return pkt
}

// parseHandshake parses a protocol-10 handshake payload and returns the
// server capability flags and the auth_plugin_data (salt).
func parseHandshake(p []byte) (caps uint32, authData []byte, err error) {
	bad := func(msg string) (uint32, []byte, error) {
		return 0, nil, fmt.Errorf("%w: %s", ErrMalformedHandshake, msg)
	}
	if len(p) < 1 {
		return bad("empty payload")
	}
	if p[0] != protocolVersion10 {
		return bad("unexpected protocol version")
	}
	rest := p[1:]
	nul := bytes.IndexByte(rest, 0)
	if nul < 0 {
		return bad("server version not NUL-terminated")
	}
	rest = rest[nul+1:]
	// connection id (4) + auth-plugin-data-part-1 (8) + filler (1) + caps lower (2)
	if len(rest) < 4+8+1+2 {
		return bad("truncated fixed fields")
	}
	rest = rest[4:]
	authData = append(authData, rest[:8]...)
	rest = rest[8+1:]
	caps = uint32(binary.LittleEndian.Uint16(rest[:2]))
	rest = rest[2:]
	if len(rest) == 0 {
		// Pre-4.1 style handshake: no upper capabilities, no salt part 2.
		return caps, authData, nil
	}
	// charset (1) + status (2) + caps upper (2) + auth data len (1) + reserved (10)
	if len(rest) < 1+2+2+1+10 {
		return bad("truncated capability fields")
	}
	rest = rest[1+2:]
	caps |= uint32(binary.LittleEndian.Uint16(rest[:2])) << 16
	rest = rest[2:]
	authLen := int(rest[0])
	rest = rest[1+10:]
	part2Len := authLen - 8
	if part2Len < 13 {
		part2Len = 13
	}
	if part2Len > len(rest) {
		part2Len = len(rest)
	}
	part2 := bytes.TrimRight(rest[:part2Len], "\x00")
	authData = append(authData, part2...)
	return caps, authData, nil
}
