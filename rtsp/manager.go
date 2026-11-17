// Package rtsp holds the server-side session state for an RTSP media server.
//
// The security properties enforced here are:
//
//   - Session identifiers are 128-bit values drawn from crypto/rand, so they
//     cannot be guessed from an observed identifier.
//   - Every session is bound to the authenticated principal that created it
//     and PLAY/TEARDOWN from any other principal fail with status 454.
//   - The media destination is pinned at SETUP time to the request source
//     address and the validated client_port pair; a Transport header that
//     names any other destination is rejected before a session exists.
//   - CSeq values must strictly increase within a session.
//   - Media packets are delivered only to the pinned target of a session
//     that has completed PLAY.
package rtsp

import (
	"net"
	"net/netip"
	"sync"
)

// PacketSink is the network abstraction used to emit media. The manager calls
// it only with the target pinned at SETUP time, never with an address taken
// from a later request.
type PacketSink interface {
	SendRTP(dst netip.AddrPort, packet []byte) error
}

// Manager owns all live RTSP sessions. Its methods are safe for concurrent
// use by many request goroutines.
type Manager struct {
	mu       sync.Mutex
	sessions map[SessionID]*Session
}

// NewManager creates an empty session manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[SessionID]*Session)}
}

// AddrFromNetAddr extracts the IP address of a connected peer, for example
// the RemoteAddr of the RTSP control connection that sent SETUP.
func AddrFromNetAddr(addr net.Addr) (netip.Addr, error) {
	switch v := addr.(type) {
	case *net.TCPAddr:
		return v.AddrPort().Addr().Unmap(), nil
	case *net.UDPAddr:
		return v.AddrPort().Addr().Unmap(), nil
	default:
		parsed, err := netip.ParseAddrPort(addr.String())
		if err != nil {
			return netip.Addr{}, errBadRequest
		}
		return parsed.Addr().Unmap(), nil
	}
}

// Setup validates a SETUP request and creates a session bound to the
// authenticated principal. source is the observed request source address
// and transportHeader the raw Transport header. The returned session's
// target is pinned to source and the validated client_port pair.
func (m *Manager) Setup(principal Principal, source netip.Addr, transportHeader string, cseq uint32) (*Session, error) {
	if principal == "" {
		return nil, errBadRequest
	}
	spec, err := ParseTransport(transportHeader, source)
	if err != nil {
		return nil, err
	}
	if cseq == 0 {
		return nil, errBadRequest
	}

	target, err := newTarget(source.Unmap(), spec.ClientPort)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// A 128-bit random collision is astronomically unlikely, but retrying
	// keeps the map invariant exact instead of silently overwriting.
	var id SessionID
	for {
		id, err = newSessionID()
		if err != nil {
			return nil, err
		}
		if _, exists := m.sessions[id]; !exists {
			break
		}
	}

	session := &Session{
		ID:       id,
		Owner:    principal,
		Target:   target,
		state:    stateReady,
		lastCSeq: cseq,
	}
	m.sessions[id] = session
	return cloneSession(session), nil
}

// Play validates a PLAY request: the session must exist and be owned by the
// requesting principal, and cseq must be greater than every earlier CSeq on
// this session. On success the session enters the playing state.
func (m *Manager) Play(principal Principal, id SessionID, cseq uint32) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.lookupLocked(principal, id)
	if err != nil {
		return nil, err
	}
	if err := session.checkCSeq(cseq); err != nil {
		return nil, err
	}
	session.state = statePlaying
	session.lastCSeq = cseq
	return cloneSession(session), nil
}

// Teardown validates a TEARDOWN request under the same ownership and CSeq
// rules as Play, then removes the session so no further media is delivered.
func (m *Manager) Teardown(principal Principal, id SessionID, cseq uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.lookupLocked(principal, id)
	if err != nil {
		return err
	}
	if err := session.checkCSeq(cseq); err != nil {
		return err
	}
	delete(m.sessions, id)
	return nil
}

// Lookup returns the session if it exists and is owned by principal; the
// same 454 error is returned otherwise so callers cannot probe ids.
func (m *Manager) Lookup(principal Principal, id SessionID) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.lookupLocked(principal, id)
	if err != nil {
		return nil, err
	}
	return cloneSession(session), nil
}

// Deliver sends one RTP packet for a session. Delivery only happens for a
// session that exists, is in the playing state, and the packet goes solely
// to the target pinned at SETUP time. A torn-down or foreign id fails with
// 454 and reaches the sink never.
func (m *Manager) Deliver(id SessionID, sink PacketSink, packet []byte) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return errSessionNotFound
	}
	target := session.Target
	playing := session.state == statePlaying
	m.mu.Unlock()

	if !playing {
		return errInvalidState
	}
	return sink.SendRTP(target.rtpAddrPort(), packet)
}

// lookupLocked enforces ownership. Unknown ids and foreign principals share
// one error response so session existence is not enumerable.
func (m *Manager) lookupLocked(principal Principal, id SessionID) (*Session, error) {
	session, ok := m.sessions[id]
	if !ok || session.Owner != principal {
		return nil, errSessionNotFound
	}
	return session, nil
}

// checkCSeq enforces strictly increasing CSeq values within a session.
func (s *Session) checkCSeq(cseq uint32) error {
	if cseq == 0 || cseq <= s.lastCSeq {
		return errBadRequest
	}
	return nil
}

func newTarget(source netip.Addr, rtpPort uint16) (Target, error) {
	if !source.IsValid() {
		return Target{}, errBadRequest
	}
	return Target{
		Addr:     source.As16(),
		IsV4:     source.Is4(),
		RTPPort:  rtpPort,
		RTCPPort: rtpPort + 1,
	}, nil
}

// rtpAddrPort reconstructs the sole permitted RTP destination.
func (t Target) rtpAddrPort() netip.AddrPort {
	addr := netip.AddrFrom16(t.Addr)
	if t.IsV4 {
		addr = addr.Unmap()
	}
	return netip.AddrPortFrom(addr, t.RTPPort)
}

// cloneSession returns a value copy so callers cannot mutate live state.
func cloneSession(s *Session) *Session {
	sessionCopy := *s
	return &sessionCopy
}
