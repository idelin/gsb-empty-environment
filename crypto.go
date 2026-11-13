package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// ProtocolVersion is the attachment-manifest and chunk-AAD version.
	ProtocolVersion uint16 = 1

	// FileIDSize and FileKeySize are the required random identifier and key
	// sizes.
	FileIDSize  = 32
	FileKeySize = 32

	// ChunkNonceSize is the truncated HMAC-SHA256 nonce length used by GCM.
	ChunkNonceSize = 12

	// GCMTagSize is the AES-256-GCM authentication tag length.
	GCMTagSize = 16

	// MaxChunkSize is the largest plaintext chunk accepted by this package.
	// It keeps the default in-memory sender bounded; deployments with larger
	// chunks should select a size below this limit.
	MaxChunkSize = 64 << 20
)

var (
	chunkNonceLabel = []byte("nonce")
	chunkAADDomain  = []byte("netsec/q0152/chunk-aad/v1\x00")
	wrapAADDomain   = []byte("netsec/q0152/file-key-wrap-aad/v1\x00")
	wrapSaltDomain  = []byte("netsec/q0152/file-key-wrap-salt/v1\x00")
	wrapInfoDomain  = []byte("netsec/q0152/file-key-wrap-info/v1\x00")
)

// DeriveChunkNonce computes
// HMAC-SHA256(fileKey, "nonce" || fileID || uint64_be(index))[0:12].
func DeriveChunkNonce(fileKey [FileKeySize]byte, fileID [FileIDSize]byte, index uint64) [ChunkNonceSize]byte {
	mac := hmac.New(sha256.New, fileKey[:])
	_, _ = mac.Write(chunkNonceLabel)
	_, _ = mac.Write(fileID[:])
	var encodedIndex [8]byte
	binary.BigEndian.PutUint64(encodedIndex[:], index)
	_, _ = mac.Write(encodedIndex[:])
	sum := mac.Sum(nil)
	var nonce [ChunkNonceSize]byte
	copy(nonce[:], sum[:ChunkNonceSize])
	return nonce
}

// BuildChunkAAD returns the canonical associated data for a chunk. It binds
// the protocol version, file identity, position, complete attachment shape,
// this chunk's plaintext length, and the metadata digest.
func BuildChunkAAD(
	version uint16,
	fileID [FileIDSize]byte,
	index, totalChunks, totalLength, plaintextLength uint64,
	metadataDigest [32]byte,
) []byte {
	aad := make([]byte, 0, len(chunkAADDomain)+2+FileIDSize+8*4+32)
	aad = append(aad, chunkAADDomain...)
	aad = binary.BigEndian.AppendUint16(aad, version)
	aad = append(aad, fileID[:]...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	aad = binary.BigEndian.AppendUint64(aad, totalChunks)
	aad = binary.BigEndian.AppendUint64(aad, totalLength)
	aad = binary.BigEndian.AppendUint64(aad, plaintextLength)
	aad = append(aad, metadataDigest[:]...)
	return aad
}

func newGCM(key [FileKeySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("netsec: create AES-256 cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create GCM: %w", err)
	}
	if aead.NonceSize() != ChunkNonceSize || aead.Overhead() != GCMTagSize {
		return nil, fmt.Errorf("netsec: unexpected GCM parameters")
	}
	return aead, nil
}

func hkdfSHA256(secret, salt, info []byte, length int) []byte {
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(secret)
	prk := extract.Sum(nil)

	out := make([]byte, 0, length)
	var previous []byte
	var counter byte = 1
	for len(out) < length {
		expand := hmac.New(sha256.New, prk)
		_, _ = expand.Write(previous)
		_, _ = expand.Write(info)
		_, _ = expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		out = append(out, previous...)
		counter++
	}
	return out[:length]
}

func wrapAAD(version uint16, fileID [FileIDSize]byte, recipientDeviceID, senderDeviceID, sessionID, messageID string) []byte {
	aad := make([]byte, 0, len(wrapAADDomain)+2+FileIDSize+4*4+len(recipientDeviceID)+len(senderDeviceID)+len(sessionID)+len(messageID))
	aad = append(aad, wrapAADDomain...)
	aad = binary.BigEndian.AppendUint16(aad, version)
	aad = append(aad, fileID[:]...)
	aad = appendLengthPrefixed(aad, []byte(recipientDeviceID))
	aad = appendLengthPrefixed(aad, []byte(senderDeviceID))
	aad = appendLengthPrefixed(aad, []byte(sessionID))
	aad = appendLengthPrefixed(aad, []byte(messageID))
	return aad
}

func deriveWrapKey(sharedSecret []byte, version uint16, fileID [FileIDSize]byte, recipientDeviceID string) [FileKeySize]byte {
	saltHash := sha256.New()
	_, _ = saltHash.Write(wrapSaltDomain)
	_, _ = saltHash.Write(fileID[:])
	salt := saltHash.Sum(nil)

	info := make([]byte, 0, len(wrapInfoDomain)+2+FileIDSize+4+len(recipientDeviceID))
	info = append(info, wrapInfoDomain...)
	info = binary.BigEndian.AppendUint16(info, version)
	info = append(info, fileID[:]...)
	info = appendLengthPrefixed(info, []byte(recipientDeviceID))

	var key [FileKeySize]byte
	copy(key[:], hkdfSHA256(sharedSecret, salt, info, FileKeySize))
	return key
}

func sealFileKey(
	random io.Reader,
	recipientPublic *ecdh.PublicKey,
	fileKey [FileKeySize]byte,
	version uint16,
	fileID [FileIDSize]byte,
	recipientDeviceID, senderDeviceID, sessionID, messageID string,
) (RecipientEnvelope, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return RecipientEnvelope{}, fmt.Errorf("netsec: generate ephemeral key: %w", err)
	}
	sharedSecret, err := ephemeral.ECDH(recipientPublic)
	if err != nil {
		return RecipientEnvelope{}, fmt.Errorf("netsec: derive recipient shared secret: %w", err)
	}
	wrapKey := deriveWrapKey(sharedSecret, version, fileID, recipientDeviceID)
	aead, err := newGCM(wrapKey)
	if err != nil {
		return RecipientEnvelope{}, err
	}
	var nonce [ChunkNonceSize]byte
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return RecipientEnvelope{}, fmt.Errorf("netsec: generate key-wrap nonce: %w", err)
	}
	aad := wrapAAD(version, fileID, recipientDeviceID, senderDeviceID, sessionID, messageID)
	wrapped := aead.Seal(nil, nonce[:], fileKey[:], aad)

	envelope := RecipientEnvelope{
		RecipientDeviceID: recipientDeviceID,
		Nonce:             nonce,
		WrappedFileKey:    wrapped,
	}
	copy(envelope.EphemeralPublicKey[:], ephemeral.PublicKey().Bytes())
	return envelope, nil
}

func openFileKey(
	recipientPrivate *ecdh.PrivateKey,
	envelope RecipientEnvelope,
	version uint16,
	fileID [FileIDSize]byte,
	senderDeviceID, sessionID, messageID string,
) ([FileKeySize]byte, error) {
	var zero [FileKeySize]byte
	if recipientPrivate == nil {
		return zero, fmt.Errorf("netsec: missing recipient encryption private key")
	}
	ephemeralPublic, err := ecdh.X25519().NewPublicKey(envelope.EphemeralPublicKey[:])
	if err != nil {
		return zero, fmt.Errorf("%w: bad ephemeral public key", ErrInvalidManifest)
	}
	sharedSecret, err := recipientPrivate.ECDH(ephemeralPublic)
	if err != nil {
		return zero, fmt.Errorf("netsec: derive recipient shared secret: %w", err)
	}
	wrapKey := deriveWrapKey(sharedSecret, version, fileID, envelope.RecipientDeviceID)
	aead, err := newGCM(wrapKey)
	if err != nil {
		return zero, err
	}
	if len(envelope.WrappedFileKey) != FileKeySize+GCMTagSize {
		return zero, fmt.Errorf("%w: bad wrapped file key length", ErrInvalidManifest)
	}
	aad := wrapAAD(version, fileID, envelope.RecipientDeviceID, senderDeviceID, sessionID, messageID)
	plaintext, err := aead.Open(nil, envelope.Nonce[:], envelope.WrappedFileKey, aad)
	if err != nil {
		return zero, fmt.Errorf("netsec: unwrap file key: %w", ErrChunkAuthentication)
	}
	if len(plaintext) != FileKeySize {
		return zero, fmt.Errorf("%w: bad file key length", ErrInvalidManifest)
	}
	var fileKey [FileKeySize]byte
	copy(fileKey[:], plaintext)
	return fileKey, nil
}
