package netsec

import "errors"

var (
	// ErrPathAlias reports a path that is not in canonical relative form
	// (absolute, contains ".", "..", duplicated or trailing separators, or
	// backslashes), even when it would resolve to a listed object.
	ErrPathAlias = errors.New("netsec: non-canonical path alias")

	// ErrNotInManifest reports a read request for an object the manifest
	// does not list, including objects of inactive optional dependencies.
	ErrNotInManifest = errors.New("netsec: object not in manifest")

	// ErrDigestMismatch reports an on-disk object whose SHA-256 digest
	// differs from the manifest entry.
	ErrDigestMismatch = errors.New("netsec: digest mismatch")

	// ErrModeMismatch reports an on-disk object whose permission or
	// setuid/setgid/sticky bits differ from the manifest entry.
	ErrModeMismatch = errors.New("netsec: mode mismatch")

	// ErrObjectMissing reports a manifest object absent from disk.
	ErrObjectMissing = errors.New("netsec: object missing")

	// ErrObjectUnreadable reports an object that cannot be opened or read.
	ErrObjectUnreadable = errors.New("netsec: object unreadable")

	// ErrNotRegular reports an object that is not a regular file, such as
	// a symlink, directory, or device.
	ErrNotRegular = errors.New("netsec: object is not a regular file")
)
