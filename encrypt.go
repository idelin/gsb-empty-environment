package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// RecipientKey identifies a recipient and its X25519 public key.
type RecipientKey struct {
	KeyID     []byte
	PublicKey *ecdh.PublicKey
}

// GenerateKey returns a fresh X25519 key pair for a session party.
func GenerateKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// kdfInfo builds the COSE_KDF_Context-style info string binding the derived
// key to the algorithm, the session context and the recipient.
func kdfInfo(alg string, ctx *Context, keyID []byte) []byte {
	return encodeFields([]byte(alg), ctx.encode(), keyID)
}

// contentAAD builds the COSE Enc_structure-style external AAD.
func contentAAD(protected, aad []byte) []byte {
	return encodeFields([]byte("Encrypt0"), protected, aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// wrapKey encrypts cek under kek, returning nonce || ciphertext.
func wrapKey(kek, cek []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, cek, nil), nil
}

// unwrapKey reverses wrapKey.
func unwrapKey(kek, wrapped []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < gcm.NonceSize() {
		return nil, ErrDecryption
	}
	return gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], nil)
}

// deriveKEK performs ECDH-ES between priv and the peer's public key, and
// derives the key-encryption key bound to the given context and recipient.
func deriveKEK(alg string, ctx *Context, keyID, peerPub []byte, priv *ecdh.PrivateKey) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid ephemeral key: %w", err)
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	return hkdf.Key(sha256.New, shared, nil, string(kdfInfo(alg, ctx, keyID)), 32)
}

// Encrypt builds a COSE_Encrypt-style message for the given recipients. The
// key-agreement context is committed into every recipient's protected
// header and into its key derivation, so a recipient is only meaningful
// inside its own session.
func Encrypt(ctx *Context, plaintext, aad []byte, recipients []*RecipientKey) (*Message, error) {
	if ctx == nil {
		return nil, ErrMissingContext
	}
	if len(recipients) == 0 {
		return nil, errors.New("netsec: no recipients")
	}

	cek, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	defer clear(cek)

	protected, err := json.Marshal(protectedHeader{Algorithm: ContentAlgorithm})
	if err != nil {
		return nil, err
	}

	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	msg := &Message{
		Protected:  protected,
		Ciphertext: gcm.Seal(nonce, nonce, plaintext, contentAAD(protected, aad)),
	}

	for _, rk := range recipients {
		if rk == nil || rk.PublicKey == nil {
			return nil, errors.New("netsec: recipient missing public key")
		}
		eph, err := GenerateKey()
		if err != nil {
			return nil, err
		}
		kek, err := deriveKEK(KeyAlgorithm, ctx, rk.KeyID, rk.PublicKey.Bytes(), eph)
		if err != nil {
			return nil, err
		}
		wrapped, err := wrapKey(kek, cek)
		clear(kek)
		if err != nil {
			return nil, err
		}
		rh, err := json.Marshal(recipientHeader{
			Algorithm:    KeyAlgorithm,
			KeyID:        rk.KeyID,
			EphemeralKey: eph.PublicKey().Bytes(),
			Context:      ctx,
		})
		if err != nil {
			return nil, err
		}
		msg.Recipients = append(msg.Recipients, &Recipient{Protected: rh, EncryptedKey: wrapped})
	}
	return msg, nil
}
