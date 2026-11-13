package netsec

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
)

const (
	// ContentAlgorithm is the content-encryption algorithm (AES-256-GCM).
	ContentAlgorithm = "A256GCM"
	// KeyAlgorithm is the recipient key-agreement algorithm
	// (ECDH-ES with X25519, HKDF-SHA-256, AES-256-GCM key wrap).
	KeyAlgorithm = "ECDH-ES+HKDF-256+A256GCM-KW"
)

var (
	// ErrNoRecipient is returned when the message holds no recipient
	// matching the requested key identifier.
	ErrNoRecipient = errors.New("netsec: no recipient matches the key identifier")
	// ErrMissingContext is returned when the caller supplies no expected
	// key-agreement context, or the recipient carries none. Without a
	// context the plaintext must not be trusted.
	ErrMissingContext = errors.New("netsec: key-agreement context required")
	// ErrContextMismatch is returned when the recipient's key-agreement
	// context does not match the caller's session context. The decrypted
	// plaintext is withheld from the caller.
	ErrContextMismatch = errors.New("netsec: key-agreement context mismatch")
	// ErrDecryption is returned when unwrapping or content decryption fails
	// cryptographically.
	ErrDecryption = errors.New("netsec: decryption failed")
)

// Message mirrors a COSE_Encrypt structure: protected headers, ciphertext
// produced under a random content-encryption key, and one recipient entry
// per key-agreement recipient.
type Message struct {
	Protected  []byte       `json:"protected"`
	Ciphertext []byte       `json:"ciphertext"` // nonce || AES-GCM output
	Recipients []*Recipient `json:"recipients"`
}

// Recipient mirrors a COSE_recipient: protected headers carrying the
// key-agreement context, and the wrapped content-encryption key.
type Recipient struct {
	Protected    []byte `json:"protected"`
	EncryptedKey []byte `json:"encrypted_key"` // nonce || wrapped CEK
}

type protectedHeader struct {
	Algorithm string `json:"alg"`
}

type recipientHeader struct {
	Algorithm    string   `json:"alg"`
	KeyID        []byte   `json:"kid"`
	EphemeralKey []byte   `json:"epk"`
	Context      *Context `json:"ctx"`
}

// Marshal serializes the message for transport.
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// ParseMessage deserializes a message produced by Marshal.
func ParseMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Message) findRecipient(keyID []byte) *Recipient {
	for _, r := range m.Recipients {
		if r == nil {
			continue
		}
		var h recipientHeader
		if err := json.Unmarshal(r.Protected, &h); err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(h.KeyID, keyID) == 1 {
			return r
		}
	}
	return nil
}
