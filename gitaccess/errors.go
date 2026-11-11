package gitaccess

import "errors"

// Sentinel errors returned by the package. Callers should use errors.Is to
// match them because the package wraps them with contextual detail.
var (
	// ErrObjectNotFound means the database contains no object with the
	// requested identifier.
	ErrObjectNotFound = errors.New("gitaccess: object not found")

	// ErrRefNotFound means the requested reference does not exist at the
	// time a capability is issued.
	ErrRefNotFound = errors.New("gitaccess: reference not found")

	// ErrUnauthorized means the object is not reachable from the fixed
	// reference snapshot bound to the presented capability.
	ErrUnauthorized = errors.New("gitaccess: object not authorized by capability")

	// ErrChecksumMismatch means the SHA-1 content digest of stored or
	// delta-reconstructed content does not match its claimed object id.
	ErrChecksumMismatch = errors.New("gitaccess: content digest mismatch")

	// ErrBadPack means a packfile is structurally malformed (bad magic,
	// trailer checksum, entry header, delta opcode, and similar faults).
	ErrBadPack = errors.New("gitaccess: malformed packfile")

	// ErrDeltaBaseMissing means a REF_DELTA or OFS_DELTA entry cannot
	// resolve its declared base object.
	ErrDeltaBaseMissing = errors.New("gitaccess: delta base object missing")

	// ErrDeltaChainTooDeep means delta resolution exceeded the maximum
	// allowed chain depth.
	ErrDeltaChainTooDeep = errors.New("gitaccess: delta chain too deep")
)
