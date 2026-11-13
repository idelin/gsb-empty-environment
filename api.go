package netsec

import "errors"

// EncryptWithParties is the explicit party-first form of Encrypt. It is useful
// when party identifiers are raw byte slices and would otherwise be ambiguous
// with raw key material.
func EncryptWithParties(partyU, partyV any, plaintext []byte, keyMaterial ...any) ([]byte, error) {
	args := []any{plaintext, explicitPartyValue(partyU), explicitPartyValue(partyV)}
	args = append(args, keyMaterial...)
	return Encrypt(args...)
}

// DecryptWithParties is the explicit party-first form of Decrypt.
func DecryptWithParties(partyU, partyV any, ciphertext []byte, keyMaterial ...any) ([]byte, error) {
	args := []any{ciphertext, explicitPartyValue(partyU), explicitPartyValue(partyV)}
	args = append(args, keyMaterial...)
	return Decrypt(args...)
}

// EncryptParties is an alias for EncryptWithParties.
func EncryptParties(partyU, partyV any, plaintext []byte, keyMaterial ...any) ([]byte, error) {
	return EncryptWithParties(partyU, partyV, plaintext, keyMaterial...)
}

// DecryptParties is an alias for DecryptWithParties.
func DecryptParties(partyU, partyV any, ciphertext []byte, keyMaterial ...any) ([]byte, error) {
	return DecryptWithParties(partyU, partyV, ciphertext, keyMaterial...)
}

// Session keeps the ordered party pair and key material together so repeated
// messages cannot accidentally be encrypted or decrypted with a different
// party order.
type Session struct {
	partyU      any
	partyV      any
	keyMaterial []any
}

// Cipher is an alias for Session.
type Cipher = Session

// NewSession creates a session for the ordered pair (partyU, partyV).
// keyMaterial may contain KeyPair values, crypto/ecdh keys, or one shared
// secret.
func NewSession(partyU, partyV any, keyMaterial ...any) (*Session, error) {
	if _, err := partyBytes(partyU); err != nil {
		return nil, err
	}
	if _, err := partyBytes(partyV); err != nil {
		return nil, err
	}
	if len(keyMaterial) == 0 {
		return nil, errors.New("netsec: missing key material")
	}
	return &Session{
		partyU:      explicitPartyValue(partyU),
		partyV:      explicitPartyValue(partyV),
		keyMaterial: append([]any(nil), keyMaterial...),
	}, nil
}

// NewCipher is an alias for NewSession.
func NewCipher(partyU, partyV any, keyMaterial ...any) (*Session, error) {
	return NewSession(partyU, partyV, keyMaterial...)
}

// Encrypt encrypts a message using the session's fixed party order.
func (session *Session) Encrypt(plaintext []byte) ([]byte, error) {
	if session == nil {
		return nil, errors.New("netsec: nil session")
	}
	return EncryptWithParties(session.partyU, session.partyV, plaintext, session.keyMaterial...)
}

// Decrypt decrypts a message using the session's fixed party order.
func (session *Session) Decrypt(ciphertext []byte) ([]byte, error) {
	if session == nil {
		return nil, errors.New("netsec: nil session")
	}
	return DecryptWithParties(session.partyU, session.partyV, ciphertext, session.keyMaterial...)
}

func explicitPartyValue(value any) any {
	switch party := value.(type) {
	case string, PartyInfo, *PartyInfo:
		return party
	default:
		bytesValue, err := partyBytes(value)
		if err != nil {
			return value
		}
		return PartyInfo{ID: bytesValue}
	}
}
