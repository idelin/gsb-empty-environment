package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
)

// Chunk is one ciphertext chunk and its zero-based position.
type Chunk struct {
	Index      uint64
	Ciphertext []byte
}

// SealOptions configures attachment sealing. FileID and FileKey are optional
// fixtures; when nil, fresh 32-byte random values are generated.
type SealOptions struct {
	ChunkSize      uint64
	Metadata       []byte
	SessionID      string
	MessageID      string
	SenderDeviceID string
	SigningKey     ed25519.PrivateKey
	Recipients     []Recipient

	// Random defaults to crypto/rand.Reader.
	Random io.Reader
	// FileID and FileKey allow deterministic fixture construction.
	FileID  *[FileIDSize]byte
	FileKey *[FileKeySize]byte
}

// SealedAttachment is a signed manifest together with ciphertext chunks in
// index order.
type SealedAttachment struct {
	Manifest *Manifest
	Chunks   []Chunk
}

// SealAttachment reads the complete plaintext from src, splits it into fixed
// chunks, encrypts each chunk independently, wraps the file key for every
// recipient device, and signs the resulting manifest.
func SealAttachment(src io.Reader, opts SealOptions) (*SealedAttachment, error) {
	if src == nil {
		return nil, fmt.Errorf("netsec: nil attachment source")
	}
	if opts.ChunkSize == 0 || opts.ChunkSize > MaxChunkSize {
		return nil, fmt.Errorf("%w: chunk size must be in [1, %d]", ErrInvalidManifest, MaxChunkSize)
	}
	if len(opts.SigningKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: bad sender signing key", ErrInvalidManifest)
	}
	if opts.SenderDeviceID == "" || len(opts.SenderDeviceID) > MaxDeviceIDLength {
		return nil, fmt.Errorf("%w: invalid sender device ID", ErrInvalidManifest)
	}
	if len(opts.Metadata) > MaxMetadataSize {
		return nil, fmt.Errorf("%w: metadata too large", ErrInvalidManifest)
	}
	if len(opts.SessionID) > MaxDeviceIDLength || len(opts.MessageID) > MaxDeviceIDLength {
		return nil, fmt.Errorf("%w: oversized session or message ID", ErrInvalidManifest)
	}
	if len(opts.Recipients) == 0 || len(opts.Recipients) > MaxManifestEnvelopes {
		return nil, fmt.Errorf("%w: invalid recipient count", ErrInvalidManifest)
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}

	recipients := make([]Recipient, len(opts.Recipients))
	seenRecipients := make(map[string]struct{}, len(opts.Recipients))
	for i, recipient := range opts.Recipients {
		validated, err := NewRecipient(recipient.DeviceID, recipient.PublicKey)
		if err != nil {
			return nil, err
		}
		if _, exists := seenRecipients[validated.DeviceID]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateRecipient, validated.DeviceID)
		}
		seenRecipients[validated.DeviceID] = struct{}{}
		recipients[i] = validated
	}

	fileID, fileKey, err := fileIdentity(random, opts.FileID, opts.FileKey)
	if err != nil {
		return nil, err
	}
	plaintextChunks, totalLength, err := splitPlaintext(src, int(opts.ChunkSize))
	if err != nil {
		return nil, err
	}
	manifest := &Manifest{
		Version:        ProtocolVersion,
		FileID:         fileID,
		ChunkSize:      opts.ChunkSize,
		TotalLength:    totalLength,
		TotalChunks:    uint64(len(plaintextChunks)),
		Metadata:       append([]byte(nil), opts.Metadata...),
		SenderDeviceID: opts.SenderDeviceID,
		SessionID:      opts.SessionID,
		MessageID:      opts.MessageID,
	}
	manifest.MetadataDigest = HashMetadata(manifest.Metadata)

	aead, err := newGCM(fileKey)
	if err != nil {
		return nil, err
	}
	chunks := make([]Chunk, len(plaintextChunks))
	manifest.ChunkSHA256 = make([][32]byte, len(plaintextChunks))
	for i, plaintext := range plaintextChunks {
		index := uint64(i)
		plaintextLength := uint64(len(plaintext))
		nonce := DeriveChunkNonce(fileKey, fileID, index)
		aad := BuildChunkAAD(
			manifest.Version,
			fileID,
			index,
			manifest.TotalChunks,
			manifest.TotalLength,
			plaintextLength,
			manifest.MetadataDigest,
		)
		ciphertext := aead.Seal(nil, nonce[:], plaintext, aad)
		chunks[i] = Chunk{Index: index, Ciphertext: ciphertext}
		manifest.ChunkSHA256[i] = HashCiphertext(ciphertext)
	}
	manifest.MerkleRoot = ComputeMerkleRoot(manifest.ChunkSHA256)

	manifest.Envelopes = make([]RecipientEnvelope, len(recipients))
	for i, recipient := range recipients {
		envelope, err := sealFileKey(
			random,
			recipient.PublicKey,
			fileKey,
			manifest.Version,
			fileID,
			recipient.DeviceID,
			manifest.SenderDeviceID,
			manifest.SessionID,
			manifest.MessageID,
		)
		if err != nil {
			return nil, err
		}
		manifest.Envelopes[i] = envelope
	}
	if err := manifest.Sign(opts.SigningKey); err != nil {
		return nil, err
	}
	return &SealedAttachment{Manifest: manifest, Chunks: chunks}, nil
}

// Open verifies and decrypts all chunks in this sealed attachment.
func (a *SealedAttachment) Open(opts OpenOptions) ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil sealed attachment", ErrInvalidManifest)
	}
	return OpenAttachment(a.Manifest, a.Chunks, opts)
}

// OpenToFile verifies this sealed attachment and atomically publishes it at
// dest. OnFile is invoked only after successful publication.
func (a *SealedAttachment) OpenToFile(dest string, opts OpenOptions) error {
	if a == nil {
		return fmt.Errorf("%w: nil sealed attachment", ErrInvalidManifest)
	}
	return OpenAttachmentToFile(a.Manifest, a.Chunks, dest, opts)
}

func fileIdentity(
	random io.Reader,
	fileIDOverride *[FileIDSize]byte,
	fileKeyOverride *[FileKeySize]byte,
) ([FileIDSize]byte, [FileKeySize]byte, error) {
	var fileID [FileIDSize]byte
	var fileKey [FileKeySize]byte
	if fileIDOverride != nil {
		fileID = *fileIDOverride
	} else if _, err := io.ReadFull(random, fileID[:]); err != nil {
		return fileID, fileKey, fmt.Errorf("netsec: generate file ID: %w", err)
	}
	if fileKeyOverride != nil {
		fileKey = *fileKeyOverride
	} else if _, err := io.ReadFull(random, fileKey[:]); err != nil {
		return fileID, fileKey, fmt.Errorf("netsec: generate file key: %w", err)
	}
	if isZero32(fileID) {
		return fileID, fileKey, fmt.Errorf("%w: zero file ID", ErrInvalidManifest)
	}
	if isZero32(fileKey) {
		return fileID, fileKey, fmt.Errorf("%w: zero file key", ErrInvalidManifest)
	}
	return fileID, fileKey, nil
}

func splitPlaintext(src io.Reader, chunkSize int) ([][]byte, uint64, error) {
	if chunkSize <= 0 || uint64(chunkSize) > MaxChunkSize {
		return nil, 0, fmt.Errorf("%w: invalid chunk size", ErrInvalidManifest)
	}
	var chunks [][]byte
	var total uint64
	buffer := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(src, buffer)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, 0, fmt.Errorf("netsec: read attachment: %w", err)
		}
		if n == 0 {
			break
		}
		if uint64(n) > ^uint64(0)-total {
			return nil, 0, fmt.Errorf("%w: total length overflow", ErrInvalidManifest)
		}
		plaintext := append([]byte(nil), buffer[:n]...)
		chunks = append(chunks, plaintext)
		total += uint64(n)
		if len(chunks) > MaxManifestChunks {
			return nil, 0, fmt.Errorf("%w: too many chunks", ErrInvalidManifest)
		}
		if n < len(buffer) {
			break
		}
	}
	return chunks, total, nil
}
