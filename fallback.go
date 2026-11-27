package noise

import (
	"fmt"
	"io"
)

// NewXXFallbackResponder constructs a responder that has received an IK first
// message and is switching to XXfallback. The first 32 bytes of firstIKMessage
// become the completed "e" pre-message of XXfallback and are mixed into h after
// the original prologue and the responder's pre-message static key.
func NewXXFallbackResponder(prologue []byte, localStatic *KeyPair, firstIKMessage []byte, random io.Reader) (*HandshakeState, error) {
	return NewHandshake(Config{
		Pattern:      PatternXXfallback,
		Initiator:    false,
		Prologue:     prologue,
		LocalStatic:  localStatic,
		FirstMessage: firstIKMessage,
		Random:       random,
	})
}

// NewXXFallbackInitiator constructs an XXfallback initiator after its original
// IK first message was rejected. ephemeral must be the same key pair used for
// the IK "e" token, and remoteStatic is the responder's pre-message static key.
func NewXXFallbackInitiator(prologue []byte, localStatic *KeyPair, remoteStatic []byte, ephemeral *KeyPair, random io.Reader) (*HandshakeState, error) {
	return NewHandshake(Config{
		Pattern:      PatternXXfallback,
		Initiator:    true,
		Prologue:     prologue,
		LocalStatic:  localStatic,
		RemoteStatic: remoteStatic,
		Ephemeral:    ephemeral,
		Random:       random,
	})
}

// FallbackResponder converts an IK responder into an XXfallback responder
// after its first IK message was rejected. firstIKMessage must contain the
// same ephemeral public key that the IK state observed. The original state is
// destroyed on both success and failure.
func (h *HandshakeState) FallbackResponder(firstIKMessage []byte) (*HandshakeState, error) {
	if h == nil || h.destroyed {
		return nil, ErrStateDestroyed
	}
	if h.spec == nil || h.spec.pattern != PatternIK || h.initiator {
		return nil, h.abort(fmt.Errorf("%w: fallback responder requires an IK responder state", ErrInvalidConfig))
	}
	if h.messageIndex != 1 {
		return nil, h.abort(fmt.Errorf("%w: IK first message has not been processed", ErrHandshakeIncomplete))
	}
	if len(firstIKMessage) < keySize {
		return nil, h.abort(fmt.Errorf("%w: truncated IK first message", ErrInvalidMessage))
	}
	if h.remoteEphemeral == nil || !equalKey(firstIKMessage[:keySize], h.remoteEphemeral.raw[:]) {
		return nil, h.abort(fmt.Errorf("%w: IK message ephemeral does not match handshake state", ErrInvalidMessage))
	}
	prologue := h.Prologue()
	localStatic, err := h.localStatic.Clone()
	random := h.random
	if err != nil {
		h.Destroy()
		return nil, err
	}
	var expected []byte
	if h.expected != nil {
		expected = h.expected.bytes()
	} else if h.remoteStatic != nil {
		expected = h.remoteStatic.bytes()
	}
	h.Destroy()
	next, err := NewXXFallbackResponder(prologue, localStatic, firstIKMessage, random)
	localStatic.Destroy()
	if err == nil && expected != nil {
		next.expected, err = parsePublicKey(expected)
		if err != nil {
			next.Destroy()
			next = nil
		}
	}
	return next, err
}

// FallbackInitiator converts an IK initiator after it sent its first message
// into an XXfallback initiator. It preserves the original prologue, the
// responder pre-message static key, and the exact ephemeral key pair. The
// original state is destroyed on both success and failure.
func (h *HandshakeState) FallbackInitiator() (*HandshakeState, error) {
	if h == nil || h.destroyed {
		return nil, ErrStateDestroyed
	}
	if h.spec == nil || h.spec.pattern != PatternIK || !h.initiator {
		return nil, h.abort(fmt.Errorf("%w: fallback initiator requires an IK initiator state", ErrInvalidConfig))
	}
	if h.messageIndex != 1 || h.localEphemeral == nil || h.remoteStatic == nil {
		return nil, h.abort(fmt.Errorf("%w: IK first message has not been sent", ErrHandshakeIncomplete))
	}
	prologue := h.Prologue()
	localStatic, err := h.localStatic.Clone()
	if err != nil {
		h.Destroy()
		return nil, err
	}
	ephemeral, err := h.localEphemeral.Clone()
	if err != nil {
		localStatic.Destroy()
		h.Destroy()
		return nil, err
	}
	remoteStatic := h.remoteStatic.bytes()
	random := h.random
	h.Destroy()
	next, err := NewXXFallbackInitiator(prologue, localStatic, remoteStatic, ephemeral, random)
	localStatic.Destroy()
	ephemeral.Destroy()
	return next, err
}
