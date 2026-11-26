package safeunpack

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformed identifies a structurally invalid or truncated archive.
	ErrMalformed = errors.New("safeunpack: malformed archive")
	// ErrChecksum identifies an invalid TAR header checksum.
	ErrChecksum = fmt.Errorf("%w: checksum mismatch", ErrMalformed)
	// ErrUnsafePath identifies an absolute, NUL-containing, drive-prefixed, or
	// otherwise non-portable path.
	ErrUnsafePath = errors.New("safeunpack: unsafe path")
	// ErrPathTraversal identifies an entry path that traverses above the root.
	ErrPathTraversal = fmt.Errorf("%w: path traversal", ErrUnsafePath)
	// ErrSymlinkTraversal identifies a path whose parent traversal crosses an
	// already-created symbolic link.
	ErrSymlinkTraversal = fmt.Errorf("%w: path crosses symbolic link", ErrPathTraversal)
	// ErrLinkEscape identifies a link target that escapes the virtual root.
	ErrLinkEscape = fmt.Errorf("%w: link escapes root", ErrUnsafePath)
	// ErrHardlink identifies a hard link with a missing, unsafe, directory, or
	// symbolic-link target.
	ErrHardlink = fmt.Errorf("%w: invalid hardlink", ErrLinkEscape)
	// ErrDuplicatePath identifies an overwrite or a case-insensitive collision.
	ErrDuplicatePath = errors.New("safeunpack: duplicate path")
	// ErrLimitExceeded identifies an entry, file, or total byte quota breach.
	ErrLimitExceeded = errors.New("safeunpack: resource limit exceeded")
	// ErrCompressionBomb identifies an excessive compression ratio.
	ErrCompressionBomb = fmt.Errorf("%w: compression ratio exceeded", ErrLimitExceeded)
	// ErrUnsupported identifies an archive feature that is not supported.
	ErrUnsupported = errors.New("safeunpack: unsupported archive feature")
	// ErrZIPConsistency identifies inconsistent ZIP central/local metadata.
	ErrZIPConsistency = fmt.Errorf("%w: inconsistent zip metadata", ErrMalformed)
)

// Common aliases used by callers that prefer different terminology.
var (
	ErrInvalidArchive   = ErrMalformed
	ErrQuotaExceeded    = ErrLimitExceeded
	ErrCompressionRatio = ErrCompressionBomb
	ErrDuplicateEntry   = ErrDuplicatePath
	ErrUnsafeLink       = ErrLinkEscape
)
