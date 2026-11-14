package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	// EnvelopeMagic marks an encrypted export produced by this component.
	EnvelopeMagic = "GSB-EXPORT-V1"
	// envelopeVersion is the current envelope layout version.
	envelopeVersion byte = 1

	keySize   = 32 // AES-256
	nonceSize = 12 // AES-GCM standard nonce

	domainFileKey   = "export-file-key-v1"
	domainFilterMAC = "task-filter-binding-v1"
	domainWatermark = "export-watermark-v1"
)

var (
	// ErrInvalidKey means a master key is not exactly keySize bytes.
	ErrInvalidKey = errors.New("netsec: master key must be 32 bytes")
	// ErrBadEnvelope means an encrypted blob does not parse or authenticate.
	ErrBadEnvelope = errors.New("netsec: malformed or unauthenticated export envelope")
)

// deriveKey derives a purpose-specific 32-byte key from the master key with
// an HKDF-SHA256 style construction (RFC 5869 expand-only style with domain
// separation). Different domains never reuse key material.
func deriveKey(masterKey []byte, domain string) []byte {
	salt := make([]byte, sha256.Size)
	prk := hmac.New(sha256.New, salt)
	prk.Write(masterKey)
	pseudorandomKey := prk.Sum(nil)

	info := append([]byte(domain), 0x01) // info || 0x01, single-block expand
	expand := hmac.New(sha256.New, pseudorandomKey)
	expand.Write(info)
	return expand.Sum(nil)
}

// SealedEnvelope is the on-disk layout of an encrypted export:
//
//	magic(14) || version(1) || nonce(12) || gcm_ciphertext
//
// The GCM authenticated data binds the magic and version header.
type SealedEnvelope struct {
	Nonce      []byte
	Ciphertext []byte
}

// Seal encrypts plaintext under a domain-derived AES-256-GCM key and returns
// the serialized envelope. A fresh random nonce is generated per call.
func Seal(randReader io.Reader, masterKey []byte, plaintext []byte) ([]byte, error) {
	if len(masterKey) != keySize {
		return nil, ErrInvalidKey
	}
	if randReader == nil {
		randReader = rand.Reader
	}

	block, err := aes.NewCipher(deriveKey(masterKey, domainFileKey))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return nil, err
	}
	aad := append([]byte(EnvelopeMagic), envelopeVersion)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)

	out := make([]byte, 0, len(EnvelopeMagic)+1+len(nonce)+len(ciphertext))
	out = append(out, []byte(EnvelopeMagic)...)
	out = append(out, envelopeVersion)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// Open parses an envelope and decrypts it, verifying the GCM tag and the
// magic/version authenticated header.
func Open(masterKey, blob []byte) ([]byte, error) {
	if len(masterKey) != keySize {
		return nil, ErrInvalidKey
	}
	headerLen := len(EnvelopeMagic) + 1 + nonceSize
	if len(blob) < headerLen+1 {
		return nil, ErrBadEnvelope
	}
	if string(blob[:len(EnvelopeMagic)]) != EnvelopeMagic {
		return nil, ErrBadEnvelope
	}
	if blob[len(EnvelopeMagic)] != envelopeVersion {
		return nil, ErrBadEnvelope
	}

	nonce := blob[len(EnvelopeMagic)+1 : headerLen]
	ciphertext := blob[headerLen:]

	block, err := aes.NewCipher(deriveKey(masterKey, domainFileKey))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	aad := append([]byte(EnvelopeMagic), envelopeVersion)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadEnvelope, err)
	}
	return plaintext, nil
}

// computeBinding returns the keyed HMAC over the immutable task identity and
// its canonical filter set. Any later rewrite of a condition invalidates it.
func computeBinding(masterKey []byte, t *Task) string {
	mac := hmac.New(sha256.New, deriveKey(masterKey, domainFilterMAC))
	mac.Write([]byte(t.ID))
	mac.Write([]byte{0})
	mac.Write([]byte(t.PrincipalID))
	mac.Write([]byte{0})
	mac.Write([]byte(t.TenantID))
	mac.Write([]byte{0})
	mac.Write([]byte(t.Dataset))
	mac.Write([]byte{0})
	mac.Write([]byte(canonicalScope(t.Filters)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyBinding compares a stored binding in constant time.
func verifyBinding(masterKey []byte, t *Task) bool {
	expected := computeBinding(masterKey, t)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(t.binding)) == 1
}

// watermarkMAC signs the visible watermark statement so tampering with the
// embedded attribution is detectable.
func watermarkMAC(masterKey []byte, statement string) string {
	mac := hmac.New(sha256.New, deriveKey(masterKey, domainWatermark))
	mac.Write([]byte(statement))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
