package safezip

import "errors"

// Sentinel errors returned by the package. Callers can use errors.Is to
// classify failures without parsing message text.
var (
	// ErrFormat is returned for structurally invalid zip metadata.
	ErrFormat = errors.New("safezip: malformed zip structure")
	// ErrTruncated is returned when declared metadata or payload runs past
	// the end of the underlying object.
	ErrTruncated = errors.New("safezip: archive is truncated")
	// ErrBinding is returned when a central directory entry cannot be bound
	// to a local file header carrying the same name, method and range.
	ErrBinding = errors.New("safezip: central directory/local header binding mismatch")
	// ErrOverlap is returned when two claimed regions (payload or local
	// header) overlap each other or the central directory.
	ErrOverlap = errors.New("safezip: overlapping member regions")
	// ErrPseudoDirectory is returned when an entry marked as a directory
	// illegally declares a payload.
	ErrPseudoDirectory = errors.New("safezip: directory entry carries a payload")
	// ErrDeclaredTooLong is returned when a declared length exceeds the
	// archive bounds or the configured Limits.
	ErrDeclaredTooLong = errors.New("safezip: declared length exceeds archive or limit")
	// ErrCRC is returned when the decompressed member does not match the
	// CRC-32 recorded in the central directory.
	ErrCRC = errors.New("safezip: crc32 verification failed")
	// ErrIncomplete is returned when a MemberReader is closed before the
	// full member has been consumed.
	ErrIncomplete = errors.New("safezip: member read was not completed")
	// ErrUnsupported is returned for zip features this package refuses,
	// such as spanned archives or unknown compression methods.
	ErrUnsupported = errors.New("safezip: unsupported zip feature")
)
