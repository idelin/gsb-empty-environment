package sctpauth

import (
	"sync"
	"time"
)

// Option customizes a Server.
type Option func(*serverConfig)

type serverConfig struct {
	cookieLifespan time.Duration
	windowSize     uint32
	clock          func() time.Time
}

// WithCookieLifespan sets the State Cookie validity window.
func WithCookieLifespan(lifespan time.Duration) Option {
	return func(c *serverConfig) { c.cookieLifespan = lifespan }
}

// WithReceiveWindow sets the DATA chunk receive window width.
func WithReceiveWindow(size uint32) Option {
	return func(c *serverConfig) { c.windowSize = size }
}

// withClock injects a clock; used by deterministic tests.
func withClock(now func() time.Time) Option {
	return func(c *serverConfig) { c.clock = now }
}

// Server is the transport-free SCTP security endpoint. It performs stateless
// cookie issuance/verification and owns the table of established associations.
type Server struct {
	mu       sync.Mutex
	keys     *KeyRing
	signer   *CookieSigner
	verifier *CookieVerifier
	window   uint32
	assocs   map[uint32]*Association
	now      func() time.Time
}

// NewServer constructs a Server from server seed entropy and options.
func NewServer(seed []byte, opts ...Option) *Server {
	cfg := serverConfig{
		cookieLifespan: defaultCookieLifespan,
		windowSize:     DefaultReceiveWindow,
		clock:          time.Now,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	keys := NewKeyRing(seed)
	signer := NewCookieSigner(keys).withClock(cfg.clock)
	signer.SetLifespan(cfg.cookieLifespan)

	return &Server{
		keys:     keys,
		signer:   signer,
		verifier: NewCookieVerifier(keys).withClock(cfg.clock),
		window:   cfg.windowSize,
		assocs:   make(map[uint32]*Association),
		now:      cfg.clock,
	}
}

// RotateKeys performs a two-generation key rotation. Cookies issued under the
// previous generation remain verifiable until they expire.
func (s *Server) RotateKeys(rotationSecret []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys.Rotate(rotationSecret)
}

// HandleINIT is the first handshake step. It validates the INIT and returns the
// INIT-ACK parameters including a signed State Cookie. The server stores no
// handshake state.
func (s *Server) HandleINIT(req InitRequest) (*InitAckResponse, error) {
	if req.InitiateTag == 0 {
		return nil, ErrForbiddenTag
	}
	if req.SourceAddr == "" {
		return nil, ErrSourceAddress
	}

	peerAddrs := append([]string{req.SourceAddr}, req.AnnouncedAddrs...)
	myTag := s.keys.randUint32()
	if myTag == 0 {
		myTag = 1
	}
	myTSN := s.keys.randUint32()

	cookie, wire, err := s.signer.Issue(HandshakeParams{
		InitiateTag: req.InitiateTag,
		MyTag:       myTag,
		InitiateTSN: req.InitiateTSN,
		MyTSN:       myTSN,
		PeerAddrs:   peerAddrs,
	})
	if err != nil {
		return nil, err
	}

	return &InitAckResponse{
		MyTag:       myTag,
		MyTSN:       myTSN,
		Cookie:      wire,
		CookieState: cookie,
	}, nil
}

// HandleCookieEcho is the third handshake step. On success it establishes (or
// returns the existing) association and produces the parameters needed for
// COOKIE-ACK. Replays, forgeries and duplicate associations are rejected.
func (s *Server) HandleCookieEcho(echo CookieEcho) (*Association, *EstablishedResult, error) {
	cookie, err := s.verifier.Verify(echo)
	if err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.assocs[cookie.InitiateTag]; ok {
		return existing, nil, ErrAssociationExists
	}

	primary := canonicalAddr(echo.SourceAddr)
	paths := NewPathManager(s.keys, cookie.PeerAddrs, true)
	assoc := &Association{
		localTag:  cookie.MyTag,
		peerTag:   cookie.InitiateTag,
		localTSN:  cookie.MyTSN,
		peerTSN:   cookie.InitiateTSN,
		paths:     paths,
		window:    NewReceiveWindow(cookie.InitiateTSN, s.window),
		createdAt: s.now().UTC(),
	}
	s.assocs[cookie.InitiateTag] = assoc

	result := &EstablishedResult{
		PeerTag:         cookie.InitiateTag,
		LocalTag:        cookie.MyTag,
		PeerInitialTSN:  cookie.InitiateTSN,
		LocalInitialTSN: cookie.MyTSN,
		PrimaryAddr:     primary,
	}
	return assoc, result, nil
}

// Association looks up an established association by the peer Verification Tag.
func (s *Server) Association(peerTag uint32) (*Association, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assoc, ok := s.assocs[peerTag]
	if !ok {
		return nil, ErrUnknownAssociation
	}
	return assoc, nil
}

// HandleEstablishedPacket dispatches an inbound packet to its association,
// enforcing the exact Verification Tag before any chunk processing.
func (s *Server) HandleEstablishedPacket(packet Packet) error {
	s.mu.Lock()
	assoc, ok := s.assocs[packet.VerificationTag]
	s.mu.Unlock()
	if !ok {
		return ErrUnknownAssociation
	}
	return assoc.HandlePacket(packet)
}

// RemoveAssociation tears down an association by peer tag.
func (s *Server) RemoveAssociation(peerTag uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.assocs, peerTag)
}

// AssociationCount returns the number of live associations.
func (s *Server) AssociationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.assocs)
}
