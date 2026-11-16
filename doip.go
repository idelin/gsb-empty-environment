package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// HeaderLen is the fixed length of a DoIP generic header in bytes.
const HeaderLen = 8

// DoIP payload types handled by the admission gate.
const (
	PayloadTypeRoutingActivationRequest uint16 = 0x0005
	PayloadTypeDiagnosticMessage        uint16 = 0x8001
)

// Routing activation response codes (ISO 13400-2).
const (
	RcUnknownSourceAddress      byte = 0x00
	RcSocketsBusy               byte = 0x01
	RcAlreadyActive             byte = 0x03
	RcUnsupportedActivationType byte = 0x06
	RcSuccess                   byte = 0x10
)

// Diagnostic message ACK/NAK codes (ISO 13400-2).
const (
	DiagAck           byte = 0x00
	DiagNackInvalidSA byte = 0x02
	DiagNackUnknownTA byte = 0x03
)

// Supported routing activation types.
const (
	ActivationTypeDefault        byte = 0x00
	ActivationTypeRegulatoryDiag byte = 0x01
)

var (
	ErrHeaderTooShort   = errors.New("doip: message shorter than generic header")
	ErrVersionMismatch  = errors.New("doip: protocol version and inverse byte are not complementary")
	ErrPayloadTooLarge  = errors.New("doip: payload length exceeds caller limit")
	ErrPayloadTruncated = errors.New("doip: message shorter than declared payload length")
	ErrPayloadTooShort  = errors.New("doip: payload too short for its payload type")
)

// Header is a parsed DoIP generic header.
type Header struct {
	Version     byte
	PayloadType uint16
	PayloadLen  uint32
}

// ParseHeader validates and parses the 8-byte generic header at the start of
// raw. maxPayload is the caller-imposed upper bound on the payload length.
func ParseHeader(raw []byte, maxPayload uint32) (Header, error) {
	if len(raw) < HeaderLen {
		return Header{}, ErrHeaderTooShort
	}
	if raw[0]^raw[1] != 0xFF {
		return Header{}, ErrVersionMismatch
	}
	h := Header{
		Version:     raw[0],
		PayloadType: binary.BigEndian.Uint16(raw[2:4]),
		PayloadLen:  binary.BigEndian.Uint32(raw[4:8]),
	}
	if h.PayloadLen > maxPayload {
		return Header{}, ErrPayloadTooLarge
	}
	if uint64(len(raw))-HeaderLen < uint64(h.PayloadLen) {
		return Header{}, ErrPayloadTruncated
	}
	return h, nil
}

// Verdict is the admission decision for one DoIP message.
type Verdict struct {
	// Accept reports whether the message may be processed/forwarded.
	Accept bool
	// Close reports whether the caller must close the TCP connection.
	Close bool
	// Code is the routing activation response code or diagnostic ACK/NAK
	// code to send back to the tester; zero when not applicable.
	Code byte
	// Err is set when the frame itself is malformed.
	Err error
	// Reason is a human-readable explanation for logging.
	Reason string
}

// Config configures a Gate.
type Config struct {
	// MaxPayloadLen is the caller-imposed upper bound on DoIP payload length.
	MaxPayloadLen uint32
	// IdleTimeout is how long a source address may stay unused before its
	// connection loses the occupancy. Values <= 0 select a 5 minute default.
	IdleTimeout time.Duration
	// Registry maps each registered tester source address to the set of ECU
	// target addresses it is authorized to reach.
	Registry map[uint16][]uint16
	// Now supplies the current time; nil selects time.Now. Tests may inject
	// a fake clock.
	Now func() time.Time
}

// Gate enforces DoIP admission control across all TCP connections of one
// gateway. It is safe for concurrent use by multiple session goroutines.
type Gate struct {
	maxPayload  uint32
	idleTimeout time.Duration
	allowed     map[uint16]map[uint16]struct{}
	now         func() time.Time

	mu     sync.Mutex
	owners map[uint16]*occupancy
}

type occupancy struct {
	owner      *Session
	lastActive time.Time
}

// NewGate builds a Gate from cfg.
func NewGate(cfg Config) *Gate {
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	allowed := make(map[uint16]map[uint16]struct{}, len(cfg.Registry))
	for sa, targets := range cfg.Registry {
		set := make(map[uint16]struct{}, len(targets))
		for _, ta := range targets {
			set[ta] = struct{}{}
		}
		allowed[sa] = set
	}
	return &Gate{
		maxPayload:  cfg.MaxPayloadLen,
		idleTimeout: idle,
		allowed:     allowed,
		now:         now,
		owners:      make(map[uint16]*occupancy),
	}
}

// Session tracks the admission state of one TCP connection. Create one per
// accepted connection with NewSession and use it only from that connection's
// read loop.
type Session struct {
	gate       *Gate
	active     bool
	sourceAddr uint16
}

// NewSession creates the per-connection admission state.
func (g *Gate) NewSession() *Session {
	return &Session{gate: g}
}

// Activated reports whether routing activation succeeded on this connection.
func (s *Session) Activated() bool { return s.active }

// SourceAddr returns the source address registered at activation time.
func (s *Session) SourceAddr() uint16 { return s.sourceAddr }

// HandleMessage validates one complete DoIP message (generic header plus
// payload) for the connection bound to s. The TCP session loop should close
// the connection when the returned Verdict has Close set, forward diagnostic
// messages only when Accept is set, and send Code back to the tester when a
// response is required.
func (g *Gate) HandleMessage(s *Session, msg []byte) Verdict {
	h, err := ParseHeader(msg, g.maxPayload)
	if err != nil {
		return Verdict{Close: true, Err: err, Reason: err.Error()}
	}
	payload := msg[HeaderLen : HeaderLen+int(h.PayloadLen)]
	switch h.PayloadType {
	case PayloadTypeRoutingActivationRequest:
		return g.handleRoutingActivation(s, payload)
	case PayloadTypeDiagnosticMessage:
		return g.handleDiagnostic(s, payload)
	default:
		return Verdict{Reason: fmt.Sprintf("doip: unsupported payload type 0x%04X", h.PayloadType)}
	}
}

func (g *Gate) handleRoutingActivation(s *Session, payload []byte) Verdict {
	if len(payload) < 3 {
		return Verdict{Close: true, Err: ErrPayloadTooShort, Reason: ErrPayloadTooShort.Error()}
	}
	sa := binary.BigEndian.Uint16(payload[0:2])
	actType := payload[2]

	if _, ok := g.allowed[sa]; !ok {
		return Verdict{Code: RcUnknownSourceAddress,
			Reason: fmt.Sprintf("doip: source address 0x%04X not in registry", sa)}
	}
	if actType != ActivationTypeDefault && actType != ActivationTypeRegulatoryDiag {
		return Verdict{Code: RcUnsupportedActivationType,
			Reason: fmt.Sprintf("doip: unsupported activation type 0x%02X", actType)}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if occ, ok := g.owners[sa]; ok {
		if occ.owner == s {
			return Verdict{Code: RcAlreadyActive,
				Reason: fmt.Sprintf("doip: source address 0x%04X already active on this connection", sa)}
		}
		if now.Sub(occ.lastActive) <= g.idleTimeout {
			return Verdict{Code: RcSocketsBusy,
				Reason: fmt.Sprintf("doip: source address 0x%04X occupied by another connection", sa)}
		}
		// Previous owner went idle past the timeout: revoke it and hand over.
		occ.owner.active = false
		occ.owner.sourceAddr = 0
		delete(g.owners, sa)
	}
	s.active = true
	s.sourceAddr = sa
	g.owners[sa] = &occupancy{owner: s, lastActive: now}
	return Verdict{Accept: true, Code: RcSuccess,
		Reason: fmt.Sprintf("doip: routing activated for source address 0x%04X", sa)}
}

func (g *Gate) handleDiagnostic(s *Session, payload []byte) Verdict {
	if !s.active {
		return Verdict{Close: true,
			Reason: "doip: diagnostic message before routing activation; dropping and closing connection"}
	}
	if len(payload) < 4 {
		return Verdict{Close: true, Err: ErrPayloadTooShort, Reason: ErrPayloadTooShort.Error()}
	}
	sa := binary.BigEndian.Uint16(payload[0:2])
	ta := binary.BigEndian.Uint16(payload[2:4])

	if sa != s.sourceAddr {
		return Verdict{Code: DiagNackInvalidSA,
			Reason: fmt.Sprintf("doip: diagnostic source 0x%04X does not match activated 0x%04X", sa, s.sourceAddr)}
	}
	if _, ok := g.allowed[s.sourceAddr][ta]; !ok {
		return Verdict{Code: DiagNackUnknownTA,
			Reason: fmt.Sprintf("doip: target 0x%04X not authorized for source 0x%04X", ta, s.sourceAddr)}
	}

	g.mu.Lock()
	if occ, ok := g.owners[s.sourceAddr]; ok && occ.owner == s {
		occ.lastActive = g.now()
	}
	g.mu.Unlock()
	return Verdict{Accept: true, Code: DiagAck,
		Reason: fmt.Sprintf("doip: diagnostic 0x%04X -> 0x%04X admitted", sa, ta)}
}
