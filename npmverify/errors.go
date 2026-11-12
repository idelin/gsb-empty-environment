package npmverify

import "errors"

// Sentinel errors returned by the verifier. Callers can use errors.Is to
// distinguish the individual trust checks that failed.
var (
	// ErrNoPackument is returned when the registry document is missing.
	ErrNoPackument = errors.New("npmverify: missing packument")

	// ErrNoVerifier is returned when no signature callback was supplied.
	ErrNoVerifier = errors.New("npmverify: signature verifier callback is required")

	// ErrTagNotFound is returned when a dist-tag does not resolve to a
	// version present in the packument's versions map.
	ErrTagNotFound = errors.New("npmverify: dist-tag cannot be resolved to a published version")

	// ErrMalformedIntegrity is returned when an SRI string cannot be parsed.
	ErrMalformedIntegrity = errors.New("npmverify: malformed integrity digest")

	// ErrIntegrityMismatch is returned when the tarball hash does not match
	// the strongest digest declared in the version metadata.
	ErrIntegrityMismatch = errors.New("npmverify: tarball integrity does not match metadata")

	// ErrSignatureFailed is returned when no dist.signatures entry is
	// accepted by the signature verifier callback.
	ErrSignatureFailed = errors.New("npmverify: registry signature verification failed")

	// ErrNameMismatch is returned when the tarball's package.json name does
	// not match the resolved metadata.
	ErrNameMismatch = errors.New("npmverify: package name mismatch")

	// ErrVersionMismatch is returned when the tarball's package.json version
	// does not match the resolved metadata.
	ErrVersionMismatch = errors.New("npmverify: package version mismatch")

	// ErrMalformedTarball is returned when the tarball cannot be read or does
	// not contain a parseable package.json.
	ErrMalformedTarball = errors.New("npmverify: malformed tarball")
)
