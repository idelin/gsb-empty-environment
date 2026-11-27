package noise

import (
	"crypto/rand"
	"fmt"
	"io"
)

// Config configures a Noise handshake.
type Config struct {
	// Pattern is XX, KK, IK, or XXfallback.
	Pattern Pattern
	// Initiator selects the initiator role.
	Initiator bool
	// Prologue is mixed into the transcript before any pre-messages.
	Prologue []byte
	// LocalStatic is the local static X25519 key. It is required for XX, KK,
	// IK, and XXfallback.
	LocalStatic *KeyPair
	// RemoteStatic is a pre-message static public key for KK, IK, and
	// XXfallback. For XX, and for the responder side of IK/XXfallback, it acts
	// as an optional expected peer static key.
	RemoteStatic []byte
	// ExpectedRemoteStatic optionally pins a static key learned during XX or
	// the responder side of IK.
	ExpectedRemoteStatic []byte
	// Ephemeral supplies deterministic ephemeral key material. It is required
	// when directly constructing an XXfallback initiator, because the first
	// fallback message is the already-sent ephemeral key.
	Ephemeral *KeyPair
	// FirstMessage is the received IK first message when directly constructing
	// an XXfallback responder.
	FirstMessage []byte
	// Random supplies randomness for generated ephemeral keys. It defaults to
	// crypto/rand.Reader.
	Random io.Reader
}

// HandshakeState executes one role of a Noise handshake.
type HandshakeState struct {
	spec      *patternSpec
	initiator bool
	prologue  []byte
	random    io.Reader

	localStatic  *KeyPair
	remoteStatic *publicKey
	expected     *publicKey

	localEphemeral  *KeyPair
	remoteEphemeral *publicKey

	symmetric    *SymmetricState
	messageIndex int
	split        bool
	destroyed    bool
}

// NewHandshake creates a handshake state. The caller should call Split on
// success; a failed ReadMessage automatically destroys the state.
func NewHandshake(cfg Config) (*HandshakeState, error) {
	spec, err := lookupPattern(cfg.Pattern)
	if err != nil {
		return nil, err
	}
	if cfg.LocalStatic == nil {
		return nil, fmt.Errorf("%w: local static key is required", ErrInvalidConfig)
	}

	h := &HandshakeState{
		spec:      spec,
		initiator: cfg.Initiator,
		random:    cfg.Random,
	}
	if h.random == nil {
		h.random = rand.Reader
	}
	h.prologue = append([]byte(nil), cfg.Prologue...)
	h.localStatic, err = cfg.LocalStatic.Clone()
	if err != nil {
		return nil, err
	}

	remoteStatic := cfg.RemoteStatic
	if len(remoteStatic) != 0 {
		h.remoteStatic, err = parsePublicKey(remoteStatic)
		if err != nil {
			h.Destroy()
			return nil, err
		}
	}
	if len(cfg.ExpectedRemoteStatic) != 0 {
		h.expected, err = parsePublicKey(cfg.ExpectedRemoteStatic)
		if err != nil {
			h.Destroy()
			return nil, err
		}
		if h.remoteStatic != nil && !equalKey(h.expected.raw[:], h.remoteStatic.raw[:]) {
			h.Destroy()
			return nil, fmt.Errorf("%w: conflicting remote static keys", ErrInvalidConfig)
		}
	}

	switch cfg.Pattern {
	case PatternXX:
		if h.expected == nil && h.remoteStatic != nil {
			h.expected = h.remoteStatic
			h.remoteStatic = nil
		}
		if cfg.Ephemeral != nil {
			h.localEphemeral, err = cfg.Ephemeral.Clone()
		}
	case PatternKK:
		if h.remoteStatic == nil {
			h.Destroy()
			return nil, fmt.Errorf("%w: KK requires a remote static key", ErrInvalidConfig)
		}
		if cfg.Ephemeral != nil {
			h.localEphemeral, err = cfg.Ephemeral.Clone()
		}
	case PatternIK:
		if cfg.Initiator {
			if h.remoteStatic == nil {
				h.Destroy()
				return nil, fmt.Errorf("%w: IK initiator requires a remote static key", ErrInvalidConfig)
			}
			if cfg.Ephemeral != nil {
				h.localEphemeral, err = cfg.Ephemeral.Clone()
			}
		} else {
			if h.remoteStatic != nil {
				if h.expected == nil {
					h.expected = h.remoteStatic
				}
				h.remoteStatic = nil
			}
			if cfg.FirstMessage != nil {
				h.Destroy()
				return nil, fmt.Errorf("%w: FirstMessage is only used for XXfallback", ErrInvalidConfig)
			}
		}
	case PatternXXfallback:
		if cfg.Initiator {
			if h.remoteStatic == nil {
				h.Destroy()
				return nil, fmt.Errorf("%w: XXfallback initiator requires the responder static key", ErrInvalidConfig)
			}
			if h.expected == nil {
				h.expected = h.remoteStatic
			}
			if cfg.Ephemeral == nil {
				h.Destroy()
				return nil, fmt.Errorf("%w: XXfallback initiator requires the original ephemeral key", ErrInvalidConfig)
			}
			h.localEphemeral, err = cfg.Ephemeral.Clone()
			if err == nil {
				h.messageIndex = 1
			}
		} else {
			if len(cfg.FirstMessage) < keySize {
				h.Destroy()
				return nil, fmt.Errorf("%w: XXfallback responder requires the first IK message", ErrInvalidConfig)
			}
			if cfg.Ephemeral != nil {
				h.Destroy()
				return nil, fmt.Errorf("%w: XXfallback responder must learn the initiator static key", ErrInvalidConfig)
			}
			if h.remoteStatic != nil {
				if h.expected == nil {
					h.expected = h.remoteStatic
				}
				h.remoteStatic = nil
			}
			h.remoteEphemeral, err = parsePublicKey(cfg.FirstMessage[:keySize])
			if err == nil {
				h.messageIndex = 1
			}
		}
	}
	if err != nil {
		h.Destroy()
		return nil, err
	}

	symmetric, err := NewSymmetricState(cfg.Pattern.ProtocolName())
	if err != nil {
		h.Destroy()
		return nil, err
	}
	h.symmetric = symmetric
	if err := h.symmetric.MixHash(h.prologue); err != nil {
		h.Destroy()
		return nil, err
	}
	if err := h.mixPreMessages(); err != nil {
		h.Destroy()
		return nil, err
	}

	// In XXfallback the already-sent initiator ephemeral is a pre-message from
	// the perspective of the remaining handshake. Both roles must mix it into
	// h in exactly the same position.
	if cfg.Pattern == PatternXXfallback {
		switch {
		case cfg.Initiator && h.localEphemeral != nil:
			if err := h.symmetric.MixHash(h.localEphemeral.public[:]); err != nil {
				h.Destroy()
				return nil, err
			}
		case !cfg.Initiator && h.remoteEphemeral != nil:
			if err := h.symmetric.MixHash(h.remoteEphemeral.raw[:]); err != nil {
				h.Destroy()
				return nil, err
			}
		default:
			h.Destroy()
			return nil, fmt.Errorf("%w: incomplete XXfallback pre-message", ErrInvalidConfig)
		}
	}

	return h, nil
}

// WriteMessage writes the next handshake message, appending an encrypted (or
// plaintext when no key is active) payload.
func (h *HandshakeState) WriteMessage(payload []byte) ([]byte, error) {
	if err := h.writable(); err != nil {
		return nil, err
	}
	msgTokens := h.spec.messages[h.messageIndex]
	out := make([]byte, 0, 128+len(payload))

	for _, tok := range msgTokens {
		switch tok {
		case tokenE:
			if h.localEphemeral == nil {
				ephemeral, err := GenerateKeyPair(h.random)
				if err != nil {
					return nil, h.abort(err)
				}
				h.localEphemeral = ephemeral
			}
			out = append(out, h.localEphemeral.public[:]...)
			if err := h.symmetric.MixHash(h.localEphemeral.public[:]); err != nil {
				return nil, h.abort(err)
			}
		case tokenS:
			if h.localStatic == nil || !h.localStatic.usable() {
				return nil, h.abort(fmt.Errorf("%w: missing local static key", ErrInvalidConfig))
			}
			encrypted, err := h.symmetric.EncryptAndHash(h.localStatic.public[:])
			if err != nil {
				return nil, h.abort(err)
			}
			out = append(out, encrypted...)
		case tokenEE, tokenES, tokenSE, tokenSS:
			shared, err := h.performDH(tok)
			if err != nil {
				return nil, h.abort(err)
			}
			err = h.symmetric.MixKey(shared[:])
			zero(shared[:])
			if err != nil {
				return nil, h.abort(err)
			}
		default:
			return nil, h.abort(fmt.Errorf("%w: unknown token", ErrInvalidMessage))
		}
	}
	encryptedPayload, err := h.symmetric.EncryptAndHash(payload)
	if err != nil {
		return nil, h.abort(err)
	}
	out = append(out, encryptedPayload...)
	h.messageIndex++
	return out, nil
}

// ReadMessage reads and authenticates the next handshake message, returning
// the decrypted payload. Any structural, DH, or AEAD failure destroys the
// handshake state.
func (h *HandshakeState) ReadMessage(message []byte) ([]byte, error) {
	if err := h.readable(); err != nil {
		return nil, err
	}
	msgTokens := h.spec.messages[h.messageIndex]
	remaining := message

	for _, tok := range msgTokens {
		switch tok {
		case tokenE:
			if len(remaining) < keySize {
				return nil, h.abort(fmt.Errorf("%w: truncated e token", ErrInvalidMessage))
			}
			pub, err := parsePublicKey(remaining[:keySize])
			if err != nil {
				return nil, h.abort(err)
			}
			if err := h.symmetric.MixHash(remaining[:keySize]); err != nil {
				return nil, h.abort(err)
			}
			h.remoteEphemeral = pub
			remaining = remaining[keySize:]
		case tokenS:
			encodedLen := keySize
			if h.symmetric.HasKey() {
				encodedLen += tagSize
			}
			if len(remaining) < encodedLen {
				return nil, h.abort(fmt.Errorf("%w: truncated s token", ErrInvalidMessage))
			}
			plain, err := h.symmetric.DecryptAndHash(remaining[:encodedLen])
			if err != nil {
				return nil, h.abort(err)
			}
			if len(plain) != keySize {
				zero(plain)
				return nil, h.abort(fmt.Errorf("%w: invalid static key length", ErrInvalidMessage))
			}
			pub, err := parsePublicKey(plain)
			zero(plain)
			if err != nil {
				return nil, h.abort(err)
			}
			if h.expected != nil && !equalKey(pub.raw[:], h.expected.raw[:]) {
				return nil, h.abort(fmt.Errorf("%w: unexpected remote static key", ErrAuthenticationFailed))
			}
			h.remoteStatic = pub
			remaining = remaining[encodedLen:]
		case tokenEE, tokenES, tokenSE, tokenSS:
			shared, err := h.performDH(tok)
			if err != nil {
				return nil, h.abort(err)
			}
			err = h.symmetric.MixKey(shared[:])
			zero(shared[:])
			if err != nil {
				return nil, h.abort(err)
			}
		default:
			return nil, h.abort(fmt.Errorf("%w: unknown token", ErrInvalidMessage))
		}
	}

	payload, err := h.symmetric.DecryptAndHash(remaining)
	if err != nil {
		return nil, h.abort(err)
	}
	h.messageIndex++
	return payload, nil
}

// Split returns the two transport cipher states. For an initiator the first
// state is for sending and the second for receiving; for a responder the
// order is reversed. Split destroys the handshake symmetric state and all
// local private key material in this HandshakeState.
func (h *HandshakeState) Split() (first, second *CipherState, err error) {
	if h == nil || h.destroyed {
		return nil, nil, ErrStateDestroyed
	}
	if h.messageIndex != len(h.spec.messages) || h.symmetric == nil {
		return nil, nil, ErrHandshakeIncomplete
	}
	first, second, err = h.symmetric.Split()
	if err != nil {
		return nil, nil, h.abort(err)
	}
	h.split = true
	h.Destroy()
	return first, second, nil
}

// SplitForTransport is like Split but returns states already oriented as
// (send, receive) for this handshake role.
func (h *HandshakeState) SplitForTransport() (send, receive *CipherState, err error) {
	first, second, err := h.Split()
	if err != nil {
		return nil, nil, err
	}
	if h.initiator {
		return first, second, nil
	}
	return second, first, nil
}

// HandshakeHash returns a copy of the current transcript hash.
func (h *HandshakeState) HandshakeHash() []byte {
	if h == nil || h.destroyed || h.symmetric == nil {
		return nil
	}
	return h.symmetric.HandshakeHash()
}

// RemoteEphemeral returns a copy of the peer's ephemeral public key.
func (h *HandshakeState) RemoteEphemeral() []byte {
	if h == nil || h.remoteEphemeral == nil {
		return nil
	}
	return h.remoteEphemeral.bytes()
}

// RemoteStatic returns a copy of the peer's static public key.
func (h *HandshakeState) RemoteStatic() []byte {
	if h == nil || h.remoteStatic == nil {
		return nil
	}
	return h.remoteStatic.bytes()
}

// Prologue returns a copy of the prologue used by this handshake.
func (h *HandshakeState) Prologue() []byte {
	if h == nil {
		return nil
	}
	return append([]byte(nil), h.prologue...)
}

// MessageIndex returns the index of the next message in the pattern.
func (h *HandshakeState) MessageIndex() int {
	if h == nil {
		return 0
	}
	return h.messageIndex
}

// Pattern returns the configured handshake pattern.
func (h *HandshakeState) Pattern() Pattern {
	if h == nil || h.spec == nil {
		return ""
	}
	return h.spec.pattern
}

// Destroy clears private key and symmetric handshake material.
func (h *HandshakeState) Destroy() {
	if h == nil {
		return
	}
	if h.symmetric != nil {
		h.symmetric.Destroy()
		h.symmetric = nil
	}
	if h.localEphemeral != nil {
		h.localEphemeral.Destroy()
		h.localEphemeral = nil
	}
	if h.localStatic != nil {
		h.localStatic.Destroy()
		h.localStatic = nil
	}
	zero(h.prologue)
	h.prologue = nil
	h.remoteStatic = nil
	h.remoteEphemeral = nil
	h.expected = nil
	h.destroyed = true
}

func (h *HandshakeState) writable() error {
	if h == nil || h.destroyed {
		return ErrStateDestroyed
	}
	if h.split {
		return ErrHandshakeComplete
	}
	if h.messageIndex >= len(h.spec.messages) {
		return ErrHandshakeComplete
	}
	initiatorTurn := h.messageIndex%2 == 0
	if initiatorTurn != h.initiator {
		return ErrUnexpectedMessage
	}
	return nil
}

func (h *HandshakeState) readable() error {
	if h == nil || h.destroyed {
		return ErrStateDestroyed
	}
	if h.split {
		return ErrHandshakeComplete
	}
	if h.messageIndex >= len(h.spec.messages) {
		return ErrHandshakeComplete
	}
	initiatorTurn := h.messageIndex%2 == 0
	if initiatorTurn == h.initiator {
		return ErrUnexpectedMessage
	}
	return nil
}

func (h *HandshakeState) abort(err error) error {
	h.Destroy()
	return err
}

func (h *HandshakeState) mixPreMessages() error {
	for _, pre := range h.spec.pre {
		var pub []byte
		if pre.token != tokenS && pre.token != tokenE {
			return fmt.Errorf("%w: invalid pre-message token", ErrInvalidMessage)
		}
		if pre.token == tokenE {
			if pre.initiator {
				if h.initiator && h.localEphemeral != nil {
					pub = h.localEphemeral.public[:]
				} else if !h.initiator && h.remoteEphemeral != nil {
					pub = h.remoteEphemeral.raw[:]
				}
			} else {
				if !h.initiator && h.localEphemeral != nil {
					pub = h.localEphemeral.public[:]
				} else if h.initiator && h.remoteEphemeral != nil {
					pub = h.remoteEphemeral.raw[:]
				}
			}
		} else {
			if pre.initiator {
				if h.initiator && h.localStatic != nil {
					pub = h.localStatic.public[:]
				} else if !h.initiator && h.remoteStatic != nil {
					pub = h.remoteStatic.raw[:]
				}
			} else {
				if !h.initiator && h.localStatic != nil {
					pub = h.localStatic.public[:]
				} else if h.initiator && h.remoteStatic != nil {
					pub = h.remoteStatic.raw[:]
				}
			}
		}
		if len(pub) != keySize {
			return fmt.Errorf("%w: missing pre-message %s key", ErrInvalidConfig, pre.token)
		}
		if err := h.symmetric.MixHash(pub); err != nil {
			return err
		}
	}
	return nil
}

func (h *HandshakeState) performDH(tok token) ([keySize]byte, error) {
	var initiatorEphemeral, initiatorStatic, responderEphemeral, responderStatic *KeyPair
	var initiatorEphemeralPub, initiatorStaticPub, responderEphemeralPub, responderStaticPub *publicKey

	if h.initiator {
		initiatorEphemeral = h.localEphemeral
		initiatorStatic = h.localStatic
		responderEphemeralPub = h.remoteEphemeral
		responderStaticPub = h.remoteStatic
	} else {
		initiatorEphemeralPub = h.remoteEphemeral
		initiatorStaticPub = h.remoteStatic
		responderEphemeral = h.localEphemeral
		responderStatic = h.localStatic
	}

	switch tok {
	case tokenEE:
		if h.initiator {
			return dh(initiatorEphemeral, responderEphemeralPub)
		}
		return dh(responderEphemeral, initiatorEphemeralPub)
	case tokenES:
		if h.initiator {
			return dh(initiatorEphemeral, responderStaticPub)
		}
		return dh(responderStatic, initiatorEphemeralPub)
	case tokenSE:
		if h.initiator {
			return dh(initiatorStatic, responderEphemeralPub)
		}
		return dh(responderEphemeral, initiatorStaticPub)
	case tokenSS:
		if h.initiator {
			return dh(initiatorStatic, responderStaticPub)
		}
		return dh(responderStatic, initiatorStaticPub)
	default:
		return [keySize]byte{}, fmt.Errorf("%w: token %s is not a DH token", ErrInvalidMessage, tok)
	}
}
