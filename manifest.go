package netsec

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidManifest is returned when a manifest is malformed.
var ErrInvalidManifest = errors.New("netsec: invalid manifest")

// ErrConflictingManifest is returned when an installed manifest has the same
// generation as an existing one but different contents.
var ErrConflictingManifest = errors.New("netsec: conflicting manifest for same generation")

// ErrObsoleteManifest is returned when a manifest generation is older than the
// generation already installed.
var ErrObsoleteManifest = errors.New("netsec: obsolete manifest generation")

// CheckID identifies one detector/check family, for example "av" or "dlp".
type CheckID string

// Digest is the integrity digest of a chunk.
type Digest struct {
	Algorithm string
	Hex       string
}

// ChunkSpec fixes the index, byte length and digest of one object fragment.
type ChunkSpec struct {
	Index  int
	Length int64
	Digest Digest
}

// Manifest fixes the fragments of an object for one generation and the set of
// checks that must complete for every fragment.
type Manifest struct {
	// Generation monotonically increases whenever the fragment list changes,
	// for example when a fragment is replaced.
	Generation int
	// Chunks must use contiguous indexes starting at zero.
	Chunks []ChunkSpec
	// Checks is the complete check set required for every chunk.
	Checks []CheckID
	// Timeout bounds how long the aggregator waits for outstanding receipts.
	// A zero value means no deadline is scheduled.
	Timeout time.Duration
}

// Validate reports whether the manifest is well formed.
func (m Manifest) Validate() error {
	if m.Generation < 0 {
		return fmt.Errorf("%w: negative generation", ErrInvalidManifest)
	}
	if len(m.Chunks) == 0 {
		return fmt.Errorf("%w: no chunks", ErrInvalidManifest)
	}
	for i, chunk := range m.Chunks {
		if chunk.Index != i {
			return fmt.Errorf("%w: chunk %d has non-contiguous index %d", ErrInvalidManifest, i, chunk.Index)
		}
		if chunk.Length < 0 {
			return fmt.Errorf("%w: chunk %d has negative length", ErrInvalidManifest, i)
		}
		if chunk.Digest.Algorithm == "" || chunk.Digest.Hex == "" {
			return fmt.Errorf("%w: chunk %d missing digest", ErrInvalidManifest, i)
		}
	}
	if len(m.Checks) == 0 {
		return fmt.Errorf("%w: no checks", ErrInvalidManifest)
	}
	seen := make(map[CheckID]struct{}, len(m.Checks))
	for _, check := range m.Checks {
		if check == "" {
			return fmt.Errorf("%w: empty check id", ErrInvalidManifest)
		}
		if _, ok := seen[check]; ok {
			return fmt.Errorf("%w: duplicate check %q", ErrInvalidManifest, check)
		}
		seen[check] = struct{}{}
	}
	if m.Timeout < 0 {
		return fmt.Errorf("%w: negative timeout", ErrInvalidManifest)
	}
	return nil
}

// equal reports whether two validated manifests describe identical content.
func (m Manifest) equal(other Manifest) bool {
	if m.Generation != other.Generation || m.Timeout != other.Timeout ||
		len(m.Chunks) != len(other.Chunks) || len(m.Checks) != len(other.Checks) {
		return false
	}
	for i := range m.Chunks {
		if m.Chunks[i] != other.Chunks[i] {
			return false
		}
	}
	for i := range m.Checks {
		if m.Checks[i] != other.Checks[i] {
			return false
		}
	}
	return true
}
