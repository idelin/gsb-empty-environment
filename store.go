// Package blkmap implements a sparse logical-offset to immutable
// physical-block interval map with copy-on-write writes, hole punching
// and snapshots. All state lives in a caller-supplied directory; faults
// are simulated through injection points.
package blkmap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultBlockSize is used when Open is called with blockSize == 0 on a
// fresh directory.
const DefaultBlockSize = 4096

// Fault injection points passed to Store.Inject:
//
//	"block.staged"        block temp file written and fsynced, before rename
//	"block.committed"     block renamed into place
//	"manifest.staged"     manifest temp file written and fsynced, before rename
//	"manifest.committed"  manifest renamed and directory fsynced
//
// Returning a non-nil error aborts the operation at that point, simulating
// a crash: the on-disk state is exactly what a process crash or power loss
// would leave behind, and reopening the directory exercises recovery.
const (
	PointBlockStaged       = "block.staged"
	PointBlockCommitted    = "block.committed"
	PointManifestStaged    = "manifest.staged"
	PointManifestCommitted = "manifest.committed"
)

var ErrSnapshotNotFound = errors.New("blkmap: snapshot not found")

type manifest struct {
	BlockSize int64               `json:"blockSize"`
	NextBlock uint64              `json:"nextBlock"`
	Extents   []Extent            `json:"extents"`
	Snaps     map[string][]Extent `json:"snaps"`
}

// Store is a single-writer interval map over immutable physical blocks.
type Store struct {
	dir       string
	blockSize int64

	mu sync.Mutex
	m  manifest

	// Inject, when non-nil, is called at every fault injection point.
	// It must only be set before use; it is read under s.mu.
	Inject func(point string) error
}

// Open creates or opens the store rooted at dir. On a fresh directory
// blockSize (or DefaultBlockSize when 0) is recorded in the manifest; on
// reopen it must match the recorded value (0 means "use recorded").
// Open also performs crash recovery: stale temp files and physical blocks
// not referenced by the committed manifest or any snapshot are removed.
func Open(dir string, blockSize int64) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blocks"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	data, err := os.ReadFile(s.manifestPath())
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &s.m); err != nil {
			return nil, fmt.Errorf("blkmap: corrupt manifest: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		if blockSize == 0 {
			blockSize = DefaultBlockSize
		}
		s.m = manifest{BlockSize: blockSize, NextBlock: 1, Snaps: map[string][]Extent{}}
		if err := s.commitLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if blockSize == 0 {
		blockSize = s.m.BlockSize
	}
	if blockSize != s.m.BlockSize {
		return nil, fmt.Errorf("blkmap: block size mismatch: manifest %d, requested %d", s.m.BlockSize, blockSize)
	}
	s.blockSize = s.m.BlockSize
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// BlockSize returns the store's immutable block size.
func (s *Store) BlockSize() int64 { return s.blockSize }

// WriteAt copy-on-writes p at logical offset off: every physical block it
// touches is newly allocated, and only the covered byte range changes.
// Holes and previously written data outside [off, off+len(p)) are preserved.
// A nil return means the write is durable (blocks and manifest fsynced).
func (s *Store) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("blkmap: negative offset")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	bs := uint64(s.blockSize)
	begin, end := uint64(off), uint64(off)+uint64(len(p))
	var repl []Extent
	for pos := begin; pos < end; {
		blkStart := pos / bs * bs
		chunkEnd := min(blkStart+bs, end)
		var data []byte
		if pos == blkStart && chunkEnd == blkStart+bs {
			data = p[pos-begin : chunkEnd-begin] // full block, no read needed
		} else {
			data = make([]byte, bs)
			if err := s.readLocked(data, int64(blkStart), s.m.Extents); err != nil {
				return 0, err
			}
			copy(data[pos-blkStart:chunkEnd-blkStart], p[pos-begin:chunkEnd-begin])
		}
		id, err := s.writeBlockLocked(data)
		if err != nil {
			return 0, err
		}
		repl = append(repl, Extent{Off: blkStart, Len: bs, Block: id, BOff: 0})
		pos = chunkEnd
	}
	if len(repl) > 0 {
		repOff := repl[0].Off
		repLen := repl[len(repl)-1].Off + repl[len(repl)-1].Len - repOff
		s.m.Extents = replaceRange(s.m.Extents, repOff, repLen, repl)
		if err := s.commitLocked(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// ReadAt reads len(p) bytes at logical offset off. Holes and ranges past
// the end of all written data read as zeros, so it always returns len(p).
func (s *Store) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("blkmap: negative offset")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(p), s.readLocked(p, off, s.m.Extents)
}

// PunchHole drops every extent overlapping [off, off+length), splitting
// extents at the boundaries so data on both sides is preserved. The hole
// reads back as zeros and occupies no physical block.
func (s *Store) PunchHole(off, length int64) error {
	if off < 0 || length < 0 {
		return errors.New("blkmap: negative offset or length")
	}
	if length == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m.Extents = replaceRange(s.m.Extents, uint64(off), uint64(length), nil)
	return s.commitLocked()
}

// Snapshot atomically records the current extent map under name. Because
// physical blocks are immutable, later writes and punches never disturb
// the snapshot, and the snapshot itself allocates no new blocks.
func (s *Store) Snapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Extent, len(s.m.Extents))
	copy(cp, s.m.Extents)
	s.m.Snaps[name] = cp
	return s.commitLocked()
}

// DeleteSnapshot removes a snapshot. Blocks referenced only by it become
// unreachable and are reclaimed on the next Open.
func (s *Store) DeleteSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m.Snaps, name)
	return s.commitLocked()
}

// ReadSnapshotAt reads from the extent map frozen by Snapshot.
func (s *Store) ReadSnapshotAt(name string, p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("blkmap: negative offset")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, ok := s.m.Snaps[name]
	if !ok {
		return 0, ErrSnapshotNotFound
	}
	return len(p), s.readLocked(p, off, exts)
}

// Extents returns a copy of the live extent map (for inspection/tests).
func (s *Store) Extents() []Extent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Extent, len(s.m.Extents))
	copy(out, s.m.Extents)
	return out
}

// readLocked fills p from exts at logical offset off, zero-filling holes.
func (s *Store) readLocked(p []byte, off int64, exts []Extent) error {
	for i := range p {
		p[i] = 0
	}
	start, end := uint64(off), uint64(off)+uint64(len(p))
	for _, e := range exts {
		if e.Off >= end {
			break
		}
		if e.Off+e.Len <= start {
			continue
		}
		lo, hi := max(start, e.Off), min(end, e.Off+e.Len)
		f, err := os.Open(s.blockPath(e.Block))
		if err != nil {
			return err
		}
		_, rerr := f.ReadAt(p[lo-start:hi-start], int64(e.BOff+lo-e.Off))
		cerr := f.Close()
		if rerr != nil {
			return rerr
		}
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

// writeBlockLocked durably publishes a new immutable block and returns
// its id. The block goes through write temp -> fsync -> rename, so a
// crash leaves either nothing or a complete block, never a partial one.
func (s *Store) writeBlockLocked(data []byte) (uint64, error) {
	id := s.m.NextBlock
	tmp := s.blockPath(id) + ".tmp"
	if err := writeFileSync(tmp, data); err != nil {
		return 0, err
	}
	if err := s.inject(PointBlockStaged); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, s.blockPath(id)); err != nil {
		return 0, err
	}
	if err := s.inject(PointBlockCommitted); err != nil {
		return 0, err
	}
	s.m.NextBlock++
	return id, nil
}

// commitLocked atomically replaces the manifest via write temp -> fsync
// -> rename -> fsync(dir), so a crash exposes either the old or the new
// manifest, never a torn one.
func (s *Store) commitLocked() error {
	data, err := json.MarshalIndent(&s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.manifestPath() + ".tmp"
	if err := writeFileSync(tmp, data); err != nil {
		return err
	}
	if err := s.inject(PointManifestStaged); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.manifestPath()); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	return s.inject(PointManifestCommitted)
}

// recover removes stale temp files and physical blocks unreachable from
// the committed manifest and all snapshots (orphans left by a crash
// between block commit and manifest commit).
func (s *Store) recover() error {
	live := map[uint64]bool{}
	for _, e := range s.m.Extents {
		live[e.Block] = true
	}
	for _, snap := range s.m.Snaps {
		for _, e := range snap {
			live[e.Block] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "blocks"))
	if err != nil {
		return err
	}
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasSuffix(name, ".tmp") {
			if err := os.Remove(filepath.Join(s.dir, "blocks", name)); err != nil {
				return err
			}
			continue
		}
		var id uint64
		if _, err := fmt.Sscanf(name, "%x.blk", &id); err != nil || !live[id] {
			if err := os.Remove(filepath.Join(s.dir, "blocks", name)); err != nil {
				return err
			}
		}
	}
	os.Remove(s.manifestPath() + ".tmp") // best effort
	return syncDir(filepath.Join(s.dir, "blocks"))
}

func (s *Store) inject(point string) error {
	if s.Inject == nil {
		return nil
	}
	return s.Inject(point)
}

func (s *Store) manifestPath() string { return filepath.Join(s.dir, "manifest.json") }

func (s *Store) blockPath(id uint64) string {
	return filepath.Join(s.dir, "blocks", fmt.Sprintf("%016x.blk", id))
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// sortedNonOverlapping reports whether exts satisfies the map invariant;
// it is exported for tests and tools.
func sortedNonOverlapping(exts []Extent) bool {
	return sort.SliceIsSorted(exts, func(i, j int) bool {
		return exts[i].Off+exts[i].Len <= exts[j].Off
	})
}
