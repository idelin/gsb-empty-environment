package netsec

import "errors"

var (
	// ErrInvalidPath reports a path that is not valid UTF-8, contains a
	// backslash or NUL, or does not identify a file after normalization.
	ErrInvalidPath = errors.New("netsec: invalid path")

	// ErrPathEscapesRoot reports a path whose ".." segments walk above the
	// package root.
	ErrPathEscapesRoot = errors.New("netsec: path escapes package root")

	// ErrInvalidManifest reports malformed manifest encoding.
	ErrInvalidManifest = errors.New("netsec: invalid manifest")

	// ErrDuplicateManifestPath reports a repeated path in the signed manifest.
	ErrDuplicateManifestPath = errors.New("netsec: duplicate manifest path")

	// ErrManifestOrder reports manifest records that are not in path byte order.
	ErrManifestOrder = errors.New("netsec: manifest paths are not sorted")

	// ErrInvalidSignature reports an invalid key, signature size, or signature.
	ErrInvalidSignature = errors.New("netsec: invalid manifest signature")

	// ErrDuplicateEntry reports multiple package entries that canonicalize to
	// the same install path.
	ErrDuplicateEntry = errors.New("netsec: duplicate package entry")

	// ErrPathConflict reports a file path that is also an ancestor directory of
	// another file path.
	ErrPathConflict = errors.New("netsec: conflicting file and directory paths")

	// ErrExtraEntry reports an entry that is absent from the signed manifest.
	ErrExtraEntry = errors.New("netsec: entry is not in signed manifest")

	// ErrMissingEntry reports a signed manifest path for which no entry was
	// supplied.
	ErrMissingEntry = errors.New("netsec: signed manifest entry is missing")

	// ErrDigestMismatch reports a declared or computed SHA-256 mismatch.
	ErrDigestMismatch = errors.New("netsec: SHA-256 mismatch")

	// ErrFileTooLarge reports that one entry exceeded MaxFileBytes.
	ErrFileTooLarge = errors.New("netsec: file exceeds size limit")

	// ErrTotalTooLarge reports that the package exceeded MaxTotalBytes.
	ErrTotalTooLarge = errors.New("netsec: package exceeds total size limit")

	// ErrStream reports a failing or truncated entry reader.
	ErrStream = errors.New("netsec: package stream error")

	// ErrInvalidLimits reports negative size limits.
	ErrInvalidLimits = errors.New("netsec: invalid size limits")

	// ErrInstall reports an error returned by the installation callback.
	ErrInstall = errors.New("netsec: installation failed")
)
