package netsec

import (
	"bytes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OpenOptions supplies recipient key material, sender trust, and optional
// expected message context. SenderPublicKey takes precedence over
// TrustedSenders when non-empty.
type OpenOptions struct {
	DeviceID             string
	EncryptionPrivateKey *ecdh.PrivateKey
	TrustedSenders       map[string]ed25519.PublicKey
	SenderPublicKey      ed25519.PublicKey

	// ExpectedSessionID and ExpectedMessageID, when non-empty, must exactly
	// match the signed manifest context.
	ExpectedSessionID string
	ExpectedMessageID string
	// ExpectedMetadata, when non-nil, must exactly match signed metadata.
	ExpectedMetadata []byte

	// OnFile is called once with the destination path after an attachment has
	// been fully verified and atomically published. It is never called on
	// validation, decryption, or publication failure.
	OnFile func(path string)
}

// ChunkSource yields chunks in caller-selected request order. It returns
// io.EOF after the final chunk.
type ChunkSource interface {
	NextChunk() (Chunk, error)
}

// ChunkSourceFunc adapts a function to ChunkSource.
type ChunkSourceFunc func() (Chunk, error)

// NextChunk implements ChunkSource.
func (f ChunkSourceFunc) NextChunk() (Chunk, error) {
	return f()
}

// SliceChunkSource returns a source over a slice. Chunk order is preserved and
// is not required to be index order.
func SliceChunkSource(chunks []Chunk) ChunkSource {
	next := 0
	return ChunkSourceFunc(func() (Chunk, error) {
		if next >= len(chunks) {
			return Chunk{}, io.EOF
		}
		chunk := chunks[next]
		next++
		return chunk, nil
	})
}

// Reader is an authenticated attachment reader. OpenReader verifies the
// manifest signature and recipient envelope before constructing it. Chunks may
// then be submitted in any order exactly once. Any failed chunk permanently
// fails the reader.
type Reader struct {
	manifest *Manifest
	fileKey  [FileKeySize]byte
	aead     cipher.AEAD

	seen        []bool
	seenCount   uint64
	plainLength uint64
	failed      error
	finished    bool
}

// OpenReader verifies the manifest signature and selects/decrypts exactly one
// recipient envelope for opts.DeviceID. No chunk is touched until both checks
// succeed.
func OpenReader(manifest *Manifest, opts OpenOptions) (*Reader, error) {
	if manifest == nil {
		return nil, fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	// Freeze all authenticated fields. Chunk verification must never consult a
	// manifest that a caller could mutate after signature verification.
	verified := manifest.Clone()
	senderKey, err := trustedSenderKey(verified, opts)
	if err != nil {
		return nil, err
	}
	if err := verified.VerifySignature(senderKey); err != nil {
		return nil, err
	}
	if err := verified.ValidateStructure(); err != nil {
		return nil, err
	}
	if opts.ExpectedSessionID != "" && opts.ExpectedSessionID != verified.SessionID {
		return nil, fmt.Errorf("%w: unexpected session ID", ErrInvalidManifest)
	}
	if opts.ExpectedMessageID != "" && opts.ExpectedMessageID != verified.MessageID {
		return nil, fmt.Errorf("%w: unexpected message ID", ErrInvalidManifest)
	}
	if opts.ExpectedMetadata != nil && !bytes.Equal(opts.ExpectedMetadata, verified.Metadata) {
		return nil, ErrMetadataMismatch
	}
	if opts.DeviceID == "" {
		return nil, fmt.Errorf("%w: empty recipient device ID", ErrRecipientNotFound)
	}

	var selected *RecipientEnvelope
	for i := range verified.Envelopes {
		if verified.Envelopes[i].RecipientDeviceID != opts.DeviceID {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateRecipient, opts.DeviceID)
		}
		selected = &verified.Envelopes[i]
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: %q", ErrRecipientNotFound, opts.DeviceID)
	}
	fileKey, err := openFileKey(
		opts.EncryptionPrivateKey,
		*selected,
		verified.Version,
		verified.FileID,
		verified.SenderDeviceID,
		verified.SessionID,
		verified.MessageID,
	)
	if err != nil {
		return nil, err
	}
	if isZero32(fileKey) {
		return nil, fmt.Errorf("%w: zero unwrapped file key", ErrInvalidManifest)
	}
	gcm, err := newGCM(fileKey)
	if err != nil {
		return nil, err
	}
	return &Reader{
		manifest: verified,
		fileKey:  fileKey,
		aead:     gcm,
		seen:     make([]bool, int(verified.TotalChunks)),
	}, nil
}

// Manifest returns a copy of the frozen manifest verified by OpenReader.
func (r *Reader) Manifest() *Manifest {
	if r == nil {
		return nil
	}
	return r.manifest.Clone()
}

// ReadChunk verifies, authenticates, and returns one plaintext chunk.
func (r *Reader) ReadChunk(index uint64, ciphertext []byte) ([]byte, error) {
	var buffer bytes.Buffer
	if err := r.ReadChunkTo(index, ciphertext, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// ReadChunkTo verifies a chunk before writing any plaintext to dst. Writes
// occur only after digest, Merkle path, and AEAD verification have all passed.
func (r *Reader) ReadChunkTo(index uint64, ciphertext []byte, dst io.Writer) error {
	if err := r.usable(); err != nil {
		return err
	}
	if dst == nil {
		return r.fail(fmt.Errorf("netsec: nil chunk destination"))
	}
	if index >= r.manifest.TotalChunks {
		return r.fail(fmt.Errorf("%w: %d", ErrChunkOutOfRange, index))
	}
	if r.seen[index] {
		return r.fail(fmt.Errorf("%w: %d", ErrDuplicateChunk, index))
	}
	expectedLength, ok := expectedPlaintextLength(r.manifest, index)
	if !ok || expectedLength == 0 || expectedLength > MaxChunkSize {
		return r.fail(fmt.Errorf("%w: non-canonical chunk %d", ErrLengthMismatch, index))
	}

	leaf := HashCiphertext(ciphertext)
	if !hmac.Equal(leaf[:], r.manifest.ChunkSHA256[index][:]) {
		return r.fail(fmt.Errorf("%w: chunk %d", ErrChunkDigestMismatch, index))
	}
	proof, err := MerkleProof(r.manifest.ChunkSHA256, index)
	if err != nil {
		return r.fail(err)
	}
	if !VerifyMerkleProof(leaf, index, r.manifest.TotalChunks, proof, r.manifest.MerkleRoot) {
		return r.fail(fmt.Errorf("%w: chunk %d Merkle path", ErrInvalidManifest, index))
	}
	if uint64(len(ciphertext)) != expectedLength+GCMTagSize {
		return r.fail(fmt.Errorf("%w: chunk %d ciphertext length", ErrLengthMismatch, index))
	}

	nonce := DeriveChunkNonce(r.fileKey, r.manifest.FileID, index)
	aad := BuildChunkAAD(
		r.manifest.Version,
		r.manifest.FileID,
		index,
		r.manifest.TotalChunks,
		r.manifest.TotalLength,
		expectedLength,
		r.manifest.MetadataDigest,
	)
	plaintext, err := r.aead.Open(nil, nonce[:], ciphertext, aad)
	if err != nil {
		return r.fail(fmt.Errorf("%w: chunk %d", ErrChunkAuthentication, index))
	}
	if uint64(len(plaintext)) != expectedLength {
		return r.fail(fmt.Errorf("%w: chunk %d plaintext length", ErrLengthMismatch, index))
	}
	if err := writeAll(dst, plaintext); err != nil {
		return r.fail(fmt.Errorf("netsec: write verified chunk: %w", err))
	}
	r.seen[index] = true
	r.seenCount++
	r.plainLength += uint64(len(plaintext))
	return nil
}

// Finish verifies that every chunk was read exactly once and that the aggregate
// plaintext length matches the signed total.
func (r *Reader) Finish() error {
	if r == nil {
		return fmt.Errorf("%w: nil reader", ErrInvalidManifest)
	}
	if r.failed != nil {
		return r.failed
	}
	if r.finished {
		return nil
	}
	if r.seenCount != r.manifest.TotalChunks {
		return r.fail(fmt.Errorf("%w: have %d of %d", ErrMissingChunks, r.seenCount, r.manifest.TotalChunks))
	}
	if r.plainLength != r.manifest.TotalLength {
		return r.fail(fmt.Errorf("%w: have %d bytes, want %d", ErrLengthMismatch, r.plainLength, r.manifest.TotalLength))
	}
	r.finished = true
	return nil
}

func (r *Reader) usable() error {
	if r == nil {
		return fmt.Errorf("%w: nil reader", ErrInvalidManifest)
	}
	if r.failed != nil {
		return r.failed
	}
	if r.finished {
		return ErrReaderFinished
	}
	return nil
}

func (r *Reader) fail(err error) error {
	if r.failed == nil {
		r.failed = err
	}
	return r.failed
}

// OpenAttachment verifies and decrypts a slice of chunks in the supplied
// request order. It returns bytes only after every chunk and the final total
// length have been verified.
func OpenAttachment(manifest *Manifest, chunks []Chunk, opts OpenOptions) ([]byte, error) {
	return OpenAttachmentStream(manifest, SliceChunkSource(chunks), opts)
}

// OpenAttachmentStream verifies and decrypts chunks from src into memory.
func OpenAttachmentStream(manifest *Manifest, src ChunkSource, opts OpenOptions) ([]byte, error) {
	reader, err := OpenReader(manifest, opts)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("netsec: nil chunk source")
	}
	var output bytes.Buffer
	for {
		chunk, err := src.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("netsec: read chunk source: %w", err)
		}
		if err := reader.ReadChunkTo(chunk.Index, chunk.Ciphertext, &output); err != nil {
			return nil, err
		}
	}
	if err := reader.Finish(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// OpenAttachmentToFile writes verified chunks to an isolated temporary object
// in the destination directory and atomically renames it only after all chunks
// and the final length verify. OnFile is never called on failure.
func OpenAttachmentToFile(manifest *Manifest, chunks []Chunk, dest string, opts OpenOptions) error {
	return OpenAttachmentStreamToFile(manifest, SliceChunkSource(chunks), dest, opts)
}

// OpenAttachmentStreamToFile is the streaming form of OpenAttachmentToFile.
func OpenAttachmentStreamToFile(manifest *Manifest, src ChunkSource, dest string, opts OpenOptions) (err error) {
	if dest == "" {
		return fmt.Errorf("netsec: empty destination path")
	}
	if src == nil {
		return fmt.Errorf("netsec: nil chunk source")
	}
	reader, err := OpenReader(manifest, opts)
	if err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(dest), ".netsec-attachment-*")
	if err != nil {
		return fmt.Errorf("netsec: create isolated temporary attachment: %w", err)
	}
	tempName := tempFile.Name()
	published := false
	defer func() {
		if !published {
			_ = tempFile.Close()
			_ = os.Remove(tempName)
		}
	}()

	for {
		chunk, nextErr := src.NextChunk()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("netsec: read chunk source: %w", nextErr)
		}
		if err := reader.ReadChunkTo(chunk.Index, chunk.Ciphertext, tempFile); err != nil {
			return err
		}
	}
	if err := reader.Finish(); err != nil {
		return err
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("netsec: sync temporary attachment: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("netsec: close temporary attachment: %w", err)
	}
	if err := os.Rename(tempName, dest); err != nil {
		return fmt.Errorf("netsec: publish attachment: %w", err)
	}
	published = true
	if opts.OnFile != nil {
		opts.OnFile(dest)
	}
	return nil
}

func trustedSenderKey(manifest *Manifest, opts OpenOptions) (ed25519.PublicKey, error) {
	if len(opts.SenderPublicKey) > 0 {
		if len(opts.SenderPublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: bad sender public key length", ErrUntrustedSender)
		}
		return opts.SenderPublicKey, nil
	}
	if opts.TrustedSenders != nil {
		if publicKey, ok := opts.TrustedSenders[manifest.SenderDeviceID]; ok {
			if len(publicKey) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("%w: bad sender public key length", ErrUntrustedSender)
			}
			return publicKey, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrUntrustedSender, manifest.SenderDeviceID)
}

func writeAll(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		if n > len(data) {
			return fmt.Errorf("netsec: invalid writer count %d", n)
		}
		data = data[n:]
	}
	return nil
}
