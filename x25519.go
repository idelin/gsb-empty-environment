package netsec

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// X25519 recipient and identity, following the age v1 "X25519" stanza.

const x25519Type = "X25519"

// X25519Recipient wraps file keys for a public key.
type X25519Recipient struct {
	PublicKey *ecdh.PublicKey
}

// X25519Identity unwraps file keys with the matching private key.
type X25519Identity struct {
	PrivateKey *ecdh.PrivateKey
}

// GenerateX25519Identity creates a fresh X25519 identity.
func GenerateX25519Identity() (*X25519Identity, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &X25519Identity{PrivateKey: key}, nil
}

// Recipient returns the public recipient for this identity.
func (i *X25519Identity) Recipient() *X25519Recipient {
	return &X25519Recipient{PublicKey: i.PrivateKey.PublicKey()}
}

// Wrap creates an X25519 stanza for fileKey.
func (r *X25519Recipient) Wrap(fileKey []byte) ([]*Stanza, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(r.PublicKey)
	if err != nil {
		return nil, err
	}
	salt := append(append([]byte{}, ephemeral.PublicKey().Bytes()...), r.PublicKey.Bytes()...)
	wrappingKey, err := hkdf.Key(sha256.New, shared, salt, "age-encryption.org/v1/X25519", wrapKeySize)
	if err != nil {
		return nil, err
	}
	body := aeadSeal(wrappingKey, make([]byte, aeadNonceSize), fileKey, nil)
	return []*Stanza{{
		Type: x25519Type,
		Args: [][]byte{[]byte(base64.StdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()))},
		Body: body,
	}}, nil
}

// Unwrap recovers the file key if one of the stanzas targets this identity.
func (i *X25519Identity) Unwrap(stanzas []*Stanza) ([]byte, error) {
	for _, s := range stanzas {
		if s.Type != x25519Type {
			continue
		}
		if len(s.Args) != 1 {
			return nil, errors.New("netsec: malformed X25519 recipient line")
		}
		share, err := base64.StdEncoding.DecodeString(string(s.Args[0]))
		if err != nil || len(share) != 32 {
			return nil, errors.New("netsec: malformed X25519 ephemeral share")
		}
		ephemeral, err := ecdh.X25519().NewPublicKey(share)
		if err != nil {
			return nil, errors.New("netsec: malformed X25519 ephemeral share")
		}
		shared, err := i.PrivateKey.ECDH(ephemeral)
		if err != nil {
			return nil, err
		}
		salt := append(append([]byte{}, share...), i.PrivateKey.PublicKey().Bytes()...)
		wrappingKey, err := hkdf.Key(sha256.New, shared, salt, "age-encryption.org/v1/X25519", wrapKeySize)
		if err != nil {
			return nil, err
		}
		fileKey, err := aeadOpen(wrappingKey, make([]byte, aeadNonceSize), s.Body, nil)
		if err != nil {
			// A bad tag means this stanza was not for us or was tampered with;
			// keep looking rather than leaking which.
			continue
		}
		if len(fileKey) != FileKeySize {
			return nil, errors.New("netsec: unwrapped file key has wrong length")
		}
		return fileKey, nil
	}
	return nil, ErrIncorrectIdentity
}
