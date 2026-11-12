package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Range is a half-open byte interval [Offset, Offset+Length) on the target
// device. Length zero is rejected because an update partition must occupy at
// least one byte.
type Range struct {
	Offset int64
	Length int64
}

// End returns the exclusive end offset.
func (r Range) End() int64 {
	return r.Offset + r.Length
}

// Valid reports whether the interval is non-empty and does not overflow the
// int64 address space.
func (r Range) Valid() bool {
	return r.Offset >= 0 && r.Length > 0 && r.End() >= r.Offset
}

// Within reports whether the interval fits inside a device of deviceSize
// bytes.
func (r Range) Within(deviceSize int64) bool {
	return r.Valid() && deviceSize >= 0 && r.End() <= deviceSize
}

// Overlaps reports whether two half-open intervals share any byte.
func (r Range) Overlaps(other Range) bool {
	if !r.Valid() || !other.Valid() {
		return false
	}
	return r.Offset < other.End() && other.Offset < r.End()
}

// HashAlgo identifies the digest algorithm used to verify a decompressed
// partition. Only algorithms from the standard library are supported so that
// the update host needs no third-party trust anchor.
type HashAlgo string

const (
	HashSHA224 HashAlgo = "sha224"
	HashSHA256 HashAlgo = "sha256"
	HashSHA384 HashAlgo = "sha384"
	HashSHA512 HashAlgo = "sha512"
)

// Compression identifies how a partition payload is encoded inside the
// upgrade package.
type Compression string

const (
	CompressNone Compression = "none"
	CompressGzip Compression = "gzip"
	CompressZlib Compression = "zlib"
	CompressBzip Compression = "bzip2"
)

// Decompressor turns one compressed payload reader into a stream of the
// partition's plaintext bytes.
//
// The writer handed to a decompressor is always backed by isolated staging
// storage, never by the target device. Decompressors therefore have no way to
// touch boot data or any other device region directly; the package only
// publishes verified bytes during Commit.
type Decompressor func(io.Reader) (io.ReadCloser, error)

// Partition describes one compressed region of an upgrade package together
// with the exact destination interval it claims on the target device.
type Partition struct {
	// Name is a unique, non-empty identifier used in diagnostics and for the
	// isolated staging file name.
	Name string

	// Dest is the claimed destination interval on the target device. Its
	// length is also the exact number of plaintext bytes the decompressor is
	// expected to emit.
	Dest Range

	// Compression selects the Decompressor used for Payload.
	Compression Compression

	// HashAlgo and ExpectedHash pin the digest of the fully decompressed
	// bytes. ExpectedHash must have the canonical length for HashAlgo.
	HashAlgo     HashAlgo
	ExpectedHash []byte

	// Open returns a fresh reader over the compressed payload. It is called
	// at most once during staging. A factory (instead of a shared reader)
	// keeps manifests reusable and avoids consumed-reader state bugs.
	Open func() (io.ReadCloser, error)
}

// Manifest is the parsed upgrade package: an ordered set of partitions plus
// the device ranges that must never be overwritten (for example bootloader and
// configuration partitions).
type Manifest struct {
	Partitions []Partition

	// Protected ranges are never written by an update. Destination intervals
	// intersecting any of them reject the whole package during preflight.
	Protected []Range
}

// Config carries the host-side limits and storage location for an update.
type Config struct {
	// DeviceSize is the total size of the target device in bytes. Every
	// destination and protected interval must fit inside it.
	DeviceSize int64

	// MaxExpansion is the maximum total plaintext size (sum of destination
	// lengths) that the package is allowed to expand to. It bounds
	// decompression-bomb resource usage.
	MaxExpansion int64

	// StagingDir is the root for isolated staging storage. When empty the
	// operating system temporary directory is used. The package creates a
	// private 0700 subdirectory beneath it and removes everything on Close or
	// failure.
	StagingDir string

	// Decompressors optionally overrides or extends the built-in registry.
	// Entries here take precedence over the built-in algorithms.
	Decompressors map[Compression]Decompressor
}

// Device is the write target. The package never hands this interface to a
// Decompressor; staging decompresses into files and Commit is the only phase
// that writes here, and only after every partition has been verified.
type Device interface {
	io.WriterAt
}

// StagedUpdate is a fully verified, isolated update that is ready to be
// committed to the device. Close releases the staging storage; calling Close
// more than once is safe.
type StagedUpdate struct {
	manifest Manifest
	config   Config
	dir      string
	items    []*stagedItem
	closed   bool
}

type stagedItem struct {
	part Partition
	path string
	file *os.File
	size int64
	hash []byte
}

// Sentinel errors. Callers should use errors.Is rather than matching message
// text.
var (
	ErrInvalidConfig      = errors.New("netsec: invalid update config")
	ErrEmptyManifest      = errors.New("netsec: manifest has no partitions")
	ErrInvalidRange       = errors.New("netsec: invalid byte range")
	ErrRangeOutOfDevice   = errors.New("netsec: destination range exceeds device size")
	ErrProtectedOverlap   = errors.New("netsec: destination overlaps protected range")
	ErrPartitionOverlap   = errors.New("netsec: partition destination ranges overlap")
	ErrExpansionBudget    = errors.New("netsec: total expansion exceeds budget")
	ErrDuplicateName      = errors.New("netsec: duplicate partition name")
	ErrUnknownCompression = errors.New("netsec: unknown compression algorithm")
	ErrUnknownHash        = errors.New("netsec: unknown hash algorithm")
	ErrBadHashLength      = errors.New("netsec: expected hash has wrong length")
	ErrMissingPayload     = errors.New("netsec: partition payload opener is missing")
	ErrLengthMismatch     = errors.New("netsec: decompressed length does not match declared destination length")
	ErrDigestMismatch     = errors.New("netsec: decompressed digest does not match expected hash")
	ErrPayloadOpen        = errors.New("netsec: cannot open partition payload")
	ErrDecompressor       = errors.New("netsec: decompressor failed")
	ErrStaging            = errors.New("netsec: staging storage failure")
	ErrClosed             = errors.New("netsec: staged update already closed")
	ErrCommitRecheck      = errors.New("netsec: staged data failed re-verification before commit")
	ErrDeviceWrite        = errors.New("netsec: target device write failed")
)

// Prepare runs preflight validation, decompresses every partition into
// isolated staging storage, and verifies exact length and digest. The target
// device is never accessed and is never visible to any decompression callback.
// Any failure rejects the whole package: staging storage is removed and no
// device bytes are written.
//
// The context bounds the potentially long decompression work; cancelling it
// aborts staging and returns an error wrapping ctx.Err().
func Prepare(ctx context.Context, manifest Manifest, config Config) (*StagedUpdate, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	registry, err := buildRegistry(config.Decompressors)
	if err != nil {
		return nil, err
	}

	if err := preflight(manifest, config, registry); err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp(config.StagingDir, "netsec-stage-*")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStaging, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%w: %v", ErrStaging, err)
	}

	update := &StagedUpdate{
		manifest: manifest,
		config:   config,
		dir:      dir,
		items:    make([]*stagedItem, 0, len(manifest.Partitions)),
	}

	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = update.closeStaging()
		}
	}()

	var total int64
	for index := range manifest.Partitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part := manifest.Partitions[index]
		item, err := stagePartition(ctx, dir, part, registry[part.Compression])
		if err != nil {
			return nil, err
		}
		update.items = append(update.items, item)
		total += item.size
	}

	// Defensive: per-partition checks already pin each size, but re-assert the
	// aggregate budget against bytes actually produced.
	if total > config.MaxExpansion {
		return nil, fmt.Errorf("%w: staged %d bytes > budget %d", ErrExpansionBudget, total, config.MaxExpansion)
	}

	cleanupOnFailure = false
	return update, nil
}
