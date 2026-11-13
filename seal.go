package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

// ShareAssociatedData binds a sealed share to the full ceremony binding and
// the participant's immutable person ID.
func ShareAssociatedData(binding CeremonyBinding, personID string) []byte {
	payload := canonicalField(nil, []byte("netsec.hsm-import-share-aad.v1"))
	payload = bindingSigningFields(payload, binding)
	payload = canonicalString(payload, personID)
	return payload
}

func sealShare(binding CeremonyBinding, wrappingPublicKey []byte, credentialID, personID string, privateKey ed25519.PrivateKey, share [32]byte) (*SealedShare, error) {
	if len(privateKey) != ed25519.PrivateKeySize || len(wrappingPublicKey) != 32 || personID == "" || credentialID == "" {
		return nil, ErrInvalidArgument
	}
	digest := sha256.Sum256(wrappingPublicKey)
	if digest != binding.WrappingKeyDigest || binding.CeremonyID == "" {
		return nil, fmt.Errorf("%w: wrapping key does not match the ceremony binding", ErrInvalidArgument)
	}
	defer zeroBytes(share[:])
	recipient, err := ecdh.X25519().NewPublicKey(wrappingPublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sharedSecret, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	key := hkdfSHA256(sharedSecret, nil, []byte("netsec.hsm-import-share-key.v1"))
	zeroBytes(sharedSecret)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	envelope := &SealedShare{
		CeremonyID:         binding.CeremonyID,
		PersonID:           personID,
		CredentialID:       credentialID,
		EphemeralPublicKey: append([]byte(nil), ephemeral.PublicKey().Bytes()...),
		Nonce:              nonce,
	}
	aad := ShareAssociatedData(binding, personID)
	envelope.Ciphertext = gcm.Seal(nil, nonce, share[:], aad)
	envelope.Signature = ed25519.Sign(privateKey, EnvelopeSigningPayload(*envelope))
	return envelope, nil
}

// OpenWrappedShare is provided for HSM implementations. The recipient private
// key must remain inside the HSM; the host never calls this function.
func OpenWrappedShare(binding CeremonyBinding, recipientPrivateKey []byte, wrapped WrappedShare) ([32]byte, error) {
	var share [32]byte
	if len(recipientPrivateKey) != 32 || len(wrapped.EphemeralPublicKey) != 32 ||
		len(wrapped.Nonce) != 12 || len(wrapped.Ciphertext) != 48 {
		return share, ErrInvalidArgument
	}
	recipient, err := ecdh.X25519().NewPrivateKey(recipientPrivateKey)
	if err != nil {
		return share, err
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(wrapped.EphemeralPublicKey)
	if err != nil {
		return share, err
	}
	sharedSecret, err := recipient.ECDH(ephemeral)
	if err != nil {
		return share, err
	}
	key := hkdfSHA256(sharedSecret, nil, []byte("netsec.hsm-import-share-key.v1"))
	zeroBytes(sharedSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return share, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return share, err
	}
	aad := ShareAssociatedData(binding, wrapped.PersonID)
	plaintext, err := gcm.Open(nil, wrapped.Nonce, wrapped.Ciphertext, aad)
	if err != nil {
		return share, fmt.Errorf("%w: sealed share authentication failed", ErrEnvelopeMismatch)
	}
	if len(plaintext) != len(share) {
		zeroBytes(plaintext)
		return share, fmt.Errorf("%w: share length", ErrInvalidArgument)
	}
	copy(share[:], plaintext)
	zeroBytes(plaintext)
	return share, nil
}

func hkdfSHA256(secret, salt, info []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	extract := hmac.New(sha256.New, salt)
	extract.Write(secret)
	prk := extract.Sum(nil)

	expand := hmac.New(sha256.New, prk)
	expand.Write(info)
	expand.Write([]byte{0x01})
	return expand.Sum(nil)
}
