package netsec

import (
	"crypto/rand"
	"time"
)

// DefaultKeyBlockLength is the key block size assumed when a caller leaves the
// negotiated length unset. It covers two 20-byte MAC keys and two 16-byte
// AES-128 keys, as used by common CBC/GCM TLS 1.2 suites.
const DefaultKeyBlockLength = 40

// HandshakeInput describes the negotiated inputs shared by every TLS 1.2 full
// handshake after the key exchange has produced a pre-master secret.
type HandshakeInput struct {
	PreMasterSecret     []byte
	ClientRandom        []byte
	ServerRandom        []byte
	Transcript          []byte
	Hash                HashFunc
	KeyBlockLength      int
	ClientAdvertisesEMS bool
	ServerAdvertisesEMS bool
	Lifetime            time.Duration
	Now                 time.Time
}

func (in *HandshakeInput) keyBlockLength() int {
	if in.KeyBlockLength > 0 {
		return in.KeyBlockLength
	}
	return DefaultKeyBlockLength
}

func (in *HandshakeInput) now() time.Time {
	if in.Now.IsZero() {
		return time.Now()
	}
	return in.Now
}

// FullHandshakeResult carries the secrets and negotiated EMS state produced
// by a full handshake.
type FullHandshakeResult struct {
	MasterSecret         []byte
	KeyBlock             []byte
	ClientVerifyData     []byte
	ServerVerifyData     []byte
	ExtendedMasterSecret bool
}

func deriveFullHandshake(in *HandshakeInput) (*FullHandshakeResult, error) {
	extended, err := NegotiateEMS(in.ClientAdvertisesEMS, in.ServerAdvertisesEMS)
	if err != nil {
		return nil, err
	}
	masterSecret := DeriveMasterSecret(in.Hash, in.PreMasterSecret, in.ClientRandom, in.ServerRandom, in.Transcript, extended)
	keyBlock := DeriveKeyBlock(in.Hash, masterSecret, in.ClientRandom, in.ServerRandom, in.keyBlockLength())
	return &FullHandshakeResult{
		MasterSecret:         masterSecret,
		KeyBlock:             keyBlock,
		ClientVerifyData:     VerifyData(in.Hash, masterSecret, in.Transcript, true),
		ServerVerifyData:     VerifyData(in.Hash, masterSecret, in.Transcript, false),
		ExtendedMasterSecret: extended,
	}, nil
}

// Server orchestrates full handshakes and resumptions on the server side.
type Server struct {
	Store   SessionStore
	Tickets *TicketCipher
}

// FullHandshake enforces EMS negotiation, derives the master secret and key
// block, persists the authoritative session state and returns a sealed
// resumption ticket.
func (s *Server) FullHandshake(in *HandshakeInput) (*FullHandshakeResult, []byte, error) {
	if s.Store == nil || s.Tickets == nil {
		return nil, nil, ErrSessionUnknown
	}
	result, err := deriveFullHandshake(in)
	if err != nil {
		return nil, nil, err
	}

	sessionID := make([]byte, 16)
	if _, err := rand.Read(sessionID); err != nil {
		return nil, nil, err
	}
	session := &Session{
		ID:                   sessionID,
		MasterSecret:         append([]byte(nil), result.MasterSecret...),
		Hash:                 in.Hash,
		KeyBlockLength:       in.keyBlockLength(),
		ExtendedMasterSecret: result.ExtendedMasterSecret,
		CreatedAt:            in.now(),
		Lifetime:             in.Lifetime,
	}
	if err := s.Store.Put(session); err != nil {
		return nil, nil, err
	}
	ticket, err := s.Tickets.Seal(&TicketData{SessionID: sessionID})
	if err != nil {
		_ = s.Store.Delete(sessionID)
		return nil, nil, err
	}
	return result, ticket, nil
}

// ResumeInput carries the resumption hello and the opaque ticket presented by
// the client.
type ResumeInput struct {
	Ticket              []byte
	ClientRandom        []byte
	ServerRandom        []byte
	Transcript          []byte
	ClientAdvertisesEMS bool
	ServerAdvertisesEMS bool
}

// ResumeHandshakeResult contains the reused master secret and freshly derived
// key block for a resumed connection.
type ResumeHandshakeResult struct {
	MasterSecret         []byte
	KeyBlock             []byte
	ServerVerifyData     []byte
	ExtendedMasterSecret bool
}

// Resume enforces the EMS resumption policy against the authoritative stored
// session, then reuses the original master secret to derive fresh keying
// material. No master secret is returned on a policy failure.
func (s *Server) Resume(in *ResumeInput) (*ResumeHandshakeResult, error) {
	if s.Store == nil || s.Tickets == nil {
		return nil, ErrSessionUnknown
	}
	data, err := s.Tickets.Open(in.Ticket)
	if err != nil {
		return nil, err
	}
	session, err := s.Store.Get(data.SessionID)
	if err != nil {
		return nil, err
	}
	negotiated, err := NegotiateEMS(in.ClientAdvertisesEMS, in.ServerAdvertisesEMS)
	if err != nil {
		return nil, err
	}
	if err := ValidateResumption(in.ClientAdvertisesEMS, in.ServerAdvertisesEMS, session); err != nil {
		return nil, err
	}

	keyBlock := DeriveKeyBlock(session.Hash, session.MasterSecret, in.ClientRandom, in.ServerRandom, session.KeyBlockLength)
	return &ResumeHandshakeResult{
		MasterSecret:         append([]byte(nil), session.MasterSecret...),
		KeyBlock:             keyBlock,
		ServerVerifyData:     VerifyData(session.Hash, session.MasterSecret, in.Transcript, false),
		ExtendedMasterSecret: negotiated,
	}, nil
}

// ClientFullHandshakeResult pairs a full handshake result with the local
// client session record used for later resumption.
type ClientFullHandshakeResult struct {
	*FullHandshakeResult
	Session *ClientSession
}

// FullHandshake mirrors the server derivation and records the ticket together
// with the authoritative EMS flag in the local client cache.
func (c *MemoryClientSessionCache) FullHandshake(name string, ticket []byte, in *HandshakeInput) (*ClientFullHandshakeResult, error) {
	if c == nil {
		return nil, ErrSessionUnknown
	}
	result, err := deriveFullHandshake(in)
	if err != nil {
		return nil, err
	}
	session := &ClientSession{
		Ticket:               append([]byte(nil), ticket...),
		MasterSecret:         append([]byte(nil), result.MasterSecret...),
		Hash:                 in.Hash,
		KeyBlockLength:       in.keyBlockLength(),
		ExtendedMasterSecret: result.ExtendedMasterSecret,
	}
	c.Put(name, session)
	return &ClientFullHandshakeResult{FullHandshakeResult: result, Session: session}, nil
}

// ClientResumeInput carries the client's resumption hello.
type ClientResumeInput struct {
	ClientAdvertisesEMS bool
	ServerAdvertisesEMS bool
	ClientRandom        []byte
	ServerRandom        []byte
	Transcript          []byte
}

// Resume validates the resumption against the locally cached EMS flag, reuses
// the master secret and derives fresh keying material.
func (c *MemoryClientSessionCache) Resume(name string, in *ClientResumeInput) (*ResumeHandshakeResult, error) {
	session, ok := c.Get(name)
	if !ok {
		return nil, ErrSessionUnknown
	}
	negotiated, err := NegotiateEMS(in.ClientAdvertisesEMS, in.ServerAdvertisesEMS)
	if err != nil {
		return nil, err
	}
	if err := ValidateResumptionFlag(in.ClientAdvertisesEMS, in.ServerAdvertisesEMS, session.ExtendedMasterSecret); err != nil {
		return nil, err
	}
	keyBlock := DeriveKeyBlock(session.Hash, session.MasterSecret, in.ClientRandom, in.ServerRandom, session.KeyBlockLength)
	return &ResumeHandshakeResult{
		MasterSecret:         append([]byte(nil), session.MasterSecret...),
		KeyBlock:             keyBlock,
		ServerVerifyData:     VerifyData(session.Hash, session.MasterSecret, in.Transcript, false),
		ExtendedMasterSecret: negotiated,
	}, nil
}
