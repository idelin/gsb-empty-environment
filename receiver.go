// Package snaprecv receives versioned snapshots delivered as unordered,
// hash-verified chunks. Chunks are staged and verified before anything
// becomes visible; installation atomically swaps the visible snapshot.
package snaprecv

import (
	"crypto/sha256"
	"errors"
	"sync"
)

var (
	// ErrStaleVersion is returned for chunks whose version is not newer
	// than the installed or currently staged snapshot.
	ErrStaleVersion = errors.New("snaprecv: stale version")
	// ErrHashMismatch is returned when a chunk's data does not match its
	// declared SHA-256 hash.
	ErrHashMismatch = errors.New("snaprecv: chunk hash mismatch")
	// ErrConflict is returned when a chunk's bytes disagree with already
	// verified bytes in the overlapping range, or when its declared total
	// size disagrees with the staged snapshot.
	ErrConflict = errors.New("snaprecv: chunk conflicts with verified data")
	// ErrInvalidChunk is returned for empty or out-of-bounds chunks.
	ErrInvalidChunk = errors.New("snaprecv: invalid chunk")
	// ErrNoSnapshot is returned by Read when nothing is installed yet.
	ErrNoSnapshot = errors.New("snaprecv: no snapshot installed")
)

// HookPoint identifies a crash cut point inside the receive/install path.
// Tests use it to inject faults; production may leave Options.Hook nil.
type HookPoint string

const (
	// HookChunkDataWritten fires after chunk bytes hit the staging file,
	// before they are fsynced.
	HookChunkDataWritten HookPoint = "chunk.data.written"
	// HookChunkDataSynced fires after the staging data file is fsynced,
	// before the verified-interval manifest is updated.
	HookChunkDataSynced HookPoint = "chunk.data.synced"
	// HookManifestSynced fires after the manifest recording the newly
	// verified interval is fsynced.
	HookManifestSynced HookPoint = "manifest.synced"
	// HookInstallRenamed fires after the fully verified staging directory
	// is renamed into snapshots/v<N>, before the visible swap.
	HookInstallRenamed HookPoint = "install.renamed"
	// HookInstallSymlinked fires after the `current` symlink atomically
	// points at the new snapshot.
	HookInstallSymlinked HookPoint = "install.symlinked"
	// HookInstallCleaned fires after obsolete snapshots are removed.
	HookInstallCleaned HookPoint = "install.cleaned"
)

// Chunk is one delivered piece of a snapshot. Chunks for the same version
// may arrive in any order and may overlap; overlapping bytes must agree.
type Chunk struct {
	Version   uint64   // global snapshot version this chunk belongs to
	Offset    uint64   // byte offset of Data within the snapshot
	TotalSize uint64   // total snapshot size in bytes (must agree per version)
	Data      []byte   // chunk payload
	Hash      [32]byte // SHA-256 of Data
}

// NewChunk builds a Chunk with a correctly computed hash.
func NewChunk(version, offset, totalSize uint64, data []byte) Chunk {
	return Chunk{
		Version:   version,
		Offset:    offset,
		TotalSize: totalSize,
		Data:      data,
		Hash:      sha256.Sum256(data),
	}
}

// Options configures a Receiver.
type Options struct {
	// Dir is the root directory holding staging, snapshots and the
	// `current` symlink. It is created if missing.
	Dir string
	// Hook, if non-nil, is invoked at every HookPoint cut point.
	Hook func(HookPoint)
}

// Receiver accepts chunks for increasingly newer snapshot versions and
// atomically installs them once fully verified. Safe for concurrent use.
type Receiver struct {
	mu   sync.Mutex
	dir  string
	hook func(HookPoint)

	installed uint64 // version currently visible via `current`; 0 = none

	staged    uint64     // version being assembled in staging; 0 = none
	total     uint64     // expected total size of the staged version
	intervals []interval // verified byte ranges of the staged version
	data      fileWriter // staging data file; nil when staged == 0
}

// Open creates or recovers a Receiver rooted at opts.Dir. Recovery reuses
// already verified staged chunks and completes any install that was
// interrupted after its rename cut point.
func Open(opts Options) (*Receiver, error) {
	r := &Receiver{dir: opts.Dir, hook: opts.Hook}
	if err := r.recover(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Receiver) call(p HookPoint) {
	if r.hook != nil {
		r.hook(p)
	}
}

// HandleChunk verifies and stages one chunk. When the chunk completes the
// staged version, the snapshot is installed atomically before returning.
func (r *Receiver) HandleChunk(c Chunk) error {
	if c.Hash != sha256.Sum256(c.Data) {
		return ErrHashMismatch
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if c.Version <= r.installed || (r.staged != 0 && c.Version < r.staged) {
		return ErrStaleVersion
	}
	if c.Version > r.staged {
		if err := r.resetStaging(c.Version); err != nil {
			return err
		}
	}

	if len(c.Data) == 0 || c.TotalSize == 0 ||
		c.Offset > c.TotalSize || uint64(len(c.Data)) > c.TotalSize-c.Offset {
		return ErrInvalidChunk
	}
	if r.total != 0 && c.TotalSize != r.total {
		return ErrConflict
	}
	r.total = c.TotalSize

	if err := r.checkOverlap(c); err != nil {
		return err
	}

	if _, err := r.data.WriteAt(c.Data, int64(c.Offset)); err != nil {
		return err
	}
	r.call(HookChunkDataWritten)
	if err := r.data.Sync(); err != nil {
		return err
	}
	r.call(HookChunkDataSynced)

	r.intervals = addInterval(r.intervals, interval{Off: c.Offset, Len: uint64(len(c.Data))})
	if err := r.writeManifest(); err != nil {
		return err
	}
	r.call(HookManifestSynced)

	if r.covered() {
		return r.install()
	}
	return nil
}

// Read returns the currently visible snapshot content and its version.
func (r *Receiver) Read() ([]byte, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.installed == 0 {
		return nil, 0, ErrNoSnapshot
	}
	b, err := readFile(r.currentDataPath())
	if err != nil {
		return nil, 0, err
	}
	return b, r.installed, nil
}

// CurrentVersion reports the installed version, 0 if none.
func (r *Receiver) CurrentVersion() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.installed
}

// Close releases the staging file handle. State is already durable per
// the write protocol, so Close never fails.
func (r *Receiver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data != nil {
		r.data.Close()
		r.data = nil
	}
	return nil
}

// covered reports whether verified intervals exactly span [0, total).
func (r *Receiver) covered() bool {
	return r.total > 0 && len(r.intervals) == 1 &&
		r.intervals[0].Off == 0 && r.intervals[0].Len == r.total
}

// checkOverlap compares c against verified bytes in overlapping ranges.
func (r *Receiver) checkOverlap(c Chunk) error {
	end := c.Offset + uint64(len(c.Data))
	for _, iv := range r.intervals {
		lo := max64(c.Offset, iv.Off)
		hi := min64(end, iv.Off+iv.Len)
		if lo >= hi {
			continue
		}
		stored := make([]byte, hi-lo)
		if _, err := r.data.ReadAt(stored, int64(lo)); err != nil {
			return err
		}
		for i := uint64(0); i < hi-lo; i++ {
			if stored[i] != c.Data[lo-c.Offset+i] {
				return ErrConflict
			}
		}
	}
	return nil
}

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// addInterval inserts n and merges overlapping or adjacent intervals.
// ivs must be sorted by Off; the result stays sorted and disjoint.
func addInterval(ivs []interval, n interval) []interval {
	out := make([]interval, 0, len(ivs)+1)
	inserted := false
	for _, iv := range ivs {
		if !inserted && n.Off < iv.Off {
			out = append(out, n)
			inserted = true
		}
		out = append(out, iv)
	}
	if !inserted {
		out = append(out, n)
	}
	merged := out[:1]
	for _, iv := range out[1:] {
		last := &merged[len(merged)-1]
		if iv.Off <= last.Off+last.Len {
			if end := iv.Off + iv.Len; end > last.Off+last.Len {
				last.Len = end - last.Off
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}
