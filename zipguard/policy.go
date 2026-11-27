package zipguard

import (
	"io/fs"
)

// Policy configures the admission limits. The zero value is not valid; use
// DefaultPolicy or construct explicit values. Every non-function field must
// be positive.
type Policy struct {
	// MaxEntries limits the number of central directory entries.
	MaxEntries int

	// MaxArchiveSize limits the total size of the archive in bytes.
	MaxArchiveSize int64

	// MaxEntryUncompressed limits the actual decompressed size of any single
	// entry, measured from decompressor output rather than header fields.
	MaxEntryUncompressed int64

	// MaxTotalUncompressed limits the sum of actual decompressed bytes over
	// all entries.
	MaxTotalUncompressed int64

	// MaxCompressionRatio rejects entries whose actual decompressed size
	// divided by their on-disk compressed payload size exceeds this value.
	// Entries with a compressed payload below ratioFloorBytes are exempt so
	// that tiny entries cannot trip the ratio check spuriously.
	MaxCompressionRatio float64

	// RatioFloorBytes is the minimum compressed payload size for ratio
	// enforcement.
	RatioFloorBytes int64

	// RejectSymlink rejects entries whose Unix mode is a symbolic link.
	RejectSymlink bool

	// RejectSetID rejects entries carrying the SUID or SGID mode bits.
	RejectSetID bool

	// RejectSpecial rejects non-regular, non-directory, non-symlink Unix
	// special files (devices, sockets, fifos).
	RejectSpecial bool
}

// DefaultPolicy returns a conservative policy suitable for artifact
// gateways. Callers may tune the returned copy.
func DefaultPolicy() Policy {
	return Policy{
		MaxEntries:           100_000,
		MaxArchiveSize:       512 << 20, // 512 MiB
		MaxEntryUncompressed: 256 << 20, // 256 MiB
		MaxTotalUncompressed: 2 << 40,   // 2 TiB
		MaxCompressionRatio:  250,
		RatioFloorBytes:      1024,
		RejectSymlink:        true,
		RejectSetID:          true,
		RejectSpecial:        true,
	}
}

// Entry describes one validated archive entry.
type Entry struct {
	// Name is the raw name as declared in the central directory.
	Name string

	// NormalizedName is the normalized form used for collision detection:
	// backslashes converted to slashes and ASCII case folded.
	NormalizedName string

	// IsDir reports whether the entry is a directory (trailing slash).
	IsDir bool

	// Method is the compression method from the central directory.
	Method uint16

	// CRC32 is the declared checksum.
	CRC32 uint32

	// CompressedSize is the on-disk payload size.
	CompressedSize uint64

	// UncompressedSize is the declared size; after a successful verification
	// it equals the number of bytes actually produced by decompression.
	UncompressedSize uint64

	// ExternalAttrs holds the raw external attributes field.
	ExternalAttrs uint32

	// UnixMode is the Unix mode extracted from ExternalAttrs and zero when
	// the entry's host system is not Unix-like.
	UnixMode fs.FileMode
}

// Report summarizes a successfully verified archive.
type Report struct {
	// Entries are the validated entries in central directory order.
	Entries []Entry

	// TotalCompressed is the sum of all on-disk payload sizes.
	TotalCompressed int64

	// TotalUncompressed is the sum of actual decompressed bytes.
	TotalUncompressed int64

	// MaxObservedRatio is the largest per-entry actual ratio observed
	// (uncompressed bytes / compressed payload bytes).
	MaxObservedRatio float64
}

func (p Policy) normalized() (Policy, error) {
	if p.MaxEntries <= 0 || p.MaxArchiveSize <= 0 ||
		p.MaxEntryUncompressed <= 0 || p.MaxTotalUncompressed <= 0 ||
		p.MaxCompressionRatio <= 0 || p.RatioFloorBytes < 0 {
		return Policy{}, ErrUnsupported
	}
	return p, nil
}
