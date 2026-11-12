package netsec

import (
	"crypto/ed25519"
	"fmt"
	"io"
)

// PeerConfig configures a peer that automatically attaches and verifies
// Ed25519 identity payloads.
type PeerConfig struct {
	Role            Role
	StaticKeyPair   *StaticKeyPair
	IdentityKeyPair *IdentityKeyPair
	OnPeerIdentity  func(*PeerIdentity) error
	Random          io.Reader
}

// Config is an alias for PeerConfig.
type Config = PeerConfig

// Peer wraps a Noise XX handshake and publishes the remote identity only
// after the handshake completes and the payload verifies.
type Peer struct {
	state    *HandshakeState
	identity *IdentityKeyPair
	callback func(*PeerIdentity) error

	peerPayload  []byte
	peerIdentity *PeerIdentity
	finalizeErr  error
}

// NewPeer creates a peer for the configured XX role.
func NewPeer(config PeerConfig) (*Peer, error) {
	if config.IdentityKeyPair == nil {
		return nil, fmt.Errorf("%w: identity key pair is required", ErrInvalidKey)
	}
	if len(config.IdentityKeyPair.PublicKey) != IdentityPublicKeySize ||
		len(config.IdentityKeyPair.PrivateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKey
	}
	identity, err := NewIdentityKeyPair(config.IdentityKeyPair.PrivateKey)
	if err != nil {
		return nil, err
	}
	if !identity.PublicKey.Equal(config.IdentityKeyPair.PublicKey) {
		return nil, fmt.Errorf("%w: Ed25519 public key does not match private key", ErrInvalidKey)
	}

	peer := &Peer{
		identity: identity,
		callback: config.OnPeerIdentity,
	}
	builder := func(messageNumber int, bindingHash, staticPublicKey []byte) ([]byte, error) {
		if messageNumber != 2 && messageNumber != 3 {
			return nil, ErrUnexpectedHandshakeMessage
		}
		return SignIdentityPayload(bindingHash, staticPublicKey, peer.identity.PrivateKey)
	}
	state, err := NewHandshakeState(HandshakeConfig{
		Role:           config.Role,
		StaticKeyPair:  config.StaticKeyPair,
		PayloadBuilder: builder,
		Random:         config.Random,
	})
	if err != nil {
		return nil, err
	}
	peer.state = state
	return peer, nil
}

// NewInitiatorPeer is a convenience constructor for the initiating side.
func NewInitiatorPeer(
	static *StaticKeyPair,
	identity *IdentityKeyPair,
	onPeerIdentity func(*PeerIdentity) error,
) (*Peer, error) {
	return NewPeer(PeerConfig{
		Role:            Initiator,
		StaticKeyPair:   static,
		IdentityKeyPair: identity,
		OnPeerIdentity:  onPeerIdentity,
	})
}

// NewResponderPeer is a convenience constructor for the responding side.
func NewResponderPeer(
	static *StaticKeyPair,
	identity *IdentityKeyPair,
	onPeerIdentity func(*PeerIdentity) error,
) (*Peer, error) {
	return NewPeer(PeerConfig{
		Role:            Responder,
		StaticKeyPair:   static,
		IdentityKeyPair: identity,
		OnPeerIdentity:  onPeerIdentity,
	})
}

// Role returns the peer's handshake role.
func (p *Peer) Role() Role {
	if p == nil || p.state == nil {
		return Initiator
	}
	return p.state.Role()
}

// NextMessageNumber returns the next expected handshake message number.
func (p *Peer) NextMessageNumber() int {
	if p == nil || p.state == nil {
		return 0
	}
	return p.state.NextMessageNumber()
}

// Complete reports whether the handshake has completed. Completion alone
// does not imply that the peer identity was accepted; check PeerIdentity.
func (p *Peer) Complete() bool {
	return p != nil && p.state != nil && p.state.Complete()
}

// WriteMessage writes the next handshake message. The identity payload is
// attached automatically to messages two and three. Extra payload bytes are
// rejected so that message one can never carry an identity declaration.
func (p *Peer) WriteMessage(extra ...[]byte) ([]byte, error) {
	if p == nil || p.state == nil {
		return nil, ErrUnexpectedHandshakeMessage
	}
	var payload []byte
	if len(extra) > 0 {
		payload = extra[0]
	}
	if len(extra) > 1 {
		return nil, ErrUnexpectedHandshakeMessage
	}
	if len(payload) != 0 {
		if p.state.NextMessageNumber() == 1 {
			return nil, ErrFirstMessagePayload
		}
		return nil, ErrUnexpectedHandshakeMessage
	}
	message, err := p.state.WriteMessage(nil)
	if err != nil {
		return nil, err
	}
	if p.state.Complete() {
		if finalizeErr := p.finalizeIdentity(); finalizeErr != nil {
			// The message is still returned so the caller can finish the
			// wire exchange, but the application sees the verification
			// failure and no identity is published.
			return message, finalizeErr
		}
	}
	return message, nil
}

// ReadMessage consumes a handshake message and returns its plaintext
// payload. Message one returns nil. The payload is not an authenticated
// identity until Complete is true and PeerIdentity succeeds.
func (p *Peer) ReadMessage(message []byte) ([]byte, error) {
	if p == nil || p.state == nil {
		return nil, ErrUnexpectedHandshakeMessage
	}
	messageNumber := p.state.NextMessageNumber()
	payload, err := p.state.ReadMessage(message)
	if err != nil {
		return nil, err
	}
	if messageNumber == 1 {
		if len(payload) != 0 {
			return nil, ErrFirstMessagePayload
		}
	}
	if messageNumber == 2 || messageNumber == 3 {
		p.peerPayload = append([]byte(nil), payload...)
	}
	if p.state.Complete() {
		if finalizeErr := p.finalizeIdentity(); finalizeErr != nil {
			return payload, finalizeErr
		}
	}
	return payload, nil
}

func (p *Peer) finalizeIdentity() error {
	if p.peerIdentity != nil || p.finalizeErr != nil {
		return p.finalizeErr
	}
	identity, err := p.state.VerifyPeerIdentity(p.peerPayload)
	if err != nil {
		p.finalizeErr = err
		return err
	}
	p.peerIdentity = identity
	if p.callback != nil {
		if err := p.callback(identity); err != nil {
			p.peerIdentity = nil
			p.finalizeErr = err
			return err
		}
	}
	return nil
}

// PeerIdentity returns the verified remote identity. Before completion it
// returns ErrHandshakeIncomplete; after a failed verification it returns the
// verification error. The identity is never exposed to the application
// before the handshake completes.
func (p *Peer) PeerIdentity() (*PeerIdentity, error) {
	if p == nil || p.state == nil || !p.state.Complete() {
		return nil, ErrHandshakeIncomplete
	}
	if p.peerIdentity == nil {
		if p.finalizeErr != nil {
			return nil, p.finalizeErr
		}
		return nil, ErrHandshakeIncomplete
	}
	return p.peerIdentity, nil
}

// HandshakeHash returns the current or final Noise handshake hash.
func (p *Peer) HandshakeHash() []byte {
	if p == nil || p.state == nil {
		return nil
	}
	return p.state.HandshakeHash()
}

// PeerStaticPublicKey returns the peer's X25519 static public key once known.
func (p *Peer) PeerStaticPublicKey() []byte {
	if p == nil || p.state == nil {
		return nil
	}
	return p.state.PeerStaticPublicKey()
}
