package netsec

import (
	"crypto/ecdh"
	"encoding/json"
	"errors"
)

// Decrypt unwraps the content-encryption key for the recipient identified by
// keyID and decrypts the message content.
//
// Decryption alone never establishes trust: even when the ciphertext
// decrypts successfully, the plaintext is only returned if the key-agreement
// context carried by the recipient matches the caller's expected session
// context. If the caller has no context, or the contexts differ, the
// plaintext is destroyed and an error is returned instead.
func Decrypt(msg *Message, expected *Context, keyID []byte, priv *ecdh.PrivateKey, aad []byte) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("netsec: nil message")
	}
	if expected == nil {
		return nil, ErrMissingContext
	}
	if priv == nil {
		return nil, errors.New("netsec: nil private key")
	}

	rec := msg.findRecipient(keyID)
	if rec == nil {
		return nil, ErrNoRecipient
	}
	var rh recipientHeader
	if err := json.Unmarshal(rec.Protected, &rh); err != nil {
		return nil, err
	}
	if rh.Context == nil {
		return nil, ErrMissingContext
	}

	// Unwrap the CEK. Key derivation commits to the context carried by the
	// message, so the cryptographic layer may succeed even for a context
	// that does not belong to this session.
	kek, err := deriveKEK(rh.Algorithm, rh.Context, rh.KeyID, rh.EphemeralKey, priv)
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	cek, err := unwrapKey(kek, rec.EncryptedKey)
	if err != nil {
		return nil, ErrDecryption
	}
	defer clear(cek)

	var ph protectedHeader
	if err := json.Unmarshal(msg.Protected, &ph); err != nil {
		return nil, err
	}
	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	if len(msg.Ciphertext) < gcm.NonceSize() {
		return nil, ErrDecryption
	}
	plaintext, err := gcm.Open(nil, msg.Ciphertext[:gcm.NonceSize()], msg.Ciphertext[gcm.NonceSize():], contentAAD(msg.Protected, aad))
	if err != nil {
		return nil, ErrDecryption
	}

	// The plaintext is only trustworthy if the recipient's key-agreement
	// context matches the session context expected by the caller.
	if !contextEqual(rh.Context, expected) {
		clear(plaintext)
		return nil, ErrContextMismatch
	}
	return plaintext, nil
}
