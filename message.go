package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
)

// MaxSkippedKeys is the maximum number of skipped message keys cached per
// group state. The bound is global across all sender chains in the group.
const MaxSkippedKeys = 128

// Header is the cleartext header of a data message. Every field participates
// in AEAD associated data, so tampering with any of them fails authentication.
type Header struct {
	GroupID      []byte
	Epoch        uint64
	SenderDevice DeviceID
	ChainID      ChainID
	Index        uint64
}

// EncodeHeader produces the canonical, unambiguous encoding of a header.
func EncodeHeader(h Header) []byte {
	buf := make([]byte, 0, 4+len(h.GroupID)+8+len(h.SenderDevice)+len(h.ChainID)+8)
	buf = append(buf, encodeLengthPrefixed(h.GroupID)...)

	var fixed [8]byte
	binary.BigEndian.PutUint64(fixed[:], h.Epoch)
	buf = append(buf, fixed[:]...)

	buf = append(buf, h.SenderDevice[:]...)
	buf = append(buf, h.ChainID[:]...)

	binary.BigEndian.PutUint64(fixed[:], h.Index)
	buf = append(buf, fixed[:]...)
	return buf
}

// EncodeAAD builds the AEAD associated data: the full message header followed
// by the member-set digest in effect for the message epoch.
func EncodeAAD(h Header, membersDigest Digest) []byte {
	buf := make([]byte, 0, len(EncodeHeader(h))+len(membersDigest))
	buf = append(buf, EncodeHeader(h)...)
	buf = append(buf, membersDigest[:]...)
	return buf
}

// DeriveMessageKey returns the one-time message key for index:
// HMAC-SHA256(chainKey, "msg" || index).
func DeriveMessageKey(chainKey []byte, index uint64) []byte {
	mac := hmac.New(sha256.New, chainKey)
	mac.Write([]byte("msg"))
	mac.Write(encodeUint64(index))
	return mac.Sum(nil)
}

// DeriveNextChainKey returns the ratcheted chain key for index:
// HMAC-SHA256(chainKey, "chain" || index).
func DeriveNextChainKey(chainKey []byte, index uint64) []byte {
	mac := hmac.New(sha256.New, chainKey)
	mac.Write([]byte("chain"))
	mac.Write(encodeUint64(index))
	return mac.Sum(nil)
}

// Seal encrypts plaintext with AES-256-GCM under messageKey and binds aad.
// It returns a fresh random nonce and the sealed ciphertext. It is a
// convenience for hosts that do not supply their own AEAD layer.
func Seal(messageKey, plaintext, aad []byte) (nonce []byte, ciphertext []byte, err error) {
	block, err := aes.NewCipher(messageKey)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// Open authenticates and decrypts an AES-256-GCM message under messageKey.
// It returns ErrAuth when authentication (including AAD binding) fails.
func Open(messageKey, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(messageKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrAuth
	}
	return plaintext, nil
}
