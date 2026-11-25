// Package manifest implements boot-admission policy for a firmware key manifest.
//
// A manifest is signed by a quorum of pinned root keys, carries a monotonically
// increasing generation, and enumerates the firmware signing keys that are
// permitted for a specific device model and generation. The package provides:
//
//   - Deterministic canonical encoding (see [CanonicalEncoding]) so that
//     signatures always cover an unambiguous byte sequence.
//   - Root-quorum verification with per-root-ID de-duplication.
//   - A [Store] abstraction with an atomic file implementation ([FileStore])
//     that never exposes a new generation paired with old manifest content.
//   - Firmware admission ([Host.BootCheck]) that counts distinct, unrevoked,
//     in-window firmware keys and collapses key-ID aliases sharing one public
//     key into a single vote.
package manifest

import (
	"errors"
	"time"
)

// Sentinel errors returned by the package. Callers can use errors.Is to match
// the failure class while wrapped errors carry the offending identifier.
var (
	ErrInvalidManifest = errors.New("manifest: invalid manifest structure")
	ErrModelMismatch   = errors.New("manifest: device model mismatch")

	ErrRootSignature = errors.New("manifest: root signature verification failed")
	ErrRootConflict  = errors.New("manifest: conflicting signatures from same root ID")
	ErrUnknownRoot   = errors.New("manifest: signature from unknown root (not counted)")
	ErrRootQuorum    = errors.New("manifest: root signature threshold not met")
	ErrDuplicateRoot = errors.New("manifest: duplicate root signature")

	ErrOutsideValidity    = errors.New("manifest: not within validity window")
	ErrReplayedManifest   = errors.New("manifest: generation lower than accepted generation")
	ErrForkedManifest     = errors.New("manifest: same generation with different digest")
	ErrRevocationRollback = errors.New("manifest: revocation set dropped previously revoked key ID")
	ErrKeyMaterialChanged = errors.New("manifest: public key material changed for retained key ID")
	ErrThresholdBelowMin  = errors.New("manifest: firmware threshold below host minimum")

	ErrFirmwareSignature = errors.New("manifest: firmware signature verification failed")
	ErrFirmwareQuorum    = errors.New("manifest: firmware signature threshold not met")

	ErrNoState        = errors.New("manifest: no accepted manifest in store")
	ErrDigestMismatch = errors.New("manifest: stored digest does not match stored manifest")

	ErrInvalidConfig = errors.New("manifest: invalid host configuration")
)

// PurposeFirmware is the key-purpose flag that authorizes a key entry to sign
// firmware images. Entries may carry additional purposes; firmware admission
// requires this flag to be present.
const PurposeFirmware = "firmware"

// Root is one entry in the host-pinned root trust set.
type Root struct {
	ID     string
	PubKey []byte // Ed25519 public key
}

// KeyEntry is one firmware signing key in a manifest.
type KeyEntry struct {
	KeyID  string
	PubKey []byte // Ed25519 public key
	// Purposes lists the authorized uses; it must contain PurposeFirmware for
	// the entry to count toward firmware admission.
	Purposes []string
	// GenStart <= manifest generation < GenStop is the entry's active window.
	GenStart uint64
	GenStop  uint64
}

// Manifest is the canonical, signable manifest body. All collection fields
// are canonicalized (sorted) during encoding, so callers may supply them in
// any order.
type Manifest struct {
	DeviceModel   string
	Generation    uint64
	NotBefore     time.Time
	NotAfter      time.Time
	Threshold     uint64 // firmware signature threshold t
	Entries       []KeyEntry
	RevokedKeyIDs []string
}

// RootSignature binds one root key ID to an Ed25519 signature over the
// manifest's canonical encoding.
type RootSignature struct {
	RootID    string
	Signature []byte
}

// SignedManifest is a manifest body together with its root attestations.
type SignedManifest struct {
	Manifest   *Manifest
	Signatures []RootSignature
}

// FirmwareSignature is one firmware-image signature produced by a key listed
// in the accepted manifest.
type FirmwareSignature struct {
	KeyID     string
	Signature []byte
}

// FirmwareImage is the firmware payload submitted for boot admission.
type FirmwareImage struct {
	DeviceModel string
	ImageDigest []byte
	Version     string
	// Generation must equal the generation of the manifest under which the
	// image is admitted.
	Generation uint64
	Signatures []FirmwareSignature
}

// State is the durable trusted state: the highest accepted generation, the
// manifest accepted at that generation and its digest.
type State struct {
	Generation     uint64
	Digest         []byte
	SignedManifest *SignedManifest
}
