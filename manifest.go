package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
)

const (
	// MaxManifestChunks bounds malicious manifest sizes during decoding.
	MaxManifestChunks = 1 << 20
	// MaxMetadataSize bounds metadata carried in a manifest.
	MaxMetadataSize = 16 << 20
	// MaxManifestEnvelopes bounds recipient envelopes in one manifest.
	MaxManifestEnvelopes = 1 << 16
	// MaxDeviceIDLength bounds serialized device and message identifiers.
	MaxDeviceIDLength = 1 << 20
)

var (
	manifestSignatureDomain = []byte("netsec/q0152/attachment-manifest/v1\x00")
	manifestBinaryMagic     = []byte("NSAQ0152\x01")
)

// RecipientEnvelope contains a file key wrapped for one recipient device.
// EphemeralPublicKey is an X25519 public key; the wrapped key is AES-256-GCM
// ciphertext bound to the manifest identity and recipient device identifier.
type RecipientEnvelope struct {
	RecipientDeviceID  string
	EphemeralPublicKey [32]byte
	Nonce              [ChunkNonceSize]byte
	WrappedFileKey     []byte
}

// Manifest is the authenticated description of a chunked attachment.
//
// Signature covers every field in this structure except Signature itself. The
// host must supply the sender's trusted Ed25519 public key separately; a
// manifest never certifies its own signer.
type Manifest struct {
	Version     uint16
	FileID      [FileIDSize]byte
	ChunkSize   uint64
	TotalLength uint64
	TotalChunks uint64

	Metadata       []byte
	MetadataDigest [32]byte

	// ChunkSHA256[i] is SHA256 of the ciphertext for chunk i.
	ChunkSHA256 [][32]byte
	MerkleRoot  [32]byte

	SenderDeviceID string
	SessionID      string
	MessageID      string
	Envelopes      []RecipientEnvelope
	Signature      []byte
}

// Clone returns a deep copy of the manifest.
func (m *Manifest) Clone() *Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Metadata = append([]byte(nil), m.Metadata...)
	clone.ChunkSHA256 = append([][32]byte(nil), m.ChunkSHA256...)
	clone.Envelopes = make([]RecipientEnvelope, len(m.Envelopes))
	for i, envelope := range m.Envelopes {
		clone.Envelopes[i] = envelope
		clone.Envelopes[i].WrappedFileKey = append([]byte(nil), envelope.WrappedFileKey...)
	}
	clone.Signature = append([]byte(nil), m.Signature...)
	return &clone
}

// RecomputeIntegrityFields refreshes the metadata digest and Merkle root from
// the current metadata and chunk digests. It does not sign the manifest.
func (m *Manifest) RecomputeIntegrityFields() {
	if m == nil {
		return
	}
	m.MetadataDigest = HashMetadata(m.Metadata)
	m.MerkleRoot = ComputeMerkleRoot(m.ChunkSHA256)
}

// SigningPayload returns the canonical bytes signed by the sender device.
func (m *Manifest) SigningPayload() []byte {
	if m == nil {
		return nil
	}
	payload := append([]byte(nil), manifestSignatureDomain...)
	return appendManifestFields(payload, m)
}

// Sign validates the manifest structure and sets its Ed25519 signature.
func (m *Manifest) Sign(privateKey ed25519.PrivateKey) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: bad Ed25519 private key", ErrInvalidManifest)
	}
	if err := m.validateStructure(false); err != nil {
		return err
	}
	m.Signature = ed25519.Sign(privateKey, m.SigningPayload())
	return nil
}

// VerifySignature checks the sender's Ed25519 signature over the manifest.
func (m *Manifest) VerifySignature(publicKey ed25519.PublicKey) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	if len(publicKey) != ed25519.PublicKeySize || len(m.Signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(publicKey, m.SigningPayload(), m.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// ValidateStructure verifies all non-signature manifest invariants, including
// canonical chunk layout, unique recipient identifiers, metadata digest, and
// Merkle root.
func (m *Manifest) ValidateStructure() error {
	return m.validateStructure(true)
}

func (m *Manifest) validateStructure(requireSignature bool) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	if m.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %d", ErrInvalidManifest, m.Version)
	}
	if isZero32(m.FileID) {
		return fmt.Errorf("%w: zero file ID", ErrInvalidManifest)
	}
	if m.ChunkSize == 0 {
		return fmt.Errorf("%w: zero chunk size", ErrInvalidManifest)
	}
	if m.ChunkSize > MaxChunkSize {
		return fmt.Errorf("%w: chunk size exceeds package limit", ErrInvalidManifest)
	}
	if m.TotalChunks > MaxManifestChunks {
		return fmt.Errorf("%w: too many chunks", ErrInvalidManifest)
	}
	expectedChunks, ok := canonicalChunkCount(m.TotalLength, m.ChunkSize)
	if !ok || expectedChunks != m.TotalChunks {
		return fmt.Errorf("%w: non-canonical total length/chunk layout", ErrInvalidManifest)
	}
	if uint64(len(m.ChunkSHA256)) != m.TotalChunks {
		return fmt.Errorf("%w: chunk digest count does not match total chunks", ErrInvalidManifest)
	}
	if len(m.Metadata) > MaxMetadataSize {
		return fmt.Errorf("%w: metadata too large", ErrInvalidManifest)
	}
	if m.MetadataDigest != HashMetadata(m.Metadata) {
		return fmt.Errorf("%w: metadata digest mismatch", ErrMetadataMismatch)
	}
	if m.MerkleRoot != ComputeMerkleRoot(m.ChunkSHA256) {
		return fmt.Errorf("%w: Merkle root mismatch", ErrInvalidManifest)
	}
	if m.SenderDeviceID == "" || len(m.SenderDeviceID) > MaxDeviceIDLength {
		return fmt.Errorf("%w: invalid sender device ID", ErrInvalidManifest)
	}
	if len(m.SessionID) > MaxDeviceIDLength || len(m.MessageID) > MaxDeviceIDLength {
		return fmt.Errorf("%w: oversized session or message ID", ErrInvalidManifest)
	}
	if len(m.Envelopes) == 0 || len(m.Envelopes) > MaxManifestEnvelopes {
		return fmt.Errorf("%w: invalid recipient envelope count", ErrInvalidManifest)
	}
	seenRecipients := make(map[string]struct{}, len(m.Envelopes))
	for i := range m.Envelopes {
		envelope := &m.Envelopes[i]
		if envelope.RecipientDeviceID == "" || len(envelope.RecipientDeviceID) > MaxDeviceIDLength {
			return fmt.Errorf("%w: invalid recipient device ID", ErrInvalidManifest)
		}
		if _, exists := seenRecipients[envelope.RecipientDeviceID]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateRecipient, envelope.RecipientDeviceID)
		}
		seenRecipients[envelope.RecipientDeviceID] = struct{}{}
		if isZero32(envelope.EphemeralPublicKey) {
			return fmt.Errorf("%w: zero ephemeral public key", ErrInvalidManifest)
		}
		if len(envelope.WrappedFileKey) != FileKeySize+GCMTagSize {
			return fmt.Errorf("%w: invalid wrapped file key length", ErrInvalidManifest)
		}
	}
	if requireSignature && len(m.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: invalid signature length", ErrInvalidManifest)
	}
	return nil
}

// MarshalBinary returns the canonical transport encoding, including signature.
func (m *Manifest) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	encoded := append([]byte(nil), manifestBinaryMagic...)
	encoded = appendManifestFields(encoded, m)
	if len(m.Signature) > 0xffff {
		return nil, fmt.Errorf("%w: signature too large", ErrInvalidManifest)
	}
	encoded = binary.BigEndian.AppendUint16(encoded, uint16(len(m.Signature)))
	encoded = append(encoded, m.Signature...)
	return encoded, nil
}

// UnmarshalBinary decodes a canonical manifest. Signature trust is not
// established by decoding; callers must still resolve the sender key and call
// VerifySignature before opening chunks.
func (m *Manifest) UnmarshalBinary(data []byte) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	if !bytes.HasPrefix(data, manifestBinaryMagic) {
		return fmt.Errorf("%w: bad magic", ErrInvalidManifest)
	}
	reader := binaryReader{data: data[len(manifestBinaryMagic):]}
	var decoded Manifest
	if err := readManifestFields(&reader, &decoded); err != nil {
		return err
	}
	signatureLength, err := reader.uint16()
	if err != nil {
		return err
	}
	decoded.Signature = append([]byte(nil), reader.take(int(signatureLength))...)
	if len(decoded.Signature) != int(signatureLength) || reader.remaining() != 0 {
		return fmt.Errorf("%w: truncated signature or trailing data", ErrInvalidManifest)
	}
	*m = decoded
	return nil
}

func appendManifestFields(dst []byte, m *Manifest) []byte {
	dst = binary.BigEndian.AppendUint16(dst, m.Version)
	dst = append(dst, m.FileID[:]...)
	dst = binary.BigEndian.AppendUint64(dst, m.ChunkSize)
	dst = binary.BigEndian.AppendUint64(dst, m.TotalLength)
	dst = binary.BigEndian.AppendUint64(dst, m.TotalChunks)
	dst = append(dst, m.MetadataDigest[:]...)
	dst = appendLengthPrefixed(dst, m.Metadata)

	dst = binary.BigEndian.AppendUint64(dst, uint64(len(m.ChunkSHA256)))
	for _, digest := range m.ChunkSHA256 {
		dst = append(dst, digest[:]...)
	}
	dst = append(dst, m.MerkleRoot[:]...)
	dst = appendLengthPrefixed(dst, []byte(m.SenderDeviceID))
	dst = appendLengthPrefixed(dst, []byte(m.SessionID))
	dst = appendLengthPrefixed(dst, []byte(m.MessageID))

	dst = binary.BigEndian.AppendUint32(dst, uint32(len(m.Envelopes)))
	for i := range m.Envelopes {
		envelope := &m.Envelopes[i]
		dst = appendLengthPrefixed(dst, []byte(envelope.RecipientDeviceID))
		dst = append(dst, envelope.EphemeralPublicKey[:]...)
		dst = append(dst, envelope.Nonce[:]...)
		dst = appendLengthPrefixed(dst, envelope.WrappedFileKey)
	}
	return dst
}

func readManifestFields(reader *binaryReader, m *Manifest) error {
	var err error
	if m.Version, err = reader.uint16(); err != nil {
		return err
	}
	if m.FileID, err = reader.hash32(); err != nil {
		return err
	}
	if m.ChunkSize, err = reader.uint64(); err != nil {
		return err
	}
	if m.TotalLength, err = reader.uint64(); err != nil {
		return err
	}
	if m.TotalChunks, err = reader.uint64(); err != nil {
		return err
	}
	if m.MetadataDigest, err = reader.hash32(); err != nil {
		return err
	}
	if m.Metadata, err = reader.lengthPrefixed(MaxMetadataSize); err != nil {
		return err
	}

	chunkCount, err := reader.uint64()
	if err != nil {
		return err
	}
	if chunkCount > MaxManifestChunks {
		return fmt.Errorf("%w: too many chunk digests", ErrInvalidManifest)
	}
	if chunkCount > uint64(reader.remaining()/32) {
		return fmt.Errorf("%w: truncated chunk digests", ErrInvalidManifest)
	}
	m.ChunkSHA256 = make([][32]byte, int(chunkCount))
	for i := range m.ChunkSHA256 {
		if m.ChunkSHA256[i], err = reader.hash32(); err != nil {
			return err
		}
	}
	if m.MerkleRoot, err = reader.hash32(); err != nil {
		return err
	}
	if m.SenderDeviceID, err = reader.stringValue(MaxDeviceIDLength); err != nil {
		return err
	}
	if m.SessionID, err = reader.stringValue(MaxDeviceIDLength); err != nil {
		return err
	}
	if m.MessageID, err = reader.stringValue(MaxDeviceIDLength); err != nil {
		return err
	}

	envelopeCount, err := reader.uint32()
	if err != nil {
		return err
	}
	if envelopeCount > MaxManifestEnvelopes {
		return fmt.Errorf("%w: too many recipient envelopes", ErrInvalidManifest)
	}
	if envelopeCount > uint32(reader.remaining()/minimumSerializedEnvelopeSize) {
		return fmt.Errorf("%w: truncated recipient envelopes", ErrInvalidManifest)
	}
	m.Envelopes = make([]RecipientEnvelope, int(envelopeCount))
	for i := range m.Envelopes {
		envelope := &m.Envelopes[i]
		if envelope.RecipientDeviceID, err = reader.stringValue(MaxDeviceIDLength); err != nil {
			return err
		}
		if envelope.EphemeralPublicKey, err = reader.hash32(); err != nil {
			return err
		}
		if envelope.Nonce, err = reader.nonce(); err != nil {
			return err
		}
		if envelope.WrappedFileKey, err = reader.lengthPrefixed(FileKeySize + GCMTagSize); err != nil {
			return err
		}
	}
	return nil
}

const minimumSerializedEnvelopeSize = 4 + 4 + 32 + ChunkNonceSize + 4

func appendLengthPrefixed(dst, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func canonicalChunkCount(totalLength, chunkSize uint64) (uint64, bool) {
	if chunkSize == 0 {
		return 0, false
	}
	if totalLength == 0 {
		return 0, true
	}
	chunks := 1 + (totalLength-1)/chunkSize
	if chunks > MaxManifestChunks {
		return 0, false
	}
	return chunks, true
}

func expectedPlaintextLength(m *Manifest, index uint64) (uint64, bool) {
	if index >= m.TotalChunks {
		return 0, false
	}
	if index+1 < m.TotalChunks {
		return m.ChunkSize, true
	}
	length := m.TotalLength - (m.TotalChunks-1)*m.ChunkSize
	if length == 0 || length > m.ChunkSize {
		return 0, false
	}
	return length, true
}

func isZero32(value [32]byte) bool {
	var zero [32]byte
	return value == zero
}

type binaryReader struct {
	data []byte
	off  int
}

func (r *binaryReader) remaining() int {
	return len(r.data) - r.off
}

func (r *binaryReader) take(length int) []byte {
	if length < 0 || length > r.remaining() {
		r.off = len(r.data)
		return nil
	}
	value := r.data[r.off : r.off+length]
	r.off += length
	return value
}

func (r *binaryReader) uint16() (uint16, error) {
	value := r.take(2)
	if len(value) != 2 {
		return 0, fmt.Errorf("%w: truncated uint16", ErrInvalidManifest)
	}
	return binary.BigEndian.Uint16(value), nil
}

func (r *binaryReader) uint32() (uint32, error) {
	value := r.take(4)
	if len(value) != 4 {
		return 0, fmt.Errorf("%w: truncated uint32", ErrInvalidManifest)
	}
	return binary.BigEndian.Uint32(value), nil
}

func (r *binaryReader) uint64() (uint64, error) {
	value := r.take(8)
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: truncated uint64", ErrInvalidManifest)
	}
	return binary.BigEndian.Uint64(value), nil
}

func (r *binaryReader) hash32() ([32]byte, error) {
	var value [32]byte
	bytesValue := r.take(32)
	if len(bytesValue) != 32 {
		return value, fmt.Errorf("%w: truncated 32-byte field", ErrInvalidManifest)
	}
	copy(value[:], bytesValue)
	return value, nil
}

func (r *binaryReader) nonce() ([ChunkNonceSize]byte, error) {
	var value [ChunkNonceSize]byte
	bytesValue := r.take(ChunkNonceSize)
	if len(bytesValue) != ChunkNonceSize {
		return value, fmt.Errorf("%w: truncated nonce", ErrInvalidManifest)
	}
	copy(value[:], bytesValue)
	return value, nil
}

func (r *binaryReader) lengthPrefixed(max int) ([]byte, error) {
	length, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if uint64(length) > uint64(max) {
		return nil, fmt.Errorf("%w: length-prefixed field too large", ErrInvalidManifest)
	}
	value := r.take(int(length))
	if len(value) != int(length) {
		return nil, fmt.Errorf("%w: truncated length-prefixed field", ErrInvalidManifest)
	}
	return append([]byte(nil), value...), nil
}

func (r *binaryReader) stringValue(max int) (string, error) {
	value, err := r.lengthPrefixed(max)
	if err != nil {
		return "", err
	}
	return string(value), nil
}
