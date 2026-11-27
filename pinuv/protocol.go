// Package pinuv implements the FIDO CTAP 2.1 PIN/UV Auth Protocol Two
// cryptographic state machine and pinUvAuthToken permission enforcement
// for a hardware security key authenticator host. It uses only the Go
// standard library and performs no network I/O.
package pinuv

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is the pinUvAuthProtocol identifier implemented here.
const ProtocolVersion = 2

const (
	// KeyLength is the byte length of both derived keys (Protocol Two
	// derives two independent 32-byte keys, unlike Protocol One which
	// uses a single shared key for both HMAC and AES).
	KeyLength = 32
	// IVLength is the AES-CBC IV length prepended to ciphertexts.
	IVLength = aes.BlockSize
	// AuthLength is the full HMAC-SHA-256 output length. Protocol Two
	// authenticates with the untruncated 32-byte tag, unlike Protocol
	// One which truncates to 16 bytes.
	AuthLength = sha256.Size
)

// HKDF info strings mandated by CTAP 2.1 for Protocol Two. Keeping them
// distinct is what cryptographically separates the HMAC key from the
// AES key; reusing one key for both (the Protocol One behaviour) is a
// downgrade/confusion vulnerability.
const (
	infoHMACKey = "CTAP2 HMAC key"
	infoAESKey  = "CTAP2 AES key"
)

var (
	ErrInvalidSharedSecret = errors.New("pinuv: invalid ECDH shared secret")
	ErrInvalidPeerKey      = errors.New("pinuv: invalid peer P-256 public key")
	ErrCiphertext          = errors.New("pinuv: malformed ciphertext")
	ErrBadPadding          = errors.New("pinuv: invalid PKCS#7 padding")
	ErrAuthTagLength       = errors.New("pinuv: auth tag must be the full 32-byte HMAC-SHA-256 (Protocol Two)")
)

// hmacSHA256 computes HMAC-SHA-256(key, msg).
func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// hkdfSHA256 is RFC 5869 HKDF with SHA-256, implemented on top of
// crypto/hmac so no external module is required. The salt is 32 zero
// bytes as specified by CTAP 2.1 for Protocol Two key derivation.
func hkdfSHA256(ikm, info []byte, length int) []byte {
	salt := make([]byte, sha256.Size)
	prk := hmacSHA256(salt, ikm) // extract

	var out, t []byte
	for counter := byte(1); len(out) < length; counter++ { // expand
		m := hmac.New(sha256.New, prk)
		m.Write(t)
		m.Write(info)
		m.Write([]byte{counter})
		t = m.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

// Protocol holds the two independent keys derived for one ECDH key
// agreement under PIN/UV Auth Protocol Two.
type Protocol struct {
	hmacKey [KeyLength]byte
	aesKey  [KeyLength]byte
	rand    io.Reader
}

// NewProtocolV2 derives the Protocol Two key pair from a P-256 ECDH
// shared secret via two independent HKDF-SHA-256 expansions:
//
//	hmacKey = HKDF-SHA-256(sharedSecret, info="CTAP2 HMAC key", L=32)
//	aesKey  = HKDF-SHA-256(sharedSecret, info="CTAP2 AES key",  L=32)
//
// The keys are never interchangeable: an HMAC key can never decrypt and
// an AES key can never authenticate.
func NewProtocolV2(sharedSecret []byte) (*Protocol, error) {
	if len(sharedSecret) != KeyLength {
		return nil, ErrInvalidSharedSecret
	}
	p := &Protocol{rand: rand.Reader}
	copy(p.hmacKey[:], hkdfSHA256(sharedSecret, []byte(infoHMACKey), KeyLength))
	copy(p.aesKey[:], hkdfSHA256(sharedSecret, []byte(infoAESKey), KeyLength))
	return p, nil
}

// GenerateKeyAgreement creates a fresh P-256 ECDH key pair and returns
// the private key together with the uncompressed (0x04||X||Y) public
// point encoding used on the CTAP wire.
func GenerateKeyAgreement() (*ecdh.PrivateKey, []byte, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("pinuv: key agreement generation: %w", err)
	}
	return priv, priv.PublicKey().Bytes(), nil
}

// SharedSecret performs P-256 ECDH against an uncompressed peer public
// point and returns the 32-byte shared secret (the X coordinate).
func SharedSecret(priv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	pub, err := ecdh.P256().NewPublicKey(peerPub)
	if err != nil {
		return nil, ErrInvalidPeerKey
	}
	secret, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("pinuv: ECDH: %w", err)
	}
	return secret, nil
}

// pkcs7Pad applies PKCS#7 padding as required by Protocol Two.
func pkcs7Pad(in []byte) []byte {
	pad := aes.BlockSize - len(in)%aes.BlockSize
	out := make([]byte, len(in)+pad)
	copy(out, in)
	for i := len(in); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(in []byte) ([]byte, error) {
	if len(in) == 0 || len(in)%aes.BlockSize != 0 {
		return nil, ErrBadPadding
	}
	pad := int(in[len(in)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(in) {
		return nil, ErrBadPadding
	}
	for _, b := range in[len(in)-pad:] {
		if int(b) != pad {
			return nil, ErrBadPadding
		}
	}
	return in[:len(in)-pad], nil
}

// Encrypt AES-256-CBC encrypts plaintext (PKCS#7 padded) under aesKey
// and returns IV || ciphertext with a fresh random 16-byte IV.
func (p *Protocol) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.aesKey[:])
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext)
	out := make([]byte, IVLength+len(padded))
	iv := out[:IVLength]
	if _, err := io.ReadFull(p.rand, iv); err != nil {
		return nil, fmt.Errorf("pinuv: IV generation: %w", err)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[IVLength:], padded)
	return out, nil
}

// Decrypt splits IV || ciphertext and AES-256-CBC decrypts under aesKey,
// verifying and stripping PKCS#7 padding.
func (p *Protocol) Decrypt(data []byte) ([]byte, error) {
	if len(data) < IVLength+aes.BlockSize || (len(data)-IVLength)%aes.BlockSize != 0 {
		return nil, ErrCiphertext
	}
	block, err := aes.NewCipher(p.aesKey[:])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data)-IVLength)
	cipher.NewCBCDecrypter(block, data[:IVLength]).CryptBlocks(plain, data[IVLength:])
	return pkcs7Unpad(plain)
}

// Authenticate returns the full 32-byte HMAC-SHA-256 of msg under
// hmacKey. Protocol Two never truncates this tag.
func (p *Protocol) Authenticate(msg []byte) []byte {
	return hmacSHA256(p.hmacKey[:], msg)
}

// Verify checks sig against Authenticate(msg) in constant time. Any tag
// shorter than 32 bytes — including a Protocol One style 16-byte
// truncated tag — is rejected, preventing protocol downgrade confusion.
func (p *Protocol) Verify(msg, sig []byte) bool {
	if len(sig) != AuthLength {
		return false
	}
	return hmac.Equal(p.Authenticate(msg), sig)
}
