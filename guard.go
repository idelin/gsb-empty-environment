package netsec

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

const (
	recordTypeAlert     uint8 = 21
	recordTypeHandshake uint8 = 22

	handshakeTypeClientHello uint8 = 1
	handshakeTypeServerHello uint8 = 2

	extensionSupportedVersions uint16 = 0x002b

	// maxTLSRecordLen is deliberately larger than the largest plaintext
	// handshake record crypto/tls emits during the initial flight.
	maxTLSRecordLen = 1 << 16

	maxHandshakeRecordPayload = 1 << 14
)

// helloRetryRequestRandom is the special ServerHello.random used by a TLS 1.3
// HelloRetryRequest (RFC 8446, Section 4.1.3).
var helloRetryRequestRandom = []byte{
	0xCF, 0x21, 0xAD, 0x74, 0xE5, 0x9A, 0x61, 0x11,
	0xBE, 0x1D, 0x8C, 0x02, 0x1E, 0x65, 0xB8, 0x91,
	0xC2, 0xA2, 0x11, 0x16, 0x7A, 0xBB, 0x8C, 0x5E,
	0x07, 0x9E, 0x09, 0xE2, 0xC8, 0xA8, 0x33, 0x9C,
}

type guardRole uint8

const (
	roleServer guardRole = iota
	roleClient
)

// clientOptions configures the Client entry point.
type clientOptions struct {
	fallback bool
}

// ClientOption configures Client.
type ClientOption func(*clientOptions)

// WithFallback marks the handshake as an explicit downgrade retry. Only a
// handshake configured this way advertises TLS_FALLBACK_SCSV.
func WithFallback() ClientOption {
	return func(o *clientOptions) {
		o.fallback = true
	}
}

// WithFallbackSCSV is an alias for WithFallback.
func WithFallbackSCSV() ClientOption {
	return WithFallback()
}

// Server returns a TLS server connection that enforces the server-side
// downgrade rules:
//
//   - If the server supports TLS 1.3 and negotiates TLS 1.2, the last eight
//     bytes of ServerHello.random are set to DowngradeSentinelTLS12.
//   - If the server supports TLS 1.3 and negotiates TLS 1.1 or lower, the last
//     eight bytes are set to DowngradeSentinelTLS11.
//   - If the server only supports TLS 1.2 or lower, no sentinel is written.
//   - If the ClientHello contains TLS_FALLBACK_SCSV and its legacy_version is
//     lower than the server's maximum supported version, the server sends a
//     fatal inappropriate_fallback alert and aborts.
func Server(conn net.Conn, config *tls.Config) *tls.Conn {
	cfg := prepareServerConfig(config)
	guard := &guardConn{
		Conn: conn,
		role: roleServer,
	}
	guard.setMaxVersion(MaxSupportedVersion(cfg))
	return tls.Server(guard, serverTLSConfig(cfg, guard))
}

// Client returns a TLS client connection for an initial handshake. It never
// advertises TLS_FALLBACK_SCSV, even if the supplied configuration contains
// it.
//
// Passing WithFallback marks the handshake as an explicit downgrade retry and
// causes TLS_FALLBACK_SCSV to be added to the ClientHello.
func Client(conn net.Conn, config *tls.Config, opts ...ClientOption) *tls.Conn {
	var options clientOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	cfg := prepareClientConfig(config)
	guard := &guardConn{
		Conn:     conn,
		role:     roleClient,
		fallback: options.fallback,
	}
	guard.setMaxVersion(MaxSupportedVersion(cfg))
	return tls.Client(guard, cfg)
}

// ClientFallback is a shorthand for Client(conn, config, WithFallback()).
func ClientFallback(conn net.Conn, config *tls.Config) *tls.Conn {
	return Client(conn, config, WithFallback())
}

// FallbackClient is an alias for ClientFallback.
func FallbackClient(conn net.Conn, config *tls.Config) *tls.Conn {
	return ClientFallback(conn, config)
}

// WrapServer is an alias for Server.
func WrapServer(conn net.Conn, config *tls.Config) *tls.Conn {
	return Server(conn, config)
}

// WrapClient is an alias for Client.
func WrapClient(conn net.Conn, config *tls.Config, opts ...ClientOption) *tls.Conn {
	return Client(conn, config, opts...)
}

// Dial establishes a TCP connection and performs a guarded initial TLS
// handshake.
func Dial(network, addr string, config *tls.Config, opts ...ClientOption) (*tls.Conn, error) {
	raw, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	conn := Client(raw, config, opts...)
	if err := conn.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

// DialFallback is a shorthand for Dial with WithFallback.
func DialFallback(network, addr string, config *tls.Config) (*tls.Conn, error) {
	return Dial(network, addr, config, WithFallback())
}

func prepareServerConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	cfg := config.Clone()
	cfg.CipherSuites = withoutFallbackSCSV(cfg.CipherSuites)
	return cfg
}

// serverTLSConfig suppresses Go's TLS 1.3 downgrade sentinel for servers whose
// own maximum is TLS 1.2 or lower. crypto/tls also writes the 00 sentinel when
// such a server negotiates TLS 1.1, which is outside the rule implemented
// here. Capping the per-connection maximum to the highest version the client
// offered leaves version selection unchanged while making crypto/tls treat
// the negotiated version as the server maximum.
func serverTLSConfig(config *tls.Config, guard *guardConn) *tls.Config {
	if config == nil {
		return nil
	}

	cfg := config.Clone()
	userGetConfig := cfg.GetConfigForClient
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		selected := config
		if userGetConfig != nil {
			var err error
			selected, err = userGetConfig(hello)
			if err != nil {
				return nil, err
			}
			if selected == nil {
				selected = config
			}
		}

		selectedMax := MaxSupportedVersion(selected)
		guard.setMaxVersion(selectedMax)
		if selectedMax == 0 {
			return selected, nil
		}
		if selectedMax >= tls.VersionTLS13 {
			selected = selected.Clone()
			selected.GetConfigForClient = nil
			selected.CipherSuites = withoutFallbackSCSV(selected.CipherSuites)
			return selected, nil
		}
		capped := selected.Clone()
		capped.GetConfigForClient = nil
		capped.CipherSuites = withoutFallbackSCSV(capped.CipherSuites)
		if best := highestOfferedVersion(selectedMax, hello.SupportedVersions); best != 0 {
			capped.MaxVersion = best
		}
		return capped, nil
	}
	return cfg
}

func highestOfferedVersion(serverMax uint16, offered []uint16) uint16 {
	var best uint16
	for _, version := range offered {
		if version <= serverMax && version > best {
			best = version
		}
	}
	return best
}

func prepareClientConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	cfg := config.Clone()
	cfg.CipherSuites = withoutFallbackSCSV(cfg.CipherSuites)
	return cfg
}

func withoutFallbackSCSV(in []uint16) []uint16 {
	if !HasFallbackSCSV(in) {
		return in
	}
	out := make([]uint16, 0, len(in))
	for _, id := range in {
		if id != TLS_FALLBACK_SCSV {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// guardConn inspects the first handshake record on each direction. It is
// deliberately transparent for every other record.
type guardConn struct {
	net.Conn
	role       guardRole
	maxVersion atomic.Uint32
	fallback   bool

	readMu    sync.Mutex
	readReady bool
	readBuf   []byte
	readErr   error

	writeMu    sync.Mutex
	writeReady bool
	writeBuf   []byte
	writeErr   error
}

func (g *guardConn) setMaxVersion(version uint16) {
	g.maxVersion.Store(uint32(version))
}

func (g *guardConn) getMaxVersion() uint16 {
	return uint16(g.maxVersion.Load())
}

func (g *guardConn) Read(p []byte) (int, error) {
	g.readMu.Lock()
	defer g.readMu.Unlock()

	if g.readErr != nil {
		return 0, g.readErr
	}
	if !g.readReady {
		raw, hello, err := readFirstHandshakeMessage(g.Conn)
		if err != nil {
			g.readReady = true
			g.readErr = err
			return 0, err
		}
		g.readReady = true
		if hello != nil {
			raw, err = g.inspectPeerHello(raw, hello)
			if err != nil {
				g.readErr = err
				return 0, err
			}
		}
		g.readBuf = raw
	}
	if len(g.readBuf) > 0 {
		n := copy(p, g.readBuf)
		g.readBuf = g.readBuf[n:]
		return n, nil
	}
	return g.Conn.Read(p)
}

func (g *guardConn) Write(p []byte) (int, error) {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()

	if g.writeErr != nil {
		return 0, g.writeErr
	}
	if g.writeReady {
		return g.Conn.Write(p)
	}

	g.writeBuf = append(g.writeBuf, p...)
	out, ready, err := g.firstWrite()
	if err != nil {
		g.writeErr = err
		return 0, err
	}
	if !ready {
		return len(p), nil
	}

	g.writeReady = true
	g.writeBuf = nil
	if err := writeAll(g.Conn, out); err != nil {
		g.writeErr = err
		return 0, err
	}
	return len(p), nil
}

func (g *guardConn) firstWrite() (out []byte, ready bool, err error) {
	buf := g.writeBuf
	if len(buf) < 5 {
		return nil, false, nil
	}
	recordLen := int(binary.BigEndian.Uint16(buf[3:5]))
	if recordLen > maxTLSRecordLen {
		return nil, false, fmt.Errorf("netsec: oversized TLS record of %d bytes", recordLen)
	}
	total := 5 + recordLen
	if len(buf) < total {
		return nil, false, nil
	}

	record := buf[:total]
	rest := buf[total:]
	if record[0] != recordTypeHandshake {
		return buf, true, nil
	}
	if len(record) < 5+4 {
		return buf, true, nil
	}

	payload := record[5:]
	helloType := payload[0]
	if helloType != g.outgoingHelloType() {
		return buf, true, nil
	}
	helloLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+helloLen {
		// The initial hello is fragmented across records. Passing it
		// through unchanged is safer than emitting a partially edited
		// handshake transcript.
		return buf, true, nil
	}

	switch g.role {
	case roleClient:
		if g.fallback {
			edited, changed := addFallbackSCSV(record)
			if changed {
				out = make([]byte, 0, len(edited)+len(rest))
				out = append(out, edited...)
				out = append(out, rest...)
				return out, true, nil
			}
		}
	case roleServer:
		applyServerHelloSentinel(record, g.getMaxVersion())
	}
	return buf, true, nil
}

func (g *guardConn) outgoingHelloType() uint8 {
	if g.role == roleServer {
		return handshakeTypeServerHello
	}
	return handshakeTypeClientHello
}

func (g *guardConn) inspectPeerHello(raw, hello []byte) ([]byte, error) {
	if len(hello) < 4 {
		return raw, nil
	}
	switch g.role {
	case roleServer:
		if hello[0] != handshakeTypeClientHello {
			return raw, nil
		}
		info, err := parseClientHello(hello)
		if err != nil {
			return raw, nil
		}
		if err := ValidateFallbackSCSV(info.legacyVersion, g.getMaxVersion(), info.cipherSuites); err != nil {
			g.sendAlert(AlertInappropriateFallback)
			return nil, &AlertError{Description: AlertInappropriateFallback, Reason: err}
		}
		if HasFallbackSCSV(info.cipherSuites) {
			return removeFallbackSCSV(raw, hello, info), nil
		}
	case roleClient:
		if hello[0] != handshakeTypeServerHello {
			return raw, nil
		}
		info, err := parseServerHello(hello)
		if err != nil {
			return raw, nil
		}
		if err := ValidateServerRandom(g.getMaxVersion(), info.negotiatedVersion, info.random); err != nil {
			g.sendAlert(AlertIllegalParameter)
			return nil, &AlertError{Description: AlertIllegalParameter, Reason: err}
		}
	}
	return raw, nil
}

func (g *guardConn) sendAlert(description uint8) {
	alert := []byte{
		recordTypeAlert, 0x03, 0x03, 0x00, 0x02, 0x02, description,
	}
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	if err := writeAll(g.Conn, alert); err == nil {
		_ = g.Conn.Close()
	}
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// readFirstHandshakeMessage reads complete TLS records until it has the first
// complete handshake message. It returns the raw records and the contiguous
// handshake message bytes. If the first record is not a handshake record,
// hello is nil and the raw record is returned untouched.
func readFirstHandshakeMessage(r io.Reader) (raw, hello []byte, err error) {
	var handshake []byte
	for {
		record, recordType, payload, err := readTLSRecord(r)
		if err != nil {
			return nil, nil, err
		}
		raw = append(raw, record...)
		if recordType != recordTypeHandshake {
			return raw, nil, nil
		}
		handshake = append(handshake, payload...)
		if len(handshake) < 4 {
			continue
		}
		n := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
		if len(handshake) >= 4+n {
			return raw, handshake[:4+n], nil
		}
	}
}

func readTLSRecord(r io.Reader) (raw []byte, recordType uint8, payload []byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(header[3:5]))
	if n > maxTLSRecordLen {
		return nil, 0, nil, fmt.Errorf("netsec: oversized TLS record of %d bytes", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, 0, nil, err
	}
	raw = append(header[:], body...)
	return raw, header[0], body, nil
}

type clientHelloInfo struct {
	legacyVersion uint16
	cipherSuites  []uint16

	// Offset of the two-byte cipher_suites length field, relative to the
	// beginning of the ClientHello body.
	cipherSuitesLenOffset int
	// Offset of the cipher_suites vector, relative to the beginning of the
	// ClientHello body.
	cipherSuitesStart int
	// Offset one past the cipher_suites vector, relative to the beginning of
	// the ClientHello body.
	cipherSuitesEnd int
}

func parseClientHello(hello []byte) (*clientHelloInfo, error) {
	const handshakeHeaderLen = 4
	if len(hello) < handshakeHeaderLen+2+32+1 {
		return nil, errors.New("netsec: short ClientHello")
	}
	bodyLen := int(hello[1])<<16 | int(hello[2])<<8 | int(hello[3])
	if bodyLen < 2+32+1 || len(hello) < handshakeHeaderLen+bodyLen {
		return nil, errors.New("netsec: malformed ClientHello")
	}
	body := hello[handshakeHeaderLen : handshakeHeaderLen+bodyLen]

	p := 34 // legacy_version (2) + random (32)
	sessionIDLen := int(body[p])
	p++
	if p+sessionIDLen > len(body) {
		return nil, errors.New("netsec: malformed ClientHello session ID")
	}
	p += sessionIDLen
	if p+2 > len(body) {
		return nil, errors.New("netsec: missing ClientHello cipher suites")
	}
	suitesLenOffset := p
	suitesLen := int(binary.BigEndian.Uint16(body[p : p+2]))
	p += 2
	if p+suitesLen > len(body) || suitesLen%2 != 0 {
		return nil, errors.New("netsec: malformed ClientHello cipher suites")
	}
	suitesEnd := p + suitesLen
	suites := make([]uint16, 0, suitesLen/2)
	for i := p; i < suitesEnd; i += 2 {
		suites = append(suites, binary.BigEndian.Uint16(body[i:i+2]))
	}

	return &clientHelloInfo{
		legacyVersion:         binary.BigEndian.Uint16(body[0:2]),
		cipherSuites:          suites,
		cipherSuitesLenOffset: suitesLenOffset,
		cipherSuitesStart:     suitesLenOffset + 2,
		cipherSuitesEnd:       suitesEnd,
	}, nil
}

type serverHelloInfo struct {
	legacyVersion     uint16
	negotiatedVersion uint16
	random            []byte
}

func parseServerHello(hello []byte) (*serverHelloInfo, error) {
	const handshakeHeaderLen = 4
	if len(hello) < handshakeHeaderLen+2+32+1+2+1 {
		return nil, errors.New("netsec: short ServerHello")
	}
	bodyLen := int(hello[1])<<16 | int(hello[2])<<8 | int(hello[3])
	if bodyLen < 2+32+1+2+1 || len(hello) < handshakeHeaderLen+bodyLen {
		return nil, errors.New("netsec: malformed ServerHello")
	}
	body := hello[handshakeHeaderLen : handshakeHeaderLen+bodyLen]

	legacyVersion := binary.BigEndian.Uint16(body[0:2])
	random := body[2:34]
	p := 34
	sessionIDLen := int(body[p])
	p++
	if p+sessionIDLen > len(body) {
		return nil, errors.New("netsec: malformed ServerHello session ID")
	}
	p += sessionIDLen
	if p+3 > len(body) {
		return nil, errors.New("netsec: missing ServerHello cipher suite")
	}
	p += 3 // cipher_suite (2) + compression_method (1)

	negotiated := legacyVersion
	if bytes.Equal(random, helloRetryRequestRandom) {
		negotiated = tls.VersionTLS13
	}
	if p+2 <= len(body) {
		extensionsLen := int(binary.BigEndian.Uint16(body[p : p+2]))
		p += 2
		end := p + extensionsLen
		if end > len(body) {
			return nil, errors.New("netsec: malformed ServerHello extensions")
		}
		for p+4 <= end {
			extType := binary.BigEndian.Uint16(body[p : p+2])
			extLen := int(binary.BigEndian.Uint16(body[p+2 : p+4]))
			p += 4
			if p+extLen > end {
				return nil, errors.New("netsec: malformed ServerHello extension")
			}
			if extType == extensionSupportedVersions && extLen >= 2 {
				negotiated = binary.BigEndian.Uint16(body[p : p+2])
			}
			p += extLen
		}
	}

	return &serverHelloInfo{
		legacyVersion:     legacyVersion,
		negotiatedVersion: negotiated,
		random:            random,
	}, nil
}

func applyServerHelloSentinel(record []byte, serverMax uint16) bool {
	if len(record) < 5+4 || record[0] != recordTypeHandshake {
		return false
	}
	payload := record[5:]
	if payload[0] != handshakeTypeServerHello {
		return false
	}
	helloLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+helloLen {
		return false
	}
	info, err := parseServerHello(payload[:4+helloLen])
	if err != nil {
		return false
	}
	return ApplyServerSentinel(info.random, serverMax, info.negotiatedVersion)
}

// removeFallbackSCSV removes TLS_FALLBACK_SCSV from the ClientHello that is
// passed to crypto/tls. The server has already validated it above; removing it
// is what keeps a local ClientFallback handshake's transcript consistent,
// because that client's own transcript was built before the SCSV was added to
// the wire.
func removeFallbackSCSV(raw, hello []byte, info *clientHelloInfo) []byte {
	index := -1
	for i, id := range info.cipherSuites {
		if id == TLS_FALLBACK_SCSV {
			index = i
			break
		}
	}
	if index < 0 {
		return raw
	}
	scsvOffset := info.cipherSuitesStart + 2*index
	if 4+scsvOffset+2 > len(hello) {
		return raw
	}

	newHello := make([]byte, len(hello)-2)
	copy(newHello, hello[:4+scsvOffset])
	copy(newHello[4+scsvOffset:], hello[4+scsvOffset+2:])

	newBodyLen := len(hello) - 4 - 2
	newHello[1] = byte(newBodyLen >> 16)
	newHello[2] = byte(newBodyLen >> 8)
	newHello[3] = byte(newBodyLen)

	suitesLenOffset := 4 + info.cipherSuitesLenOffset
	suitesLen := binary.BigEndian.Uint16(newHello[suitesLenOffset : suitesLenOffset+2])
	binary.BigEndian.PutUint16(newHello[suitesLenOffset:suitesLenOffset+2], suitesLen-2)

	payload := append(newHello, trailingHandshakeBytes(raw, len(hello))...)
	version := [2]byte{0x03, 0x01}
	if len(raw) >= 3 {
		version[0] = raw[1]
		version[1] = raw[2]
	}
	return marshalHandshakeRecords(version, payload)
}

func trailingHandshakeBytes(raw []byte, firstMessageLen int) []byte {
	var payload []byte
	for len(raw) > 0 {
		if len(raw) < 5 {
			return nil
		}
		n := int(binary.BigEndian.Uint16(raw[3:5]))
		if n > len(raw)-5 {
			return nil
		}
		if raw[0] == recordTypeHandshake {
			payload = append(payload, raw[5:5+n]...)
		}
		raw = raw[5+n:]
	}
	if len(payload) <= firstMessageLen {
		return nil
	}
	return payload[firstMessageLen:]
}

func marshalHandshakeRecords(version [2]byte, payload []byte) []byte {
	var out []byte
	for len(payload) > 0 {
		n := len(payload)
		if n > maxHandshakeRecordPayload {
			n = maxHandshakeRecordPayload
		}
		header := [5]byte{
			recordTypeHandshake,
			version[0],
			version[1],
			byte(n >> 8),
			byte(n),
		}
		out = append(out, header[:]...)
		out = append(out, payload[:n]...)
		payload = payload[n:]
	}
	return out
}

// addFallbackSCSV appends TLS_FALLBACK_SCSV to the ClientHello cipher suites
// vector and fixes the nested ClientHello and TLS record lengths. It returns
// the original record unchanged if the SCSV is already present or the record
// is malformed.
func addFallbackSCSV(record []byte) ([]byte, bool) {
	if len(record) < 5+4+34 || record[0] != recordTypeHandshake {
		return record, false
	}
	payload := record[5:]
	if payload[0] != handshakeTypeClientHello {
		return record, false
	}
	helloLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+helloLen {
		return record, false
	}
	info, err := parseClientHello(payload[:4+helloLen])
	if err != nil {
		return record, false
	}
	if HasFallbackSCSV(info.cipherSuites) {
		return record, false
	}

	body := payload[4 : 4+helloLen]
	newBodyLen := helloLen + 2
	newPayloadLen := len(payload) + 2
	if newPayloadLen > 0xffff {
		return record, false
	}
	newRecord := make([]byte, 5+newPayloadLen)

	// TLS record header.
	newRecord[0] = record[0]
	copy(newRecord[1:3], record[1:3])
	binary.BigEndian.PutUint16(newRecord[3:5], uint16(newPayloadLen))

	// Handshake header.
	newRecord[5] = handshakeTypeClientHello
	newRecord[6] = byte(newBodyLen >> 16)
	newRecord[7] = byte(newBodyLen >> 8)
	newRecord[8] = byte(newBodyLen)

	// Copy the body up to the end of the cipher_suites vector, append the
	// SCSV, then copy the remainder of the payload.
	bodyStart := 9
	copy(newRecord[bodyStart:], body[:info.cipherSuitesEnd])
	bodyCopyLen := bodyStart + info.cipherSuitesEnd
	newRecord[bodyCopyLen] = byte(TLS_FALLBACK_SCSV >> 8)
	newRecord[bodyCopyLen+1] = byte(TLS_FALLBACK_SCSV & 0xff)
	copy(newRecord[bodyCopyLen+2:], body[info.cipherSuitesEnd:])
	copy(newRecord[bodyStart+newBodyLen:], payload[4+helloLen:])

	// Fix cipher_suites length.
	oldSuitesLen := binary.BigEndian.Uint16(body[info.cipherSuitesLenOffset : info.cipherSuitesLenOffset+2])
	binary.BigEndian.PutUint16(newRecord[bodyStart+info.cipherSuitesLenOffset:], oldSuitesLen+2)

	return newRecord, true
}
