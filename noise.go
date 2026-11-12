package netsec

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

const (
	// ProtocolName is the concrete Noise protocol implemented here.
	ProtocolName = "Noise_XX_25519_ChaChaPoly_SHA256"

	message1Size = StaticKeySize
)

// Role identifies the party that starts or answers an XX handshake.
type Role uint8

const (
	// Initiator writes messages one and three and reads message two.
	Initiator Role = iota
	// Responder reads message one and writes message two.
	Responder
)

// PayloadBuilder is called after all static key material for a message has
// been mixed into the handshake hash and before the payload is encrypted.
// The supplied hash is the binding hash for this message.
type PayloadBuilder func(messageNumber int, bindingHash, staticPublicKey []byte) ([]byte, error)

// HandshakeConfig configures an XX handshake.
type HandshakeConfig struct {
	Role           Role
	StaticKeyPair  *StaticKeyPair
	PayloadBuilder PayloadBuilder
	Random         io.Reader
}

// HandshakeState is a single Noise_XX_25519_ChaChaPoly_SHA256 handshake.
//
// The state machine intentionally exposes the raw handshake payload. Callers
// must authenticate it with VerifyPeerIdentity (or
// ValidateIdentityPayloadAtCompletion) before treating it as an identity.
type HandshakeState struct {
	role    Role
	static  *StaticKeyPair
	random  io.Reader
	builder PayloadBuilder

	h     [HashSize]byte
	ck    [HashSize]byte
	key   []byte
	nonce uint32

	localEphemeral  *ecdh.PrivateKey
	remoteEphemeral []byte
	remoteStatic    []byte

	nextMessage   int
	completed     bool
	payloadHashes [5][]byte
	lastPayload   []byte
}

// NewHandshakeState creates a handshake in its initial state.
func NewHandshakeState(config HandshakeConfig) (*HandshakeState, error) {
	if config.StaticKeyPair == nil {
		return nil, fmt.Errorf("%w: static key pair is required", ErrInvalidKey)
	}
	if _, err := config.StaticKeyPair.public(); err != nil {
		return nil, err
	}
	if _, err := config.StaticKeyPair.private(); err != nil {
		return nil, err
	}
	if config.Role != Initiator && config.Role != Responder {
		return nil, fmt.Errorf("netsec: invalid handshake role %d", config.Role)
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	h := sha256.Sum256([]byte(ProtocolName))
	state := &HandshakeState{
		role:        config.Role,
		static:      config.StaticKeyPair.clone(),
		random:      config.Random,
		builder:     config.PayloadBuilder,
		h:           h,
		ck:          h,
		nextMessage: 1,
	}
	return state, nil
}

// Role returns the handshake role.
func (s *HandshakeState) Role() Role {
	if s == nil {
		return Initiator
	}
	return s.role
}

// NextMessageNumber returns the one-based number of the next handshake
// message that this peer expects to write or read.
func (s *HandshakeState) NextMessageNumber() int {
	if s == nil {
		return 0
	}
	return s.nextMessage
}

// Complete reports whether the three-message handshake has finished.
func (s *HandshakeState) Complete() bool {
	return s != nil && s.completed
}

// HandshakeHash returns the current handshake hash h. After completion this
// is the final Noise handshake hash.
func (s *HandshakeState) HandshakeHash() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.h[:]...)
}

// PayloadBindingHash returns the handshake hash at the point where the
// payload in messageNumber was (or will be) encrypted. messageNumber must be
// two or three.
func (s *HandshakeState) PayloadBindingHash(messageNumber int) []byte {
	if s == nil || messageNumber < 0 || messageNumber >= len(s.payloadHashes) {
		return nil
	}
	return append([]byte(nil), s.payloadHashes[messageNumber]...)
}

// PeerPayloadBindingHash returns the binding hash for the identity payload
// that authenticates the peer: message two for an initiator and message
// three for a responder.
func (s *HandshakeState) PeerPayloadBindingHash() []byte {
	if s == nil {
		return nil
	}
	messageNumber := 2
	if s.role == Responder {
		messageNumber = 3
	}
	return s.PayloadBindingHash(messageNumber)
}

// PeerStaticPublicKey returns the peer's X25519 static public key once it
// has been received.
func (s *HandshakeState) PeerStaticPublicKey() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.remoteStatic...)
}

// LocalStaticPublicKey returns this peer's X25519 static public key.
func (s *HandshakeState) LocalStaticPublicKey() []byte {
	if s == nil || s.static == nil {
		return nil
	}
	return append([]byte(nil), s.static.PublicKey...)
}

// LastPayload returns the plaintext payload from the most recently read
// handshake message.
func (s *HandshakeState) LastPayload() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.lastPayload...)
}

func (s *HandshakeState) mixHash(data []byte) {
	buffer := make([]byte, HashSize+len(data))
	copy(buffer[:HashSize], s.h[:])
	copy(buffer[HashSize:], data)
	sum := sha256.Sum256(buffer)
	s.h = sum
}

func (s *HandshakeState) mixKey(input []byte) {
	mac := hmac.New(sha256.New, s.ck[:])
	mac.Write(input)
	temp := mac.Sum(nil)

	mac = hmac.New(sha256.New, temp)
	mac.Write([]byte{0x01})
	copy(s.ck[:], mac.Sum(nil))

	mac = hmac.New(sha256.New, temp)
	mac.Write([]byte{0x02})
	mac.Write(s.ck[:])
	s.key = mac.Sum(nil)
	s.nonce = 0
}

func (s *HandshakeState) encryptAndHash(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	if len(s.key) != 32 {
		return nil, fmt.Errorf("%w: payload encryption key is not available", ErrInvalidHandshakeMessage)
	}
	var nonce [12]byte
	for i := 0; i < 3; i++ {
		nonce[4+i] = byte(s.nonce >> (8 * i))
	}
	ciphertext, err := chacha20poly1305Seal(s.key, nonce[:], s.h[:], plaintext)
	if err != nil {
		return nil, err
	}
	s.nonce++
	s.mixHash(ciphertext)
	return ciphertext, nil
}

func (s *HandshakeState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	if len(s.key) != 32 {
		return nil, fmt.Errorf("%w: payload decryption key is not available", ErrInvalidHandshakeMessage)
	}
	var nonce [12]byte
	for i := 0; i < 3; i++ {
		nonce[4+i] = byte(s.nonce >> (8 * i))
	}
	plaintext, err := chacha20poly1305Open(s.key, nonce[:], s.h[:], ciphertext)
	if err != nil {
		return nil, err
	}
	s.nonce++
	s.mixHash(ciphertext)
	return plaintext, nil
}

// WriteMessage produces the next outbound handshake message. Message one
// rejects any payload. For messages two and three, PayloadBuilder is used
// when configured; otherwise payload is sent as-is.
func (s *HandshakeState) WriteMessage(payload []byte) ([]byte, error) {
	if s == nil {
		return nil, ErrUnexpectedHandshakeMessage
	}
	if err := ValidateFirstMessagePayload(s.nextMessage, payload); err != nil {
		return nil, err
	}

	switch s.nextMessage {
	case 1:
		if s.role != Initiator {
			return nil, ErrUnexpectedHandshakeMessage
		}
		ephemeral, err := ecdh.X25519().GenerateKey(s.random)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate ephemeral key: %w", err)
		}
		s.localEphemeral = ephemeral
		public := ephemeral.PublicKey().Bytes()
		s.mixHash(public)
		s.nextMessage = 2
		return append([]byte(nil), public...), nil

	case 2:
		if s.role != Responder || len(s.remoteEphemeral) != StaticKeySize {
			return nil, ErrUnexpectedHandshakeMessage
		}
		ephemeral, err := ecdh.X25519().GenerateKey(s.random)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate ephemeral key: %w", err)
		}
		s.localEphemeral = ephemeral
		public := ephemeral.PublicKey().Bytes()
		wire := append([]byte(nil), public...)
		s.mixHash(public)

		shared, err := x25519(ephemeral, s.remoteEphemeral)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		staticCiphertext, err := s.encryptAndHash(s.static.PublicKey)
		if err != nil {
			return nil, err
		}
		wire = append(wire, staticCiphertext...)

		private, err := s.static.private()
		if err != nil {
			return nil, err
		}
		shared, err = x25519(private, s.remoteEphemeral)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		if s.builder != nil {
			bindingHash := append([]byte(nil), s.h[:]...)
			staticPublicKey := append([]byte(nil), s.static.PublicKey...)
			payload, err = s.builder(2, bindingHash, staticPublicKey)
			if err != nil {
				return nil, err
			}
		}
		s.payloadHashes[2] = append([]byte(nil), s.h[:]...)
		payloadCiphertext, err := s.encryptAndHash(payload)
		if err != nil {
			return nil, err
		}
		wire = append(wire, payloadCiphertext...)
		s.nextMessage = 3
		return wire, nil

	case 3:
		if s.role != Initiator || len(s.remoteEphemeral) != StaticKeySize || len(s.remoteStatic) != StaticKeySize {
			return nil, ErrUnexpectedHandshakeMessage
		}
		staticCiphertext, err := s.encryptAndHash(s.static.PublicKey)
		if err != nil {
			return nil, err
		}
		wire := append([]byte(nil), staticCiphertext...)

		private, err := s.static.private()
		if err != nil {
			return nil, err
		}
		// "se": initiator static private * responder ephemeral public.
		shared, err := x25519(private, s.remoteEphemeral)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		// "ss": initiator static private * responder static public.
		shared, err = x25519(private, s.remoteStatic)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		if s.builder != nil {
			bindingHash := append([]byte(nil), s.h[:]...)
			staticPublicKey := append([]byte(nil), s.static.PublicKey...)
			payload, err = s.builder(3, bindingHash, staticPublicKey)
			if err != nil {
				return nil, err
			}
		}
		s.payloadHashes[3] = append([]byte(nil), s.h[:]...)
		payloadCiphertext, err := s.encryptAndHash(payload)
		if err != nil {
			return nil, err
		}
		wire = append(wire, payloadCiphertext...)
		s.nextMessage = 4
		s.completed = true
		return wire, nil

	default:
		return nil, ErrUnexpectedHandshakeMessage
	}
}

// ReadMessage consumes the next inbound handshake message and returns its
// plaintext payload. For message one the returned payload is always nil.
func (s *HandshakeState) ReadMessage(message []byte) ([]byte, error) {
	if s == nil {
		return nil, ErrUnexpectedHandshakeMessage
	}
	s.lastPayload = nil

	switch s.nextMessage {
	case 1:
		if s.role != Responder {
			return nil, ErrUnexpectedHandshakeMessage
		}
		if len(message) > message1Size {
			return nil, ErrFirstMessagePayload
		}
		if len(message) != message1Size {
			return nil, ErrInvalidHandshakeMessage
		}
		if _, err := ecdh.X25519().NewPublicKey(message); err != nil {
			return nil, fmt.Errorf("%w: invalid ephemeral public key", ErrInvalidHandshakeMessage)
		}
		s.remoteEphemeral = append([]byte(nil), message...)
		s.mixHash(message)
		s.nextMessage = 2
		return nil, nil

	case 2:
		if s.role != Initiator || s.localEphemeral == nil {
			return nil, ErrUnexpectedHandshakeMessage
		}
		if len(message) < StaticKeySize+aeadTagSize {
			return nil, ErrInvalidHandshakeMessage
		}
		ephemeralPublic := message[:StaticKeySize]
		if _, err := ecdh.X25519().NewPublicKey(ephemeralPublic); err != nil {
			return nil, fmt.Errorf("%w: invalid peer ephemeral public key", ErrInvalidHandshakeMessage)
		}
		s.remoteEphemeral = append([]byte(nil), ephemeralPublic...)
		s.mixHash(ephemeralPublic)

		shared, err := x25519(s.localEphemeral, s.remoteEphemeral)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		// The peer static key is encrypted once message two has a key.
		staticStart := StaticKeySize
		staticEnd := staticStart + StaticKeySize + aeadTagSize
		if len(message) < staticEnd {
			return nil, ErrInvalidHandshakeMessage
		}
		staticPlaintext, err := s.decryptAndHash(message[staticStart:staticEnd])
		if err != nil {
			return nil, err
		}
		if len(staticPlaintext) != StaticKeySize {
			return nil, ErrInvalidHandshakeMessage
		}
		if _, err := ecdh.X25519().NewPublicKey(staticPlaintext); err != nil {
			return nil, fmt.Errorf("%w: invalid peer static public key", ErrInvalidHandshakeMessage)
		}
		s.remoteStatic = append([]byte(nil), staticPlaintext...)

		shared, err = x25519(s.localEphemeral, s.remoteStatic)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		s.payloadHashes[2] = append([]byte(nil), s.h[:]...)
		payload, err := s.decryptAndHash(message[staticEnd:])
		if err != nil {
			return nil, err
		}
		s.lastPayload = append([]byte(nil), payload...)
		s.nextMessage = 3
		return payload, nil

	case 3:
		if s.role != Responder || len(s.remoteEphemeral) != StaticKeySize {
			return nil, ErrUnexpectedHandshakeMessage
		}
		staticEnd := StaticKeySize + aeadTagSize
		if len(message) < staticEnd {
			return nil, ErrInvalidHandshakeMessage
		}
		staticPlaintext, err := s.decryptAndHash(message[:staticEnd])
		if err != nil {
			return nil, err
		}
		if len(staticPlaintext) != StaticKeySize {
			return nil, ErrInvalidHandshakeMessage
		}
		if _, err := ecdh.X25519().NewPublicKey(staticPlaintext); err != nil {
			return nil, fmt.Errorf("%w: invalid peer static public key", ErrInvalidHandshakeMessage)
		}
		s.remoteStatic = append([]byte(nil), staticPlaintext...)

		// "se": responder ephemeral private * initiator static public.
		if s.localEphemeral == nil {
			return nil, ErrUnexpectedHandshakeMessage
		}
		shared, err := x25519(s.localEphemeral, s.remoteStatic)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		private, err := s.static.private()
		if err != nil {
			return nil, err
		}
		// "ss": responder static private * initiator static public.
		shared, err = x25519(private, s.remoteStatic)
		if err != nil {
			return nil, err
		}
		s.mixKey(shared)

		s.payloadHashes[3] = append([]byte(nil), s.h[:]...)
		payload, err := s.decryptAndHash(message[staticEnd:])
		if err != nil {
			return nil, err
		}
		s.lastPayload = append([]byte(nil), payload...)
		s.nextMessage = 4
		s.completed = true
		return payload, nil

	default:
		return nil, ErrUnexpectedHandshakeMessage
	}
}

// VerifyPeerIdentity validates the payload that the peer sent in its
// authentication message. It fails until the handshake is complete and uses
// the message-local binding hash, so an identity from another handshake or
// another message cannot be substituted.
func (s *HandshakeState) VerifyPeerIdentity(payload []byte) (*PeerIdentity, error) {
	if s == nil || !s.completed {
		return nil, ErrHandshakeIncomplete
	}
	messageNumber := 2
	if s.role == Responder {
		messageNumber = 3
	}
	if len(s.payloadHashes[messageNumber]) == 0 || len(s.remoteStatic) != StaticKeySize {
		return nil, ErrIdentityPayloadMissing
	}
	return ValidateIdentityPayloadAtCompletion(
		true,
		s.payloadHashes[messageNumber],
		s.remoteStatic,
		payload,
	)
}
