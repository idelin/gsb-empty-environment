package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Wire message types.
const (
	// TypeCapabilities announces the algorithms a peer claims to support.
	TypeCapabilities = "capabilities"
	// TypeSelection announces the algorithm chosen for the session.
	TypeSelection = "selection"
	// TypeConfirmation is a peer's authenticated final acknowledgement.
	TypeConfirmation = "confirmation"
)

// Message is the decoded wire payload of a transcript entry.
//
// Only one of Capabilities, Selection and Confirmation is populated,
// determined by Type.
type Message struct {
	Type         string        `json:"type"`
	Sender       string        `json:"sender"`
	SessionID    string        `json:"session_id"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Selection    string        `json:"selection,omitempty"`
	Confirmation *Confirmation `json:"confirmation,omitempty"`
}

// Confirmation binds a selected algorithm to the negotiated transcript.
type Confirmation struct {
	Algorithm      string `json:"algorithm"`
	TranscriptHash string `json:"transcript_hash"`
}

// Envelope is one captured transcript entry: the exact raw bytes on the
// wire plus the host's authentication assessment of those bytes.
type Envelope struct {
	Raw  []byte
	Auth AuthMark
}

// AuthMark is the verification host's authentication assessment of a
// message's raw bytes. The host performs the cryptographic work (signature,
// MAC or channel-binding check) and reports the result here.
type AuthMark struct {
	// Authenticated reports whether the raw bytes passed authentication.
	Authenticated bool
	// Signer is the identity the raw bytes authenticated as. It is only
	// meaningful when Authenticated is true.
	Signer string
}

// EncodeMessage serializes a message into its canonical wire representation.
func EncodeMessage(msg *Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("netsec: nil message")
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("netsec: encode message: %w", err)
	}
	return raw, nil
}

// DecodeMessage parses raw wire bytes using strict decoding so that
// truncated or malformed entries cannot silently pass the check.
func DecodeMessage(raw []byte) (*Message, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("netsec: empty message")
	}
	var msg Message
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("netsec: decode message: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("netsec: trailing data after message")
	}
	if err := validateShape(&msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func validateShape(msg *Message) error {
	if msg.Sender == "" {
		return fmt.Errorf("netsec: message has empty sender")
	}
	if msg.SessionID == "" {
		return fmt.Errorf("netsec: message has empty session_id")
	}
	switch msg.Type {
	case TypeCapabilities:
		if len(msg.Capabilities) == 0 {
			return fmt.Errorf("netsec: capabilities message lists no algorithms")
		}
		if msg.Selection != "" || msg.Confirmation != nil {
			return fmt.Errorf("netsec: capabilities message carries foreign fields")
		}
		for _, alg := range msg.Capabilities {
			if alg == "" {
				return fmt.Errorf("netsec: capabilities message contains empty algorithm")
			}
		}
	case TypeSelection:
		if msg.Selection == "" {
			return fmt.Errorf("netsec: selection message has empty algorithm")
		}
		if len(msg.Capabilities) != 0 || msg.Confirmation != nil {
			return fmt.Errorf("netsec: selection message carries foreign fields")
		}
	case TypeConfirmation:
		if msg.Confirmation == nil {
			return fmt.Errorf("netsec: confirmation message has no confirmation block")
		}
		if msg.Confirmation.Algorithm == "" {
			return fmt.Errorf("netsec: confirmation has empty algorithm")
		}
		if msg.Confirmation.TranscriptHash == "" {
			return fmt.Errorf("netsec: confirmation has empty transcript_hash")
		}
		if len(msg.Capabilities) != 0 || msg.Selection != "" {
			return fmt.Errorf("netsec: confirmation message carries foreign fields")
		}
	default:
		return fmt.Errorf("netsec: unknown message type %q", msg.Type)
	}
	return nil
}

// TranscriptDigest hashes the exact raw bytes of every envelope in order.
// Confirmations bind to this value, so any deletion, reordering or splicing
// of messages changes the digest and breaks a correctly formed confirmation.
func TranscriptDigest(envelopes []Envelope) []byte {
	h := sha256.New()
	for i := range envelopes {
		h.Write(envelopes[i].Raw)
	}
	return h.Sum(nil)
}

// PrefixDigest hashes the raw bytes of the first n envelopes in order. It
// matches TranscriptDigest over that prefix and is used to construct
// confirmation transcript hashes.
func PrefixDigest(envelopes []Envelope, n int) []byte {
	if n < 0 {
		n = 0
	}
	if n > len(envelopes) {
		n = len(envelopes)
	}
	return TranscriptDigest(envelopes[:n])
}
