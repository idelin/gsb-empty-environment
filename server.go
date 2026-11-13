package netsec

import (
	"bytes"
	"crypto/rand"
	"encoding/gob"
	"sync"
)

// ServerStore persists the server state atomically. Commit must be
// all-or-nothing: a crash before Commit completes leaves the previous
// snapshot intact.
type ServerStore interface {
	LoadServerState() ([]byte, error)
	CommitServerState([]byte) error
}

// storedReply is the JoinAccept produced for the last accepted request,
// kept so an identical retransmitted JoinRequest (response lost in
// transit) can be answered with the original JoinAccept.
type storedReply struct {
	DevNonce uint16
	MIC      [4]byte
	Accept   JoinAccept
}

// serverDeviceState is the persistent per-device join state.
type serverDeviceState struct {
	SeenDevNonces map[uint16]bool
	MaxJoinNonce  uint32
	SessionGen    uint64
	Pending       *storedReply
}

func (s *serverDeviceState) clone() *serverDeviceState {
	out := &serverDeviceState{
		SeenDevNonces: make(map[uint16]bool, len(s.SeenDevNonces)),
		MaxJoinNonce:  s.MaxJoinNonce,
		SessionGen:    s.SessionGen,
	}
	for n := range s.SeenDevNonces {
		out.SeenDevNonces[n] = true
	}
	if s.Pending != nil {
		p := *s.Pending
		p.Accept.Ciphertext = append([]byte(nil), s.Pending.Accept.Ciphertext...)
		out.Pending = &p
	}
	return out
}

type serverState struct {
	Devices map[[8]byte]*serverDeviceState
}

func encodeServerState(s *serverState) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeServerState(b []byte) (*serverState, error) {
	s := &serverState{}
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(s); err != nil {
		return nil, err
	}
	if s.Devices == nil {
		s.Devices = make(map[[8]byte]*serverDeviceState)
	}
	for _, ds := range s.Devices {
		if ds.SeenDevNonces == nil {
			ds.SeenDevNonces = make(map[uint16]bool)
		}
	}
	return s, nil
}

// ServerOption customises a Server.
type ServerOption func(*Server)

// WithJoinNonceLimit lowers the JoinNonce ceiling (test-only knob for
// exercising exhaustion without 2^24 joins). It can never exceed the
// 24-bit maximum.
func WithJoinNonceLimit(limit uint32) ServerOption {
	return func(s *Server) {
		if limit < s.joinNonceLimit {
			s.joinNonceLimit = limit
		}
	}
}

// Server is the join server network side.
type Server struct {
	mu             sync.Mutex
	reg            *Registry
	ks             *KeyStore
	st             ServerStore
	net            uint32
	joinNonceLimit uint32

	state *serverState

	// OnJoinAllocated, when set, is invoked after a successful commit with
	// each freshly allocated JoinNonce. Test hook.
	OnJoinAllocated func(devEUI [8]byte, joinNonce uint32, sessionGen uint64)
}

// NewServer loads persistent state from the store (empty on first boot).
func NewServer(reg *Registry, ks *KeyStore, st ServerStore, netID uint32, opts ...ServerOption) (*Server, error) {
	s := &Server{
		reg:            reg,
		ks:             ks,
		st:             st,
		net:            netID & 0xFFFFFF,
		joinNonceLimit: JoinNonceMax24,
	}
	for _, o := range opts {
		o(s)
	}
	raw, err := st.LoadServerState()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		s.state = &serverState{Devices: make(map[[8]byte]*serverDeviceState)}
	} else {
		s.state, err = decodeServerState(raw)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Join processes one JoinRequest.
//
// The MIC callback runs over the canonical request bytes first. Only
// after it passes does the server atomically claim the DevNonce, allocate
// a strictly increasing 24-bit JoinNonce and persist everything in one
// commit. An identical retransmission of the last accepted request
// returns the original JoinAccept without consuming anything new.
func (s *Server) Join(req *JoinRequest) (*JoinAccept, error) {
	dev, ok := s.reg.Lookup(req.DevEUI)
	if !ok {
		return nil, ErrUnknownDevice
	}
	if dev.JoinEUI != req.JoinEUI {
		return nil, ErrJoinEUIMismatch
	}
	mic, err := dev.MIC(req.Canonical())
	if err != nil {
		return nil, err
	}
	if mic != req.MIC {
		return nil, ErrMICMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ds := s.state.Devices[req.DevEUI]
	if ds == nil {
		ds = &serverDeviceState{SeenDevNonces: make(map[uint16]bool)}
	}

	// Idempotent replay: byte-identical request returns the original
	// JoinAccept. A matching DevNonce with a different MIC is rejected.
	if p := ds.Pending; p != nil && p.DevNonce == req.DevNonce {
		if p.MIC == req.MIC {
			cp := p.Accept
			cp.Ciphertext = append([]byte(nil), p.Accept.Ciphertext...)
			return &cp, nil
		}
		return nil, ErrDevNonceReused
	}
	if ds.SeenDevNonces[req.DevNonce] {
		return nil, ErrDevNonceReused
	}
	if ds.MaxJoinNonce >= s.joinNonceLimit {
		return nil, ErrJoinNonceExhausted
	}

	joinNonce := ds.MaxJoinNonce + 1
	sessionGen := ds.SessionGen + 1
	var sessionID [16]byte
	if _, err := rand.Read(sessionID[:]); err != nil {
		return nil, err
	}

	body := &joinAcceptBody{
		JoinNonce:  joinNonce,
		NetID:      s.net,
		DevEUI:     req.DevEUI,
		DevNonce:   req.DevNonce,
		SessionID:  sessionID,
		SessionGen: sessionGen,
	}
	// Encrypt first, then authenticate the ciphertext via the callback.
	ct, err := s.ks.Encrypt(dev.RootKey, body.marshal())
	if err != nil {
		return nil, err
	}
	tag, err := dev.MIC(ct)
	if err != nil {
		return nil, err
	}
	accept := &JoinAccept{Ciphertext: ct, MIC: tag}

	next := ds.clone()
	next.SeenDevNonces[req.DevNonce] = true
	next.MaxJoinNonce = joinNonce
	next.SessionGen = sessionGen
	next.Pending = &storedReply{DevNonce: req.DevNonce, MIC: req.MIC, Accept: *accept}

	newState := &serverState{Devices: make(map[[8]byte]*serverDeviceState, len(s.state.Devices)+1)}
	for k, v := range s.state.Devices {
		newState.Devices[k] = v
	}
	newState.Devices[req.DevEUI] = next

	raw, err := encodeServerState(newState)
	if err != nil {
		return nil, err
	}
	// Atomic commit: only swap the in-memory state after the store
	// durably accepted the new snapshot.
	if err := s.st.CommitServerState(raw); err != nil {
		return nil, err
	}
	s.state = newState

	if s.OnJoinAllocated != nil {
		s.OnJoinAllocated(req.DevEUI, joinNonce, sessionGen)
	}
	return accept, nil
}

// JoinNonceUsed reports the highest JoinNonce allocated to a device.
func (s *Server) JoinNonceUsed(devEUI [8]byte) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ds := s.state.Devices[devEUI]; ds != nil {
		return ds.MaxJoinNonce
	}
	return 0
}
