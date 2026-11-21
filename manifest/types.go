// Package manifest implements verifiable firmware-key-manifest admission for a
// Go boot-rom host.
//
// The host pins a set of root Ed25519 keys. A manifest is accepted only when:
//
//   - its canonical encoding (see EncodeManifest) is well formed and within
//     its validity window,
//   - root signatures over the canonical encoding verify against distinct
//     root IDs and reach the host's root threshold (unknown roots are ignored;
//     duplicate root IDs with different signatures are a conflict),
//   - its generation is monotonically increasing relative to trusted storage
//     (same generation is accepted only with a byte-identical digest, so a
//     replayed or forked old manifest cannot resurrect revoked keys),
//   - revocations only grow and public key material of still-valid keyIDs
//     never changes,
//   - the firmware signature threshold stays above the host's minimum.
//
// Firmware is admitted only when signatures over the firmware descriptor
// (model, image digest, version, manifest generation) verify against distinct
// eligible firmware keys: not revoked, purpose containing firmware, and the
// accepted generation inside the key's [StartGeneration, EndGeneration)
// window. Alias keyIDs sharing identical public key material count once.
package manifest

import (
	"crypto/ed25519"
	"time"
)

// PurposeFirmware marks a key that may sign bootable firmware.
const PurposeFirmware = "firmware"

// RootKey is a host-pinned root trust anchor. IDs must be unique within a
// host's root set.
type RootKey struct {
	ID     string
	Public ed25519.PublicKey
}

// FirmwareKey is one entry in a manifest's firmware key list. The generation
// window is left closed and right open: [StartGeneration, EndGeneration).
type FirmwareKey struct {
	KeyID           string
	Public          ed25519.PublicKey
	Purposes        []string
	StartGeneration uint64
	EndGeneration   uint64
}

// ValidAt reports whether the key's window covers generation g.
func (k FirmwareKey) ValidAt(g uint64) bool {
	return g >= k.StartGeneration && g < k.EndGeneration
}

// HasPurpose reports whether purpose p is listed for the key.
func (k FirmwareKey) HasPurpose(p string) bool {
	for _, x := range k.Purposes {
		if x == p {
			return true
		}
	}
	return false
}

// Manifest is the signed firmware-key manifest. TimeWindow is left closed and
// right open: [NotBefore, NotAfter).
type Manifest struct {
	DeviceModel       string
	Generation        uint64
	NotBefore         time.Time
	NotAfter          time.Time
	FirmwareThreshold uint64
	Keys              []FirmwareKey
	RevokedKeyIDs     []string
}

// RootSignature is one root signature over the manifest's canonical encoding.
type RootSignature struct {
	RootID    string
	Signature []byte
}

// SignedManifest pairs a manifest with root signatures.
type SignedManifest struct {
	Manifest   Manifest
	Signatures []RootSignature
}

// FirmwareBundle is an image together with the signatures authorizing it.
type FirmwareBundle struct {
	DeviceModel        string
	Image              []byte
	ImageDigest        []byte
	Version            string
	ManifestGeneration uint64
	Signatures         []FirmwareSignature
}

// FirmwareSignature binds a firmware keyID to a signature over the firmware
// descriptor (see EncodeFirmwareSigningInput).
type FirmwareSignature struct {
	KeyID     string
	Signature []byte
}

// State is the trusted record persisted atomically after each admission.
type State struct {
	// Generation is the highest accepted manifest generation.
	Generation uint64
	// Digest is the digest of the canonical encoding of the accepted manifest.
	Digest []byte
	// AcceptedAt records when the state was committed.
	AcceptedAt time.Time
	// Manifest is the full accepted manifest, stored beside the generation so
	// crash recovery can never expose a new generation paired with old content.
	Manifest Manifest
}

// Store persists the single trusted manifest state.
type Store interface {
	// Load returns ErrNoState when nothing has been accepted yet.
	Load() (*State, error)
	// Save must be all-or-nothing: a crash during Save either leaves the
	// previous state fully readable or installs state fully.
	Save(state *State) error
}
