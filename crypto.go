package treekem

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	hashSize       = sha256.Size
	mlsLabelPrefix = "MLS 1.0 "
)

// Identity is a member's long-term signature and key-agreement identity.
type Identity struct {
	public   PublicIdentity
	encPriv  *ecdh.PrivateKey
	signPriv ed25519.PrivateKey
}

// PublicIdentity is the shareable part of Identity.
type PublicIdentity struct {
	ID            string `json:"id"`
	EncryptionKey []byte `json:"encryption_key"`
	SignatureKey  []byte `json:"signature_key"`
}

// NewIdentity generates an X25519 ECDH key and Ed25519 signature key.
func NewIdentity(id string) (*Identity, error) {
	encPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signature key: %w", err)
	}
	return &Identity{
		public: PublicIdentity{
			ID:            id,
			EncryptionKey: encPriv.PublicKey().Bytes(),
			SignatureKey:  []byte(pub),
		},
		encPriv:  encPriv,
		signPriv: priv,
	}, nil
}

// Public returns the shareable identity.
func (i *Identity) Public() PublicIdentity {
	return PublicIdentity{
		ID:            i.public.ID,
		EncryptionKey: append([]byte(nil), i.public.EncryptionKey...),
		SignatureKey:  append([]byte(nil), i.public.SignatureKey...),
	}
}

// Sign creates an Ed25519 signature over message.
func (i *Identity) Sign(message []byte) []byte {
	return ed25519.Sign(i.signPriv, message)
}

func (i *Identity) encryptionPrivate() *ecdh.PrivateKey { return i.encPriv }

func verifySignature(pub ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(pub, message, signature)
}

func hkdfExtract(salt, inputKeyMaterial []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, hashSize)
	}
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(inputKeyMaterial)
	return mac.Sum(nil)
}

func hkdfExpand(pseudorandomKey, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > 255*hashSize {
		return nil, fmt.Errorf("treekem: invalid HKDF length %d", length)
	}
	out := make([]byte, 0, length)
	var previous []byte
	var counter byte = 1
	for len(out) < length {
		mac := hmac.New(sha256.New, pseudorandomKey)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		out = append(out, previous...)
		counter++
	}
	return out[:length], nil
}

func encodeLabelInfo(label string, context []byte, length uint16) []byte {
	fullLabel := []byte(mlsLabelPrefix + label)
	info := make([]byte, 0, 8+len(fullLabel)+len(context))
	var encodedLength [2]byte
	binary.BigEndian.PutUint16(encodedLength[:], length)
	info = append(info, encodedLength[:]...)
	binary.BigEndian.PutUint16(encodedLength[:], uint16(len(fullLabel)))
	info = append(info, encodedLength[:]...)
	info = append(info, fullLabel...)
	binary.BigEndian.PutUint16(encodedLength[:], uint16(len(context)))
	info = append(info, encodedLength[:]...)
	info = append(info, context...)
	return info
}

// ExpandWithLabel implements the RFC 9420 KDF labeled-expansion construction
// using HKDF-SHA256.
func ExpandWithLabel(secret []byte, label string, context []byte, length int) ([]byte, error) {
	if length <= 0 || length > 0xffff {
		return nil, fmt.Errorf("treekem: invalid labeled expansion length %d", length)
	}
	info := encodeLabelInfo(label, context, uint16(length))
	return hkdfExpand(secret, info, length)
}

func deriveSecret(secret []byte, label string, context []byte) ([]byte, error) {
	return ExpandWithLabel(secret, label, context, hashSize)
}

func derivePathSecret(previous []byte) ([]byte, error) {
	return deriveSecret(previous, "path", nil)
}

func derivePathPrivateKey(secret []byte, node Index) (*ecdh.PrivateKey, []byte, error) {
	var context [4]byte
	binary.BigEndian.PutUint32(context[:], uint32(node))
	seed, err := ExpandWithLabel(secret, "path-key", context[:], hashSize)
	if err != nil {
		return nil, nil, err
	}
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, nil, fmt.Errorf("derive X25519 key for node %d: %w", node, err)
	}
	return priv, priv.PublicKey().Bytes(), nil
}

func deriveCommitSecret(rootPathSecret []byte) ([]byte, error) {
	return deriveSecret(rootPathSecret, "commit", nil)
}

// Envelope is an authenticated X25519 ECDH + HKDF + AES-256-GCM one-to-one
// encryption wrapper. It models HPKE seal/open with standard-library only.
type Envelope struct {
	EphemeralPublicKey []byte `json:"ephemeral_public_key"`
	Nonce              []byte `json:"nonce"`
	Ciphertext         []byte `json:"ciphertext"`
}

func sealEnvelope(recipientPublicKey, additionalData, plaintext []byte) (*Envelope, error) {
	recipient, err := ecdh.X25519().NewPublicKey(recipientPublicKey)
	if err != nil {
		return nil, fmt.Errorf("recipient X25519 key: %w", err)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	ephPublic := ephemeral.PublicKey().Bytes()
	prk := hkdfExtract(ephPublic, shared)
	key, err := hkdfExpand(prk, []byte("treekem-envelope-v1-key"), 32)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, additionalData)
	return &Envelope{
		EphemeralPublicKey: ephPublic,
		Nonce:              nonce,
		Ciphertext:         ciphertext,
	}, nil
}

func openEnvelope(recipientPrivate *ecdh.PrivateKey, additionalData []byte, envelope *Envelope) ([]byte, error) {
	if envelope == nil {
		return nil, errors.New("nil envelope")
	}
	ephemeralPublic, err := ecdh.X25519().NewPublicKey(envelope.EphemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("ephemeral X25519 key: %w", err)
	}
	shared, err := recipientPrivate.ECDH(ephemeralPublic)
	if err != nil {
		return nil, err
	}
	prk := hkdfExtract(envelope.EphemeralPublicKey, shared)
	key, err := hkdfExpand(prk, []byte("treekem-envelope-v1-key"), 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, additionalData)
}

func randomSecret() ([]byte, error) {
	secret := make([]byte, hashSize)
	_, err := io.ReadFull(rand.Reader, secret)
	return secret, err
}

func constantTimeEqual(a, b []byte) bool { return hmac.Equal(a, b) }
