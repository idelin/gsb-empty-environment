// Package zipguard performs raw byte-level structural consistency and
// security admission checks on ZIP/JAR/APK archives.
//
// The validator never relies on high-level archive traversal. It parses the
// End of Central Directory record (EOCD), every Central Directory Header
// (CDH), every referenced Local File Header (LFH) and Data Descriptor directly
// from the byte stream, cross-checks every declaration, maps every byte of the
// archive to a referenced structure (rejecting overlaps and hidden gaps),
// validates entry names and Unix file modes, and performs streamed, quota
// limited decompression whose accounting is based on the actual bytes
// produced by the decompressor rather than on header declarations.
//
// Only the Stored (0) and Deflate (8) compression methods are admitted.
package zipguard

import "errors"

// Structural and admission errors. They are wrapped with contextual
// information; use errors.Is for classification.
var (
	// ErrTruncated means the archive ends before a declared structure.
	ErrTruncated = errors.New("zipguard: truncated archive")

	// ErrBadSignature means an expected magic signature is missing or forged.
	ErrBadSignature = errors.New("zipguard: bad or missing signature")

	// ErrEOCDNotFound means no End of Central Directory record is locatable.
	ErrEOCDNotFound = errors.New("zipguard: end of central directory not found")

	// ErrMultipleEOCD means more than one structurally valid EOCD exists, or a
	// forged EOCD signature is embedded before the terminating EOCD.
	ErrMultipleEOCD = errors.New("zipguard: multiple or forged EOCD records")

	// ErrTrailingData means bytes exist after the terminating EOCD.
	ErrTrailingData = errors.New("zipguard: trailing data after EOCD")

	// ErrSplitArchive means a multi-disk/split archive is presented.
	ErrSplitArchive = errors.New("zipguard: split/multi-disk archives are rejected")

	// ErrUnsupported means a ZIP feature the policy does not admit is used
	// (unknown compression method, unsupported version, and similar).
	ErrUnsupported = errors.New("zipguard: unsupported zip feature")

	// ErrHeaderMismatch means LFH and CDH disagree on a declared field.
	ErrHeaderMismatch = errors.New("zipguard: LFH/CDH header mismatch")

	// ErrBadDescriptor means a data descriptor is missing, truncated or does
	// not match the central directory declaration.
	ErrBadDescriptor = errors.New("zipguard: bad data descriptor")

	// ErrOverlap means data regions of two entries overlap.
	ErrOverlap = errors.New("zipguard: overlapping entry data regions")

	// ErrGap means bytes are not referenced by any header or entry payload
	// (a hidden gap payload).
	ErrGap = errors.New("zipguard: unreferenced hidden gap in archive")

	// ErrDuplicateName means two entries share a normalized name.
	ErrDuplicateName = errors.New("zipguard: duplicate normalized entry name")

	// ErrUnsafeName means an entry name is absolute, traverses with "..",
	// contains a drive letter or is otherwise unsafe.
	ErrUnsafeName = errors.New("zipguard: unsafe entry name")

	// ErrUnsafeMode means an entry carries a symlink, SUID/SGID or another
	// non-regular special file mode in its external attributes.
	ErrUnsafeMode = errors.New("zipguard: unsafe unix file mode")

	// ErrCRC means the actual CRC-32 checksum does not match the declaration.
	ErrCRC = errors.New("zipguard: crc-32 mismatch")

	// ErrSizeMismatch means the real decompressed size differs from the
	// declared uncompressed size.
	ErrSizeMismatch = errors.New("zipguard: decompressed size mismatch")

	// ErrEntryTooLarge means a single entry exceeds the per-entry quota while
	// decompressing (counted on actual output bytes).
	ErrEntryTooLarge = errors.New("zipguard: entry exceeds uncompressed quota")

	// ErrTotalTooLarge means the sum of actual decompressed bytes exceeds the
	// archive-wide quota.
	ErrTotalTooLarge = errors.New("zipguard: archive exceeds total uncompressed quota")

	// ErrCompressionBomb means the compression ratio exceeds the policy
	// threshold or an entry declares a bomb-like geometry.
	ErrCompressionBomb = errors.New("zipguard: compression ratio bomb")

	// ErrTooManyEntries means the entry count exceeds the policy limit.
	ErrTooManyEntries = errors.New("zipguard: too many entries")
)
