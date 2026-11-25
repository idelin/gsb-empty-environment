package displaycommit

import (
	"crypto/ed25519"
	"fmt"
)

// SignedMessage is an optional envelope for an already-signed message tree.
// The signature binds the canonical raw message root.
type SignedMessage struct {
	Message   *Message
	Root      Hash
	Signature []byte
	PublicKey ed25519.PublicKey
}

func SignMessage(privateKey ed25519.PrivateKey, msg *Message) (*SignedMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("displaycommit: cannot sign nil message")
	}
	root := msg.ComputeRoot()
	return &SignedMessage{
		Message:   msg,
		Root:      root,
		Signature: ed25519.Sign(privateKey, root[:]),
		PublicKey: privateKey.Public().(ed25519.PublicKey),
	}, nil
}

func (s *SignedMessage) Verify() bool {
	if s == nil || s.Message == nil || len(s.PublicKey) != ed25519.PublicKeySize || len(s.Signature) == 0 {
		return false
	}
	root := s.Message.ComputeRoot()
	if s.Root != ZeroHash && s.Root != root {
		return false
	}
	return ed25519.Verify(s.PublicKey, root[:], s.Signature)
}
