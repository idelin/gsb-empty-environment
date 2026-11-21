package manifest

import "errors"

// Sentinel errors returned by the package. Callers can use errors.Is to
// distinguish admission decisions while wrapped messages carry context.
var (
	// ErrNoState is returned by a Store when no manifest has been accepted yet.
	ErrNoState = errors.New("manifest: no trusted state persisted")
	// ErrCorruptState indicates persisted state failed integrity checks.
	ErrCorruptState = errors.New("manifest: corrupt persisted state")
	// ErrInvalidManifest means the manifest itself is malformed.
	ErrInvalidManifest = errors.New("manifest: invalid manifest")
	// ErrModelMismatch means the manifest/firmware targets another device model.
	ErrModelMismatch = errors.New("manifest: device model mismatch")
	// ErrThresholdTooLow means the firmware threshold is below the host floor.
	ErrThresholdTooLow = errors.New("manifest: firmware threshold below host minimum")
	// ErrOutsideValidityWindow means the admission time is not in [NotBefore, NotAfter).
	ErrOutsideValidityWindow = errors.New("manifest: outside validity window")
	// ErrInvalidRootSignature means a signature from a configured root failed verification.
	ErrInvalidRootSignature = errors.New("manifest: invalid root signature")
	// ErrConflictingRootSignatures means one root ID carries two different signatures.
	ErrConflictingRootSignatures = errors.New("manifest: conflicting signatures from same root")
	// ErrRootThresholdNotMet means distinct valid root signatures are below the root quorum.
	ErrRootThresholdNotMet = errors.New("manifest: root signature threshold not met")
	// ErrStaleGeneration means the submitted generation is lower than the accepted one.
	ErrStaleGeneration = errors.New("manifest: generation older than accepted generation")
	// ErrSameGenerationFork means same generation but a different manifest digest.
	ErrSameGenerationFork = errors.New("manifest: same generation with different digest")
	// ErrRevocationRollback means the new manifest drops a previously revoked keyID.
	ErrRevocationRollback = errors.New("manifest: revocation set is not a superset of the previous one")
	// ErrKeyMaterialChanged means a previously valid keyID was rebound to new public key material.
	ErrKeyMaterialChanged = errors.New("manifest: public key material changed for still-valid keyID")
	// ErrNoTrustedManifest means firmware admission was attempted before any manifest was accepted.
	ErrNoTrustedManifest = errors.New("manifest: no trusted manifest available")
	// ErrFirmwareGenerationMismatch means the firmware was signed for a different manifest generation.
	ErrFirmwareGenerationMismatch = errors.New("manifest: firmware signed for different manifest generation")
	// ErrInvalidFirmwareBundle means the firmware bundle is malformed.
	ErrInvalidFirmwareBundle = errors.New("manifest: invalid firmware bundle")
	// ErrFirmwareThresholdNotMet means distinct eligible firmware signatures are below t.
	ErrFirmwareThresholdNotMet = errors.New("manifest: firmware signature threshold not met")
)
