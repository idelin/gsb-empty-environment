package netsec

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

const (
	DefaultEntityAddress    uint16 = 0x0e00
	DefaultMaxPayloadLength uint32 = 1 << 20
	DefaultIdleTimeout             = 5 * time.Minute
)

// Reason identifies why a frame was rejected, dropped, or closed.
type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonMalformedFrame
	ReasonVersionInverseMismatch
	ReasonPayloadTooLarge
	ReasonUnsupportedPayloadType
	ReasonMalformedActivationRequest
	ReasonUnknownSourceAddress
	ReasonInvalidActivationType
	ReasonAlreadyActivated
	ReasonSourceAddressInUse
	ReasonNotActivated
	ReasonDiagnosticSourceMismatch
	ReasonTargetAddressNotAuthorized
	ReasonSessionClosed
)

func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonMalformedFrame:
		return "malformed frame"
	case ReasonVersionInverseMismatch:
		return "protocol version and inverse mismatch"
	case ReasonPayloadTooLarge:
		return "payload too large"
	case ReasonUnsupportedPayloadType:
		return "unsupported payload type"
	case ReasonMalformedActivationRequest:
		return "malformed routing activation request"
	case ReasonUnknownSourceAddress:
		return "unknown source address"
	case ReasonInvalidActivationType:
		return "invalid activation type"
	case ReasonAlreadyActivated:
		return "connection already activated"
	case ReasonSourceAddressInUse:
		return "source address already in use"
	case ReasonNotActivated:
		return "connection not activated"
	case ReasonDiagnosticSourceMismatch:
		return "diagnostic source does not match activation"
	case ReasonTargetAddressNotAuthorized:
		return "diagnostic target is not authorized"
	case ReasonSessionClosed:
		return "session closed"
	default:
		return "unknown reason"
	}
}

// DiagnosticMessage is a validated 0x8001 message that may be routed to an ECU.
type DiagnosticMessage struct {
	SourceAddress uint16
	TargetAddress uint16
	Data          []byte
}

// Decision is the admission result for one frame.
//
// Accepted is true when routing activation succeeded or a diagnostic message
// passed all checks. Deliver is true only for an authorized diagnostic
// message. Response, when non-nil, is a complete DoIP response frame that the
// TCP loop should write back. Close tells the TCP loop to terminate the
// connection after handling this frame.
type Decision struct {
	Accepted     bool
	Deliver      bool
	Close        bool
	Reason       Reason
	Response     []byte
	ResponseCode byte
	Diagnostic   *DiagnosticMessage
}

// GatewayConfig configures an admission gateway.
//
// MaxPayloadLength is the caller's payload limit. Zero selects
// DefaultMaxPayloadLength. IdleTimeout is how long an activated connection may
// be idle before its source address may be reclaimed; zero selects
// DefaultIdleTimeout and a negative value disables reclamation.
type GatewayConfig struct {
	EntityAddress        uint16
	MaxPayloadLength     uint32
	IdleTimeout          time.Duration
	SourceAuthorizations map[uint16][]uint16
	Now                  func() time.Time
}

// Config is an alias for GatewayConfig.
type Config = GatewayConfig

type sourceRegistration struct {
	authorizedTargets map[uint16]struct{}
}

type sourceLease struct {
	session  *Session
	lastSeen time.Time
}

// Gateway owns the source-address registry and connection leases. It is safe
// for concurrent use by multiple TCP session loops.
type Gateway struct {
	entityAddress    uint16
	maxPayloadLength uint32
	idleTimeout      time.Duration
	now              func() time.Time

	listenerMu sync.Mutex
	registry   map[uint16]sourceRegistration
	leases     map[uint16]sourceLease
}

// NewGateway creates an admission gateway.
func NewGateway(config GatewayConfig) *Gateway {
	if config.EntityAddress == 0 {
		config.EntityAddress = DefaultEntityAddress
	}
	if config.MaxPayloadLength == 0 {
		config.MaxPayloadLength = DefaultMaxPayloadLength
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = DefaultIdleTimeout
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	g := &Gateway{
		entityAddress:    config.EntityAddress,
		maxPayloadLength: config.MaxPayloadLength,
		idleTimeout:      config.IdleTimeout,
		now:              config.Now,
		registry:         make(map[uint16]sourceRegistration),
		leases:           make(map[uint16]sourceLease),
	}
	for source, targets := range config.SourceAuthorizations {
		g.Register(source, targets...)
	}
	return g
}

// New is an alias for NewGateway.
func New(config GatewayConfig) *Gateway {
	return NewGateway(config)
}

// MaxPayloadLength returns the configured payload limit.
func (g *Gateway) MaxPayloadLength() uint32 {
	return g.maxPayloadLength
}

// IdleTimeout returns the configured source-lease idle timeout.
func (g *Gateway) IdleTimeout() time.Duration {
	return g.idleTimeout
}

// EntityAddress returns the logical address used in activation responses.
func (g *Gateway) EntityAddress() uint16 {
	return g.entityAddress
}

// Register adds or replaces a source address and the ECU targets it may reach.
func (g *Gateway) Register(source uint16, targets ...uint16) {
	registration := sourceRegistration{
		authorizedTargets: make(map[uint16]struct{}, len(targets)),
	}
	for _, target := range targets {
		registration.authorizedTargets[target] = struct{}{}
	}

	g.listenerMu.Lock()
	g.registry[source] = registration
	g.listenerMu.Unlock()
}

// Unregister removes a source address from the registry. Existing leases stay
// in place, but diagnostic messages from that source no longer authorize any
// target.
func (g *Gateway) Unregister(source uint16) {
	g.listenerMu.Lock()
	delete(g.registry, source)
	g.listenerMu.Unlock()
}

// Registered reports whether a source address is in the registry.
func (g *Gateway) Registered(source uint16) bool {
	g.listenerMu.Lock()
	_, ok := g.registry[source]
	g.listenerMu.Unlock()
	return ok
}

// AuthorizedTargets returns a copy of the ECU targets authorized for source.
func (g *Gateway) AuthorizedTargets(source uint16) ([]uint16, bool) {
	g.listenerMu.Lock()
	registration, ok := g.registry[source]
	g.listenerMu.Unlock()
	if !ok {
		return nil, false
	}

	targets := make([]uint16, 0, len(registration.authorizedTargets))
	for target := range registration.authorizedTargets {
		targets = append(targets, target)
	}
	return targets, true
}

// Session represents one TCP connection's DoIP admission state.
type Session struct {
	gateway       *Gateway
	connectionID  string
	activated     bool
	sourceAddress uint16
	closed        bool
}

// NewSession creates the admission state for one TCP connection. connectionID
// must be unique for each live TCP connection.
func (g *Gateway) NewSession(connectionID string) *Session {
	return &Session{gateway: g, connectionID: connectionID}
}

// NewSession creates admission state for one TCP connection.
func NewSession(g *Gateway, connectionID string) *Session {
	return g.NewSession(connectionID)
}

// ConnectionID returns the caller-provided connection identifier.
func (s *Session) ConnectionID() string {
	return s.connectionID
}

// Activated reports whether this session currently owns an activated source
// address.
func (s *Session) Activated() bool {
	s.gateway.listenerMu.Lock()
	activated := s.activated && !s.closed
	s.gateway.listenerMu.Unlock()
	return activated
}

// ActivatedSource returns the source address bound to this session.
func (s *Session) ActivatedSource() (uint16, bool) {
	s.gateway.listenerMu.Lock()
	defer s.gateway.listenerMu.Unlock()
	if !s.activated || s.closed {
		return 0, false
	}
	return s.sourceAddress, true
}

// Close releases this session's source lease. TCP loops should call it when
// the connection terminates.
func (s *Session) Close() {
	s.gateway.listenerMu.Lock()
	defer s.gateway.listenerMu.Unlock()

	s.releaseLeaseLocked()
	s.closed = true
	s.activated = false
	s.sourceAddress = 0
}

// Handle processes one complete DoIP frame using the gateway clock.
func (s *Session) Handle(frame []byte) Decision {
	return s.HandleAt(frame, s.gateway.now())
}

// HandleAt processes one complete DoIP frame using now for idle-lease checks.
func (s *Session) HandleAt(frame []byte, now time.Time) Decision {
	header, payload, err := ParseFrame(frame, s.gateway.maxPayloadLength)
	if err != nil {
		return decisionForParseError(err)
	}
	return s.HandlePartsAt(header, payload, now)
}

// HandleParts processes a header and payload already read from a stream. It
// validates the header again so callers cannot bypass framing checks by
// constructing a Header directly.
func (s *Session) HandleParts(header Header, payload []byte) Decision {
	return s.HandlePartsAt(header, payload, s.gateway.now())
}

// HandlePartsAt processes an already-read header and payload using now for
// idle-lease checks.
func (s *Session) HandlePartsAt(header Header, payload []byte, now time.Time) Decision {
	if !header.ValidVersionInverse() {
		return Decision{Close: true, Reason: ReasonVersionInverseMismatch}
	}
	if header.PayloadLength > s.gateway.maxPayloadLength {
		return Decision{Close: true, Reason: ReasonPayloadTooLarge}
	}
	if header.PayloadLength != uint32(len(payload)) {
		return Decision{Close: true, Reason: ReasonMalformedFrame}
	}

	switch header.PayloadType {
	case PayloadTypeRoutingActivationRequest:
		return s.handleRoutingActivation(header.Version, payload, now)
	case PayloadTypeDiagnosticMessage:
		return s.handleDiagnostic(payload, now)
	default:
		return Decision{Close: true, Reason: ReasonUnsupportedPayloadType}
	}
}

func (s *Session) handleRoutingActivation(version byte, payload []byte, now time.Time) Decision {
	if len(payload) < 3 {
		return Decision{Close: true, Reason: ReasonMalformedActivationRequest}
	}
	source := binary.BigEndian.Uint16(payload[0:2])
	activationType := payload[2]

	s.gateway.listenerMu.Lock()
	defer s.gateway.listenerMu.Unlock()

	if s.closed {
		return Decision{Close: true, Reason: ReasonSessionClosed}
	}
	if s.activated {
		return rejectedActivation(
			version,
			source,
			s.gateway.entityAddress,
			RoutingActivationSourceAlreadyActive,
			ReasonAlreadyActivated,
		)
	}
	if activationType != ActivationTypeDefault && activationType != ActivationTypeWWHOBD {
		return rejectedActivation(
			version,
			source,
			s.gateway.entityAddress,
			RoutingActivationInvalidActivationType,
			ReasonInvalidActivationType,
		)
	}
	if _, registered := s.gateway.registry[source]; !registered {
		return rejectedActivation(
			version,
			source,
			s.gateway.entityAddress,
			RoutingActivationUnknownSourceAddress,
			ReasonUnknownSourceAddress,
		)
	}

	if lease, occupied := s.gateway.leases[source]; occupied {
		expired := s.gateway.idleTimeout > 0 &&
			now.Sub(lease.lastSeen) >= s.gateway.idleTimeout
		if !expired && !lease.session.closed && lease.session.activated {
			return rejectedActivation(
				version,
				source,
				s.gateway.entityAddress,
				RoutingActivationSourceAlreadyActive,
				ReasonSourceAddressInUse,
			)
		}

		// Reclaim the address and revoke the stale session before assigning it
		// to the new connection.
		lease.session.activated = false
		lease.session.sourceAddress = 0
		lease.session.closed = true
		delete(s.gateway.leases, source)
	}

	s.activated = true
	s.sourceAddress = source
	s.gateway.leases[source] = sourceLease{session: s, lastSeen: now}

	response := buildActivationResponse(
		version,
		source,
		s.gateway.entityAddress,
		RoutingActivationSuccess,
	)
	return Decision{
		Accepted:     true,
		Reason:       ReasonNone,
		Response:     response,
		ResponseCode: RoutingActivationSuccess,
	}
}

func (s *Session) handleDiagnostic(payload []byte, now time.Time) Decision {
	if len(payload) < 4 {
		return Decision{Close: true, Reason: ReasonMalformedFrame}
	}
	source := binary.BigEndian.Uint16(payload[0:2])
	target := binary.BigEndian.Uint16(payload[2:4])

	s.gateway.listenerMu.Lock()
	defer s.gateway.listenerMu.Unlock()

	if s.closed {
		return Decision{Close: true, Reason: ReasonSessionClosed}
	}
	if !s.activated {
		return Decision{Close: true, Reason: ReasonNotActivated}
	}
	if source != s.sourceAddress {
		return Decision{Close: true, Reason: ReasonDiagnosticSourceMismatch}
	}

	registration, registered := s.gateway.registry[source]
	if !registered {
		return Decision{Close: true, Reason: ReasonTargetAddressNotAuthorized}
	}
	if _, authorized := registration.authorizedTargets[target]; !authorized {
		return Decision{Close: true, Reason: ReasonTargetAddressNotAuthorized}
	}

	if lease, ok := s.gateway.leases[source]; ok && lease.session == s {
		lease.lastSeen = now
		s.gateway.leases[source] = lease
	}

	data := append([]byte(nil), payload[4:]...)
	return Decision{
		Accepted: true,
		Deliver:  true,
		Reason:   ReasonNone,
		Diagnostic: &DiagnosticMessage{
			SourceAddress: source,
			TargetAddress: target,
			Data:          data,
		},
	}
}

func (s *Session) releaseLeaseLocked() {
	if lease, ok := s.gateway.leases[s.sourceAddress]; ok && lease.session == s {
		delete(s.gateway.leases, s.sourceAddress)
	}
}

func decisionForParseError(err error) Decision {
	reason := ReasonMalformedFrame
	switch {
	case errors.Is(err, ErrVersionInverseMismatch):
		reason = ReasonVersionInverseMismatch
	case errors.Is(err, ErrPayloadTooLarge):
		reason = ReasonPayloadTooLarge
	}
	return Decision{Close: true, Reason: reason}
}

func rejectedActivation(version byte, source, entity uint16, code byte, reason Reason) Decision {
	return Decision{
		Reason:       reason,
		Response:     buildActivationResponse(version, source, entity, code),
		ResponseCode: code,
	}
}

func buildActivationResponse(version byte, source, entity uint16, code byte) []byte {
	payload := make([]byte, 9)
	binary.BigEndian.PutUint16(payload[0:2], source)
	binary.BigEndian.PutUint16(payload[2:4], entity)
	payload[4] = code
	return encodeFrame(version, PayloadTypeRoutingActivationResponse, payload)
}
