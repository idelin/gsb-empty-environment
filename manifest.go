package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
	"unicode/utf8"
)

const (
	// UsageFirmware identifies a key entry that may sign firmware statements.
	UsageFirmware = "firmware"

	manifestDomain = "netsec/firmware-manifest/v1"

	maxManifestBytes  = 1 << 20
	maxStringBytes    = 4096
	maxKeyEntries     = 4096
	maxUsages         = 64
	maxRevokedKeys    = 65536
	maxRootSignatures = 4096
)

// Root is one fixed root of trust in the boot ROM.
type Root struct {
	ID        string
	PublicKey ed25519.PublicKey
}

// RootSignature is a signature by a root over the manifest's canonical body.
type RootSignature struct {
	RootID    string
	Signature []byte
}

// FirmwareKey is one firmware signing key entry in a manifest.
//
// The entry is active for StartGeneration <= generation and, when
// EndGeneration is non-zero, generation < EndGeneration. A zero
// EndGeneration means that the entry has no upper generation bound.
type FirmwareKey struct {
	KeyID           string
	PublicKey       ed25519.PublicKey
	Usages          []string
	StartGeneration uint64
	EndGeneration   uint64
}

// HasUsage reports whether the key has the supplied usage.
func (k FirmwareKey) HasUsage(usage string) bool {
	for _, candidate := range k.Usages {
		if candidate == usage {
			return true
		}
	}
	return false
}

// ActiveAt reports whether the key entry covers generation.
func (k FirmwareKey) ActiveAt(generation uint64) bool {
	if generation < k.StartGeneration {
		return false
	}
	return k.EndGeneration == 0 || generation < k.EndGeneration
}

// Manifest is a root-signed firmware key manifest.
//
// RootSignatures is deliberately excluded from CanonicalEncoding and Digest;
// each signature covers the complete canonical body.
type Manifest struct {
	Model             string
	Generation        uint64
	ValidFrom         time.Time
	ValidUntil        time.Time
	FirmwareThreshold int
	Keys              []FirmwareKey
	RevokedKeyIDs     []string
	RootSignatures    []RootSignature
}

// ValidAt reports whether t is in the manifest's left-closed, right-open
// validity window.
func (m Manifest) ValidAt(t time.Time) bool {
	return !t.Before(m.ValidFrom) && t.Before(m.ValidUntil)
}

// Validate checks the manifest body's shape. It does not verify signatures,
// host policy, or whether the manifest is currently within its validity window.
func (m Manifest) Validate() error {
	if err := validateIdentifier("model", m.Model); err != nil {
		return err
	}
	if m.Generation == 0 {
		return fmt.Errorf("%w: generation must be non-zero", ErrInvalidManifest)
	}
	if !m.ValidFrom.Before(m.ValidUntil) {
		return fmt.Errorf("%w: validity window must be non-empty", ErrInvalidManifest)
	}
	if m.FirmwareThreshold < 1 {
		return fmt.Errorf("%w: firmware threshold must be positive", ErrInvalidManifest)
	}
	if len(m.Keys) > maxKeyEntries {
		return fmt.Errorf("%w: too many key entries", ErrInvalidManifest)
	}
	if len(m.RevokedKeyIDs) > maxRevokedKeys {
		return fmt.Errorf("%w: too many revoked key IDs", ErrInvalidManifest)
	}
	if len(m.RootSignatures) > maxRootSignatures {
		return fmt.Errorf("%w: too many root signatures", ErrInvalidManifest)
	}

	keyIDs := make(map[string]struct{}, len(m.Keys))
	for i, key := range m.Keys {
		if err := validateIdentifier("key ID", key.KeyID); err != nil {
			return fmt.Errorf("key entry %d: %w", i, err)
		}
		if _, exists := keyIDs[key.KeyID]; exists {
			return fmt.Errorf("%w: duplicate key ID %q", ErrInvalidManifest, key.KeyID)
		}
		keyIDs[key.KeyID] = struct{}{}
		if len(key.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: key %q has invalid public key length", ErrInvalidManifest, key.KeyID)
		}
		if len(key.Usages) > maxUsages {
			return fmt.Errorf("%w: key %q has too many usages", ErrInvalidManifest, key.KeyID)
		}
		usages := make(map[string]struct{}, len(key.Usages))
		for _, usage := range key.Usages {
			if err := validateIdentifier("usage", usage); err != nil {
				return fmt.Errorf("key %q: %w", key.KeyID, err)
			}
			if _, exists := usages[usage]; exists {
				return fmt.Errorf("%w: key %q has duplicate usage %q", ErrInvalidManifest, key.KeyID, usage)
			}
			usages[usage] = struct{}{}
		}
		if key.EndGeneration != 0 && key.EndGeneration <= key.StartGeneration {
			return fmt.Errorf("%w: key %q has an empty generation window", ErrInvalidManifest, key.KeyID)
		}
	}

	revoked := make(map[string]struct{}, len(m.RevokedKeyIDs))
	for _, keyID := range m.RevokedKeyIDs {
		if err := validateIdentifier("revoked key ID", keyID); err != nil {
			return err
		}
		if _, exists := revoked[keyID]; exists {
			return fmt.Errorf("%w: duplicate revoked key ID %q", ErrInvalidManifest, keyID)
		}
		revoked[keyID] = struct{}{}
	}

	for i, signature := range m.RootSignatures {
		if err := validateIdentifier("root ID", signature.RootID); err != nil {
			return fmt.Errorf("root signature %d: %w", i, err)
		}
	}
	return nil
}

func validateText(field, value string) error {
	if len(value) > maxStringBytes {
		return fmt.Errorf("%w: %s too long", ErrInvalidManifest, field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidManifest, field)
	}
	return nil
}

func validateIdentifier(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", ErrInvalidManifest, field)
	}
	return validateText(field, value)
}

// CanonicalEncoding returns the deterministic manifest body encoding covered
// by root signatures. RootSignatures is not part of this encoding.
//
// Key entries, usages, and revoked key IDs are encoded in lexicographic order.
// All strings use UTF-8 and all integers are big-endian.
func (m Manifest) CanonicalEncoding() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	keys := append([]FirmwareKey(nil), m.Keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyID < keys[j].KeyID })

	revoked := append([]string(nil), m.RevokedKeyIDs...)
	sort.Strings(revoked)

	var buf bytes.Buffer
	writeString(&buf, manifestDomain)
	writeString(&buf, m.Model)
	writeUint64(&buf, m.Generation)
	writeString(&buf, canonicalTime(m.ValidFrom))
	writeString(&buf, canonicalTime(m.ValidUntil))
	writeUint64(&buf, uint64(m.FirmwareThreshold))
	writeUint32(&buf, uint32(len(keys)))
	for _, key := range keys {
		writeString(&buf, key.KeyID)
		writeBytes(&buf, key.PublicKey)

		usages := append([]string(nil), key.Usages...)
		sort.Strings(usages)
		writeUint32(&buf, uint32(len(usages)))
		for _, usage := range usages {
			writeString(&buf, usage)
		}
		writeUint64(&buf, key.StartGeneration)
		writeUint64(&buf, key.EndGeneration)
	}
	writeUint32(&buf, uint32(len(revoked)))
	for _, keyID := range revoked {
		writeString(&buf, keyID)
	}

	if buf.Len() > maxManifestBytes {
		return nil, fmt.Errorf("%w: canonical encoding exceeds %d bytes", ErrInvalidManifest, maxManifestBytes)
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

// Digest returns SHA-256 over CanonicalEncoding.
func (m Manifest) Digest() ([sha256.Size]byte, error) {
	canonical, err := m.CanonicalEncoding()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func canonicalTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func writeUint32(buf *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	buf.Write(encoded[:])
}

func writeUint64(buf *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	buf.Write(encoded[:])
}

func writeBytes(buf *bytes.Buffer, value []byte) {
	writeUint32(buf, uint32(len(value)))
	buf.Write(value)
}

func writeString(buf *bytes.Buffer, value string) {
	writeBytes(buf, []byte(value))
}

type canonicalReader struct {
	data   []byte
	offset int
}

func (r *canonicalReader) readUint32() (uint32, error) {
	if r.offset+4 > len(r.data) {
		return 0, fmt.Errorf("%w: truncated uint32", ErrInvalidManifest)
	}
	value := binary.BigEndian.Uint32(r.data[r.offset : r.offset+4])
	r.offset += 4
	return value, nil
}

func (r *canonicalReader) readUint64() (uint64, error) {
	if r.offset+8 > len(r.data) {
		return 0, fmt.Errorf("%w: truncated uint64", ErrInvalidManifest)
	}
	value := binary.BigEndian.Uint64(r.data[r.offset : r.offset+8])
	r.offset += 8
	return value, nil
}

func (r *canonicalReader) readBytes() ([]byte, error) {
	length, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	if uint64(length) > uint64(len(r.data)-r.offset) {
		return nil, fmt.Errorf("%w: truncated byte string", ErrInvalidManifest)
	}
	value := r.data[r.offset : r.offset+int(length)]
	r.offset += int(length)
	return value, nil
}

func (r *canonicalReader) readString() (string, error) {
	value, err := r.readBytes()
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func decodeManifestBody(data []byte) (Manifest, error) {
	if len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: canonical encoding exceeds %d bytes", ErrInvalidManifest, maxManifestBytes)
	}
	r := canonicalReader{data: data}

	domain, err := r.readString()
	if err != nil {
		return Manifest{}, err
	}
	if domain != manifestDomain {
		return Manifest{}, fmt.Errorf("%w: unexpected encoding domain %q", ErrInvalidManifest, domain)
	}

	var manifest Manifest
	if manifest.Model, err = r.readString(); err != nil {
		return Manifest{}, err
	}
	if manifest.Generation, err = r.readUint64(); err != nil {
		return Manifest{}, err
	}

	from, err := r.readString()
	if err != nil {
		return Manifest{}, err
	}
	if manifest.ValidFrom, err = time.Parse(time.RFC3339Nano, from); err != nil {
		return Manifest{}, fmt.Errorf("%w: invalid valid-from time: %v", ErrInvalidManifest, err)
	}
	until, err := r.readString()
	if err != nil {
		return Manifest{}, err
	}
	if manifest.ValidUntil, err = time.Parse(time.RFC3339Nano, until); err != nil {
		return Manifest{}, fmt.Errorf("%w: invalid valid-until time: %v", ErrInvalidManifest, err)
	}

	threshold, err := r.readUint64()
	if err != nil {
		return Manifest{}, err
	}
	if threshold > math.MaxInt {
		return Manifest{}, fmt.Errorf("%w: firmware threshold overflow", ErrInvalidManifest)
	}
	manifest.FirmwareThreshold = int(threshold)

	keyCount, err := r.readUint32()
	if err != nil {
		return Manifest{}, err
	}
	if keyCount > maxKeyEntries {
		return Manifest{}, fmt.Errorf("%w: too many key entries", ErrInvalidManifest)
	}
	manifest.Keys = make([]FirmwareKey, 0, keyCount)
	for i := uint32(0); i < keyCount; i++ {
		var key FirmwareKey
		if key.KeyID, err = r.readString(); err != nil {
			return Manifest{}, err
		}
		publicKey, err := r.readBytes()
		if err != nil {
			return Manifest{}, err
		}
		key.PublicKey = append(ed25519.PublicKey(nil), publicKey...)

		usageCount, err := r.readUint32()
		if err != nil {
			return Manifest{}, err
		}
		if usageCount > maxUsages {
			return Manifest{}, fmt.Errorf("%w: too many usages", ErrInvalidManifest)
		}
		key.Usages = make([]string, 0, usageCount)
		for j := uint32(0); j < usageCount; j++ {
			usage, err := r.readString()
			if err != nil {
				return Manifest{}, err
			}
			key.Usages = append(key.Usages, usage)
		}
		if key.StartGeneration, err = r.readUint64(); err != nil {
			return Manifest{}, err
		}
		if key.EndGeneration, err = r.readUint64(); err != nil {
			return Manifest{}, err
		}
		manifest.Keys = append(manifest.Keys, key)
	}

	revokedCount, err := r.readUint32()
	if err != nil {
		return Manifest{}, err
	}
	if revokedCount > maxRevokedKeys {
		return Manifest{}, fmt.Errorf("%w: too many revoked key IDs", ErrInvalidManifest)
	}
	manifest.RevokedKeyIDs = make([]string, 0, revokedCount)
	for i := uint32(0); i < revokedCount; i++ {
		keyID, err := r.readString()
		if err != nil {
			return Manifest{}, err
		}
		manifest.RevokedKeyIDs = append(manifest.RevokedKeyIDs, keyID)
	}

	if r.offset != len(r.data) {
		return Manifest{}, fmt.Errorf("%w: trailing data in canonical encoding", ErrInvalidManifest)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}

	// Re-encoding also checks sorted/set canonicalisation and rejects any
	// corruption that happens to parse but does not match the canonical form.
	canonical, err := manifest.CanonicalEncoding()
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(canonical, data) {
		return Manifest{}, fmt.Errorf("%w: manifest encoding is not canonical", ErrInvalidManifest)
	}
	return manifest, nil
}
