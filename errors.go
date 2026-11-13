package netsec

import "errors"

var (
	// ErrInvalidManifest reports malformed manifest data or a policy field that
	// cannot be represented by the canonical encoding.
	ErrInvalidManifest = errors.New("netsec: invalid manifest")

	// ErrInvalidRootTrust reports an invalid host root configuration.
	ErrInvalidRootTrust = errors.New("netsec: invalid root trust configuration")

	// ErrInvalidHostConfig reports an invalid host policy configuration.
	ErrInvalidHostConfig = errors.New("netsec: invalid host configuration")

	// ErrRootSignatureConflict reports different signatures carrying the same
	// root ID.
	ErrRootSignatureConflict = errors.New("netsec: conflicting signatures for root ID")

	// ErrRootThreshold reports that too few distinct trusted roots signed the
	// manifest.
	ErrRootThreshold = errors.New("netsec: root signature threshold not met")

	// ErrInvalidFirmware reports malformed firmware attestation data.
	ErrInvalidFirmware = errors.New("netsec: invalid firmware attestation")

	// ErrFirmwareSignatureConflict reports different signatures for the same
	// firmware key ID.
	ErrFirmwareSignatureConflict = errors.New("netsec: conflicting firmware signatures for key ID")

	// ErrFirmwareThreshold reports that the attestation did not reach the
	// manifest's firmware signature threshold.
	ErrFirmwareThreshold = errors.New("netsec: firmware signature threshold not met")

	// ErrFirmwareThresholdPolicy reports a manifest threshold below the host's
	// minimum.
	ErrFirmwareThresholdPolicy = errors.New("netsec: firmware threshold below host policy")

	// ErrDeviceModel reports a manifest or attestation for a different device
	// model than the host.
	ErrDeviceModel = errors.New("netsec: device model mismatch")

	// ErrManifestExpired reports a manifest used outside its validity window.
	ErrManifestExpired = errors.New("netsec: manifest outside validity window")

	// ErrGenerationRollback reports an attempt to replace accepted state with a
	// lower generation.
	ErrGenerationRollback = errors.New("netsec: manifest generation rollback")

	// ErrGenerationFork reports a different manifest body at an already
	// accepted generation.
	ErrGenerationFork = errors.New("netsec: conflicting manifest at accepted generation")

	// ErrRevocationRollback reports a manifest that omits previously revoked
	// key IDs.
	ErrRevocationRollback = errors.New("netsec: revocation set rollback")

	// ErrKeyMaterialChanged reports public key material replacement for a
	// firmware key ID that is still valid in the prior manifest.
	ErrKeyMaterialChanged = errors.New("netsec: firmware key material changed for active key ID")

	// ErrNoAcceptedManifest reports a boot attempt before any manifest has been
	// accepted.
	ErrNoAcceptedManifest = errors.New("netsec: no accepted manifest")

	// ErrStore reports a trusted-store failure.
	ErrStore = errors.New("netsec: trusted store failure")

	// ErrInvalidSigningKey reports an unusable Ed25519 private key.
	ErrInvalidSigningKey = errors.New("netsec: invalid Ed25519 private key")
)
