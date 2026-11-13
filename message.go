package netsec

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
)

const (
	ProtocolVersion          uint16 = 1
	initialMessageDomain            = "example.org/netsec/q0154/initial-message/v1"
	initiatorSignatureDomain        = "example.org/netsec/q0154/initiator-signature/v1"
	gcmNonceSize                    = 12
	maxFirstPlaintextSize           = maxWireBlobLen - 16
)

// InitialMessage is the first, prekey-authenticated message sent to a receiver.
type InitialMessage struct {
	Version            uint16
	Sender             PublicIdentity
	RecipientAccountID string
	RecipientDeviceID  string
	Transcript         []byte
	DirectoryProof     []byte
	Nonce              []byte
	Ciphertext         []byte
	Signature          []byte
}

// Validate checks the outer message, its transcript, and fixed-size fields.
func (m *InitialMessage) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: nil message", ErrInvalidMessage)
	}
	if m.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidMessage, m.Version)
	}
	if err := m.Sender.Validate(); err != nil {
		return fmt.Errorf("%w: sender: %v", ErrInvalidMessage, err)
	}
	if m.RecipientAccountID == "" || m.RecipientDeviceID == "" {
		return fmt.Errorf("%w: missing recipient", ErrInvalidMessage)
	}
	if len(m.RecipientAccountID) > maxIdentifierLen || len(m.RecipientDeviceID) > maxIdentifierLen {
		return fmt.Errorf("%w: recipient identifier too long", ErrInvalidMessage)
	}
	if _, err := ParseTranscript(m.Transcript); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if len(m.DirectoryProof) == 0 {
		return fmt.Errorf("%w: missing directory proof", ErrInvalidMessage)
	}
	if len(m.DirectoryProof) > maxWireBlobLen {
		return fmt.Errorf("%w: directory proof is too large", ErrInvalidMessage)
	}
	if len(m.Nonce) != gcmNonceSize {
		return fmt.Errorf("%w: AEAD nonce has %d bytes", ErrInvalidMessage, len(m.Nonce))
	}
	if len(m.Ciphertext) < 16 {
		return fmt.Errorf("%w: ciphertext is too short", ErrInvalidMessage)
	}
	if len(m.Ciphertext) > maxWireBlobLen {
		return fmt.Errorf("%w: ciphertext is too large", ErrInvalidMessage)
	}
	if len(m.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: initiator signature has %d bytes", ErrInvalidMessage, len(m.Signature))
	}
	return nil
}

// MarshalBinary returns the canonical wire encoding of the first message.
func (m *InitialMessage) MarshalBinary() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	w := &wireBuilder{}
	w.text(initialMessageDomain)
	w.u16(m.Version)
	writePublicIdentity(w, m.Sender)
	w.text(m.RecipientAccountID)
	w.text(m.RecipientDeviceID)
	w.blob(m.Transcript)
	w.blob(m.DirectoryProof)
	w.blob(m.Nonce)
	w.blob(m.Ciphertext)
	w.blob(m.Signature)
	return w.bytes(), nil
}

// ParseInitialMessage strictly decodes a canonical first message.
func ParseInitialMessage(data []byte) (*InitialMessage, error) {
	if len(data) > maxMessageBytes {
		return nil, fmt.Errorf("%w: message too large", ErrInvalidMessage)
	}
	r := newWireReader(data)
	if domain := r.text(maxWireIDLen); domain != initialMessageDomain {
		return nil, fmt.Errorf("%w: wrong domain", ErrInvalidMessage)
	}
	m := &InitialMessage{Version: r.u16()}
	sender, err := readPublicIdentity(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	m.Sender = sender
	m.RecipientAccountID = r.text(maxWireIDLen)
	m.RecipientDeviceID = r.text(maxWireIDLen)
	m.Transcript = r.blob(maxMessageBytes)
	m.DirectoryProof = r.blob(maxWireBlobLen)
	m.Nonce = r.blob(64)
	m.Ciphertext = r.blob(maxWireBlobLen)
	m.Signature = r.blob(ed25519.SignatureSize)
	if err := r.done(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	canonical, err := m.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, fmt.Errorf("%w: non-canonical encoding", ErrInvalidMessage)
	}
	return m, nil
}

// Clone returns a deep copy of the message.
func (m *InitialMessage) Clone() *InitialMessage {
	if m == nil {
		return nil
	}
	return &InitialMessage{
		Version:            m.Version,
		Sender:             m.Sender.clone(),
		RecipientAccountID: m.RecipientAccountID,
		RecipientDeviceID:  m.RecipientDeviceID,
		Transcript:         append([]byte(nil), m.Transcript...),
		DirectoryProof:     append([]byte(nil), m.DirectoryProof...),
		Nonce:              append([]byte(nil), m.Nonce...),
		Ciphertext:         append([]byte(nil), m.Ciphertext...),
		Signature:          append([]byte(nil), m.Signature...),
	}
}

func initiatorSignatureInput(transcript []byte) []byte {
	w := &wireBuilder{}
	w.text(initiatorSignatureDomain)
	w.blob(transcript)
	return w.bytes()
}
