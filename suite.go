package wireguard

import (
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
	"io"
)

const (
	// NoiseConstruction is the Noise protocol name used by WireGuard.
	NoiseConstruction = "Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s"
	// NoiseIdentifier is the WireGuard v1 protocol identifier.
	NoiseIdentifier = "WireGuard v1 zx2c4 Jason@zx2c4.com"

	labelMAC1   = "mac1----"
	labelCookie = "cookie--"
)

// KeyPair is a Curve25519 static or ephemeral key pair.
type KeyPair struct {
	Private [32]byte
	Public  [32]byte
}

// Suite is the cryptographic primitive set used by the handshake and data
// plane. DefaultSuite implements the standard WireGuard construction:
// X25519, BLAKE2s, and ChaCha20-Poly1305.
type Suite interface {
	NewKeyPair(rand io.Reader) (KeyPair, error)
	PublicKey(private [32]byte) ([32]byte, error)
	SharedSecret(private, public [32]byte) ([32]byte, error)

	Hash(parts ...[]byte) [32]byte
	MAC(key, data []byte) [16]byte

	KDF1(key [32]byte, input []byte) [32]byte
	KDF2(key [32]byte, input []byte) ([32]byte, [32]byte)
	KDF3(key [32]byte, input []byte) ([32]byte, [32]byte, [32]byte)

	Seal(key [32]byte, nonce [12]byte, plaintext, additionalData []byte) ([]byte, error)
	Open(key [32]byte, nonce [12]byte, ciphertext, additionalData []byte) ([]byte, error)
	SealX(key [32]byte, nonce [24]byte, plaintext, additionalData []byte) ([]byte, error)
	OpenX(key [32]byte, nonce [24]byte, ciphertext, additionalData []byte) ([]byte, error)
}

// DefaultSuite is the standard WireGuard primitive suite.
type DefaultSuite struct{}

func (DefaultSuite) NewKeyPair(r io.Reader) (KeyPair, error) {
	if r == nil {
		r = rand.Reader
	}
	priv, err := ecdh.X25519().GenerateKey(r)
	if err != nil {
		return KeyPair{}, err
	}
	var kp KeyPair
	copy(kp.Private[:], priv.Bytes())
	copy(kp.Public[:], priv.PublicKey().Bytes())
	return kp, nil
}

func (DefaultSuite) PublicKey(private [32]byte) ([32]byte, error) {
	var out [32]byte
	priv, err := ecdh.X25519().NewPrivateKey(private[:])
	if err != nil {
		return out, err
	}
	copy(out[:], priv.PublicKey().Bytes())
	return out, nil
}

func (DefaultSuite) SharedSecret(private, public [32]byte) ([32]byte, error) {
	var out [32]byte
	priv, err := ecdh.X25519().NewPrivateKey(private[:])
	if err != nil {
		return out, err
	}
	pub, err := ecdh.X25519().NewPublicKey(public[:])
	if err != nil {
		return out, err
	}
	secret, err := priv.ECDH(pub)
	if err != nil {
		return out, err
	}
	copy(out[:], secret)
	return out, nil
}

func (DefaultSuite) Hash(parts ...[]byte) [32]byte {
	return blake2sHash(parts...)
}

func (DefaultSuite) MAC(key, data []byte) [16]byte {
	return blake2sMAC(key, data)
}

func (DefaultSuite) KDF1(key [32]byte, input []byte) [32]byte {
	return hmacBlake2s(key[:], input)
}

func (DefaultSuite) KDF2(key [32]byte, input []byte) ([32]byte, [32]byte) {
	t0 := hmacBlake2s(key[:], input)
	t1 := hmacBlake2s(t0[:], []byte{1})
	t2 := hmacBlake2s(t0[:], append(t1[:], 2))
	return t1, t2
}

func (DefaultSuite) KDF3(key [32]byte, input []byte) ([32]byte, [32]byte, [32]byte) {
	t0 := hmacBlake2s(key[:], input)
	t1 := hmacBlake2s(t0[:], []byte{1})
	t2 := hmacBlake2s(t0[:], append(t1[:], 2))
	t3 := hmacBlake2s(t0[:], append(t2[:], 3))
	return t1, t2, t3
}

func (DefaultSuite) Seal(key [32]byte, nonce [12]byte, plaintext, additionalData []byte) ([]byte, error) {
	return chacha20Poly1305Seal(&key, &nonce, plaintext, additionalData), nil
}

func (DefaultSuite) Open(key [32]byte, nonce [12]byte, ciphertext, additionalData []byte) ([]byte, error) {
	return chacha20Poly1305Open(&key, &nonce, ciphertext, additionalData)
}

func (DefaultSuite) SealX(key [32]byte, nonce [24]byte, plaintext, additionalData []byte) ([]byte, error) {
	return xchacha20Poly1305Seal(&key, &nonce, plaintext, additionalData), nil
}

func (DefaultSuite) OpenX(key [32]byte, nonce [24]byte, ciphertext, additionalData []byte) ([]byte, error) {
	return xchacha20Poly1305Open(&key, &nonce, ciphertext, additionalData)
}

// GenerateKeyPair creates a Curve25519 key pair using the default suite.
func GenerateKeyPair() (KeyPair, error) {
	return DefaultSuite{}.NewKeyPair(rand.Reader)
}

// PublicKey derives the public key for a private key.
func PublicKey(private [32]byte) ([32]byte, error) {
	return DefaultSuite{}.PublicKey(private)
}

// NewKeyPairFromPrivate derives a complete key pair from a private key.
func NewKeyPairFromPrivate(private [32]byte) (KeyPair, error) {
	pub, err := PublicKey(private)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Private: private, Public: pub}, nil
}

func validatePeerPublic(pub [32]byte) error {
	var zero [32]byte
	if pub == zero {
		return fmt.Errorf("wireguard: remote public key is all zero")
	}
	return nil
}
