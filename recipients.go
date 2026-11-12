package netsec

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strconv"
)

// Recipient wraps a file key into one or more header stanzas during
// encryption.
type Recipient interface {
	Wrap(fileKey []byte) ([]*Stanza, error)
}

// Identity attempts to unwrap a file key from the header stanzas during
// decryption. An identity returns ErrIncorrectIdentity when none of the
// stanzas belongs to it.
type Identity interface {
	Unwrap(stanzas []*Stanza) (fileKey []byte, err error)
}

// ErrIncorrectIdentity means no identity could recover the file key.
var ErrIncorrectIdentity = errors.New("netsec: no identity matched any of the recipients")

const (
	scryptType        = "scrypt"
	scryptSaltSize    = 16
	scryptMinLogN     = 2
	scryptMaxLogN     = 30
	scryptDefaultLogN = 18
	wrapKeySize       = 32
)

// ScryptRecipient encrypts to a passphrase, age style. A header may contain at
// most one scrypt stanza; encryption enforces this and decryption rejects any
// header where a scrypt stanza is accompanied by another recipient line.
type ScryptRecipient struct {
	Passphrase []byte
	LogN       int
}

// ScryptIdentity decrypts a passphrase-protected header.
type ScryptIdentity struct {
	Passphrase []byte
}

// Wrap produces the single scrypt stanza for fileKey.
func (r *ScryptRecipient) Wrap(fileKey []byte) ([]*Stanza, error) {
	logN := r.LogN
	if logN == 0 {
		logN = scryptDefaultLogN
	}
	if logN < scryptMinLogN || logN > scryptMaxLogN {
		return nil, fmt.Errorf("netsec: scrypt work factor %d out of range", logN)
	}
	salt := make([]byte, scryptSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	saltLabel := append([]byte("age-encryption.org/"), salt...)
	derived, err := scrypt(r.Passphrase, saltLabel, 1<<logN, 8, 1, wrapKeySize)
	if err != nil {
		return nil, err
	}
	wrappingKey, err := hkdf.Key(sha256.New, derived, nil, "scrypt", wrapKeySize)
	if err != nil {
		return nil, err
	}

	body := aeadSeal(wrappingKey, make([]byte, aeadNonceSize), fileKey, nil)
	stanza := &Stanza{
		Type: scryptType,
		Args: [][]byte{[]byte(base64.StdEncoding.EncodeToString(salt)), []byte(strconv.Itoa(logN))},
		Body: body,
	}
	return []*Stanza{stanza}, nil
}

// Unwrap recovers the file key from a scrypt stanza, rejecting headers that
// mix scrypt with any other recipient line.
func (i *ScryptIdentity) Unwrap(stanzas []*Stanza) ([]byte, error) {
	var scryptStanza *Stanza
	for _, s := range stanzas {
		if s.Type == scryptType {
			if scryptStanza != nil {
				return nil, errors.New("netsec: multiple scrypt recipient lines")
			}
			scryptStanza = s
		}
	}
	if scryptStanza == nil {
		return nil, ErrIncorrectIdentity
	}
	if len(stanzas) != 1 {
		// A scrypt stanza must be the only recipient line: a passphrase-based
		// header cannot be mixed with key-based recipients.
		return nil, errors.New("netsec: scrypt recipient line must be the only recipient line")
	}

	if len(scryptStanza.Args) != 2 {
		return nil, errors.New("netsec: malformed scrypt recipient line")
	}
	salt, err := base64.StdEncoding.DecodeString(string(scryptStanza.Args[0]))
	if err != nil || len(salt) != scryptSaltSize {
		return nil, errors.New("netsec: invalid scrypt salt")
	}
	logN, err := strconv.Atoi(string(scryptStanza.Args[1]))
	if err != nil || logN < scryptMinLogN || logN > scryptMaxLogN {
		return nil, fmt.Errorf("netsec: invalid scrypt work factor %q", scryptStanza.Args[1])
	}

	saltLabel := append([]byte("age-encryption.org/"), salt...)
	derived, err := scrypt(i.Passphrase, saltLabel, 1<<logN, 8, 1, wrapKeySize)
	if err != nil {
		return nil, err
	}
	wrappingKey, err := hkdf.Key(sha256.New, derived, nil, "scrypt", wrapKeySize)
	if err != nil {
		return nil, err
	}
	fileKey, err := aeadOpen(wrappingKey, make([]byte, aeadNonceSize), scryptStanza.Body, nil)
	if err != nil {
		return nil, ErrIncorrectIdentity
	}
	if len(fileKey) != FileKeySize {
		return nil, errors.New("netsec: unwrapped file key has wrong length")
	}
	return fileKey, nil
}

// hkdfKey is a small wrapper used by the package to derive keys.
func hkdfKey(h func() hash.Hash, secret, salt []byte, info string, length int) ([]byte, error) {
	return hkdf.Key(h, secret, salt, info, length)
}
