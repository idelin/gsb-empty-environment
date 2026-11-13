package netsec

import "errors"

var (
	// ErrMalformedPublicKey means a configured signify public key is invalid.
	ErrMalformedPublicKey = errors.New("netsec: malformed signify public key")
	// ErrMalformedSignature means the signed manifest envelope is invalid.
	ErrMalformedSignature = errors.New("netsec: malformed signify signature")
	// ErrKeyNumMismatch means the signature was made by a different key.
	ErrKeyNumMismatch = errors.New("netsec: signify key number mismatch")
	// ErrInvalidSignature means the Ed25519 signature did not verify.
	ErrInvalidSignature = errors.New("netsec: invalid signify signature")
	// ErrMalformedManifest means the authenticated manifest body is invalid.
	ErrMalformedManifest = errors.New("netsec: malformed checksum manifest")
	// ErrUnsafeFileName means a manifest or download name is not a safe basename.
	ErrUnsafeFileName = errors.New("netsec: unsafe file name")
	// ErrConflictingEntry means one name has more than one digest.
	ErrConflictingEntry = errors.New("netsec: conflicting manifest entry")
	// ErrFileNotListed means the download name is absent from the manifest.
	ErrFileNotListed = errors.New("netsec: downloaded file is not listed")
	// ErrChecksumMismatch means the downloaded bytes do not match the manifest.
	ErrChecksumMismatch = errors.New("netsec: downloaded file checksum mismatch")
)
