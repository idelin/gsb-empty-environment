package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultSessionTTL is the RTSP Session header timeout.
	DefaultSessionTTL = 60 * time.Minute
	// DefaultMaxSessions bounds unauthenticated SETUP allocation.
	DefaultMaxSessions = 10000
)

// Option configures a Manager.
type Option func(*Manager)

// WithSessionTTL changes the session lifetime advertised in the Session header.
func WithSessionTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.ttl = ttl
		}
	}
}

// WithMaxSessions bounds the number of concurrently allocated sessions.
func WithMaxSessions(max int) Option {
	return func(m *Manager) {
		if max > 0 {
			m.maxSessions = max
		}
	}
}

// WithRandomSource replaces crypto/rand.Reader, primarily for deterministic tests.
func WithRandomSource(reader io.Reader) Option {
	return func(m *Manager) {
		if reader != nil {
			m.random = reader
		}
	}
}

// WithClock replaces the wall clock used for session expiry.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// MediaTarget is the immutable media destination fixed by SETUP.
type MediaTarget struct {
	Destination     netip.Addr
	RTPPort         uint16
	RTCPPort        uint16
	InterleavedRTP  uint8
	InterleavedRTCP uint8
	TCP             bool
}

// Manager owns RTSP sessions and their validated media destinations.
type Manager struct {
	initOnce    sync.Once
	mu          sync.Mutex
	sessions    map[string]*sessionState
	random      io.Reader
	now         func() time.Time
	ttl         time.Duration
	maxSessions int
}

type sessionState struct {
	id        string
	principal string
	source    netip.Addr
	target    MediaTarget
	cseq      uint32
	playing   bool
	expires   time.Time
}

// NewManager constructs an RTSP session manager.
func NewManager(options ...Option) *Manager {
	m := &Manager{}
	m.init()
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	return m
}

// NewSessionManager is an alias for NewManager.
func NewSessionManager(options ...Option) *Manager {
	return NewManager(options...)
}

func (m *Manager) init() {
	m.initOnce.Do(func() {
		m.sessions = make(map[string]*sessionState)
		m.random = rand.Reader
		m.now = time.Now
		m.ttl = DefaultSessionTTL
		m.maxSessions = DefaultMaxSessions
	})
}

// Setup validates Transport and source binding, then allocates a 128-bit session ID.
func (m *Manager) Setup(principal, remoteAddr string, cseq uint32, transport string) Response {
	m.init()
	if strings.TrimSpace(principal) == "" {
		return newResponse(StatusUnauthorized, cseq)
	}
	source, err := parseSourceAddress(remoteAddr)
	if err != nil {
		return badRequest(cseq, err.Error())
	}
	spec, err := ParseTransport(transport, source)
	if err != nil {
		return badRequest(cseq, err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.removeExpiredLocked()
	if len(m.sessions) >= m.maxSessions {
		return newResponse(StatusUnavailable, cseq)
	}

	id, err := m.newSessionIDLocked()
	if err != nil {
		return newResponse(StatusInternalError, cseq)
	}

	target := mediaTarget(spec, source)
	m.sessions[id] = &sessionState{
		id:        id,
		principal: principal,
		source:    source,
		target:    target,
		cseq:      cseq,
		expires:   m.now().Add(m.ttl),
	}

	response := newResponse(StatusOK, cseq)
	response.SessionID = id
	response.SessionTimeout = int(m.ttl / time.Second)
	response.Target = cloneTarget(target)
	response.Headers.Set("Session", fmt.Sprintf("%s;timeout=%d", id, response.SessionTimeout))
	return response
}

// Play transitions an owned session to PLAYING and returns its fixed media target.
func (m *Manager) Play(principal, remoteAddr, sessionID string, cseq uint32) Response {
	m.init()
	_ = remoteAddr
	sessionID = sessionIDOnly(sessionID)
	if sessionID == "" {
		return sessionNotFound(cseq)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return sessionNotFound(cseq)
	}
	if m.expiredLocked(session) {
		delete(m.sessions, sessionID)
		return sessionNotFound(cseq)
	}
	if principal == "" || session.principal != principal {
		return sessionNotFound(cseq)
	}
	if cseq <= session.cseq {
		return badRequest(cseq, "CSeq must strictly increase within a session")
	}

	session.cseq = cseq
	session.playing = true
	session.expires = m.now().Add(m.ttl)

	response := newResponse(StatusOK, cseq)
	response.SessionID = session.id
	response.SessionTimeout = int(m.ttl / time.Second)
	response.Target = cloneTarget(session.target)
	response.Headers.Set("Session", fmt.Sprintf("%s;timeout=%d", session.id, response.SessionTimeout))
	return response
}

// Teardown removes a session owned by principal.
func (m *Manager) Teardown(principal, remoteAddr, sessionID string, cseq uint32) Response {
	m.init()
	_ = remoteAddr
	sessionID = sessionIDOnly(sessionID)
	if sessionID == "" {
		return sessionNotFound(cseq)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return sessionNotFound(cseq)
	}
	if m.expiredLocked(session) {
		delete(m.sessions, sessionID)
		return sessionNotFound(cseq)
	}
	if principal == "" || session.principal != principal {
		return sessionNotFound(cseq)
	}
	if cseq <= session.cseq {
		return badRequest(cseq, "CSeq must strictly increase within a session")
	}

	session.cseq = cseq
	delete(m.sessions, sessionID)
	return newResponse(StatusOK, cseq)
}

// Handle dispatches an already authenticated RTSP request.
func (m *Manager) Handle(request *Request) Response {
	m.init()
	if request == nil {
		return badRequest(0, "nil request")
	}
	if strings.TrimSpace(request.Principal) == "" {
		return newResponse(StatusUnauthorized, request.CSeq)
	}

	switch strings.ToUpper(request.Method) {
	case "OPTIONS":
		response := newResponse(StatusOK, request.CSeq)
		response.Headers.Set("Public", "OPTIONS, SETUP, PLAY, TEARDOWN")
		return response
	case "SETUP":
		if strings.TrimSpace(request.Session) != "" {
			return badRequest(request.CSeq, "SETUP must not carry a Session header")
		}
		return m.Setup(request.Principal, request.RemoteAddr, request.CSeq, request.Transport)
	case "PLAY":
		return m.Play(request.Principal, request.RemoteAddr, request.Session, request.CSeq)
	case "TEARDOWN":
		return m.Teardown(request.Principal, request.RemoteAddr, request.Session, request.CSeq)
	default:
		return newResponse(StatusMethodNotAllowed, request.CSeq)
	}
}

// HandleRaw parses and dispatches one authenticated RTSP wire request.
func (m *Manager) HandleRaw(principal, remoteAddr, raw string) Response {
	request, err := ParseRequest(raw)
	if err != nil {
		return badRequest(0, err.Error())
	}
	request.Principal = principal
	request.RemoteAddr = remoteAddr
	return m.Handle(request)
}

// Close drops all sessions.
func (m *Manager) Close() {
	m.init()
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.sessions)
}

// WriteRTP sends one RTP payload only to the destination fixed by SETUP.
func (m *Manager) WriteRTP(conn net.PacketConn, principal, sessionID string, payload []byte) error {
	return m.writeUDP(conn, principal, sessionID, payload, false)
}

// WriteRTCP sends one RTCP payload only to the destination fixed by SETUP.
func (m *Manager) WriteRTCP(conn net.PacketConn, principal, sessionID string, payload []byte) error {
	return m.writeUDP(conn, principal, sessionID, payload, true)
}

func (m *Manager) writeUDP(conn net.PacketConn, principal, sessionID string, payload []byte, rtcp bool) error {
	m.init()
	if conn == nil {
		return ErrInvalidMediaWriter
	}
	sessionID = sessionIDOnly(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}

	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if m.expiredLocked(session) {
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if principal == "" || session.principal != principal {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if !session.playing {
		m.mu.Unlock()
		return ErrNotPlaying
	}
	target := session.target
	m.mu.Unlock()

	if target.TCP {
		return ErrNoUDPTransport
	}
	port := target.RTPPort
	if rtcp {
		port = target.RTCPPort
	}
	addr := netAddr(target.Destination, port)
	_, err := conn.WriteTo(payload, addr)
	return err
}

func mediaTarget(spec TransportSpec, source netip.Addr) MediaTarget {
	target := MediaTarget{
		Destination: source,
		TCP:         spec.TCP,
	}
	if spec.TCP {
		target.InterleavedRTP = uint8(spec.Interleaved.RTP)
		target.InterleavedRTCP = uint8(spec.Interleaved.RTCP)
		return target
	}
	target.RTPPort = spec.ClientPort.RTP
	target.RTCPPort = spec.ClientPort.RTCP
	return target
}

func (m *Manager) newSessionIDLocked() (string, error) {
	var raw [16]byte
	for range 4 {
		if _, err := io.ReadFull(m.random, raw[:]); err != nil {
			return "", err
		}
		id := hex.EncodeToString(raw[:])
		if _, exists := m.sessions[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("could not allocate a unique session ID")
}

func (m *Manager) removeExpiredLocked() {
	now := m.now()
	for id, session := range m.sessions {
		if !session.expires.IsZero() && !now.Before(session.expires) {
			delete(m.sessions, id)
		}
	}
}

func (m *Manager) expiredLocked(session *sessionState) bool {
	return !session.expires.IsZero() && !m.now().Before(session.expires)
}

func sessionIDOnly(session string) string {
	session = strings.TrimSpace(session)
	if session == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(session, ";", 2)[0])
}

func cloneTarget(target MediaTarget) *MediaTarget {
	copyTarget := target
	return &copyTarget
}

func netAddr(addr netip.Addr, port uint16) *net.UDPAddr {
	return &net.UDPAddr{
		IP:   addr.AsSlice(),
		Port: int(port),
		Zone: addr.Zone(),
	}
}
