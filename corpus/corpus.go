// Package corpus implements a content-addressed sample corpus for fuzzing
// hosts. Coverage is modeled as a set of edge IDs; the store preserves the
// union of all submitted coverage while preferring shorter samples, breaking
// ties by content digest. Every mutation atomically publishes an immutable
// index, and recovery after a crash never leaves the index referencing
// missing files.
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// ErrEmptySample is returned when adding a zero-length sample.
var ErrEmptySample = errors.New("corpus: empty sample")

// Store is a corpus rooted at a directory on disk. It is safe for concurrent
// use within a process, and distinct processes may open the same directory:
// mutations are serialized with an advisory file lock.
type Store struct {
	dir  string
	mu   sync.Mutex
	lock *os.File
	seq  int
	hook func(op string) error
}

// Option configures a Store.
type Option func(*Store)

// withFaultHook installs a hook invoked before every critical disk
// operation. A non-nil return aborts the operation. Used by tests to inject
// write failures.
func withFaultHook(h func(op string) error) Option {
	return func(s *Store) { s.hook = h }
}

// Open creates the corpus directory layout if needed, recovers from any
// interrupted state, and returns the store.
func Open(dir string, opts ...Option) (*Store, error) {
	s := &Store{dir: dir}
	for _, o := range opts {
		o(s)
	}
	for _, d := range []string{dir, s.samplesDir(), s.indexDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	s.lock = lock
	s.nextSeq()
	if err := s.withLock(func() error {
		_, err := s.lockedIndex()
		return err
	}); err != nil {
		lock.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the store's resources.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lock.Close()
}

func (s *Store) check(op string) error {
	if s.hook != nil {
		return s.hook(op)
	}
	return nil
}

// withLock serializes against other Store instances in this process and,
// via flock, against other processes sharing the directory.
func (s *Store) withLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return fn()
}

// Add submits a sample with its coverage edges. It reports whether the
// corpus changed: re-adding known content with no new edges is a no-op,
// while known content carrying additional edges merges them into the
// existing entry. The sample file is durable before the updated index is
// published, so a crash can leave at most an unreferenced orphan file.
func (s *Store) Add(sample []byte, edges []uint64) (bool, error) {
	if len(sample) == 0 {
		return false, ErrEmptySample
	}
	sum := sha256.Sum256(sample)
	digest := hex.EncodeToString(sum[:])
	var changed bool
	err := s.withLock(func() error {
		idx, err := s.lockedIndex()
		if err != nil {
			return err
		}
		if e, err := idx.find(digest); err == nil {
			merged := unionEdges(e.Edges, edges)
			if equalEdges(merged, e.Edges) {
				return nil
			}
			e.Edges = merged
			changed = true
			return s.publish(idx)
		}
		if err := s.putFile(s.samplePath(digest), sample, "write-sample", "sync-sample"); err != nil {
			return err
		}
		meta, err := json.Marshal(metaFile{Edges: normEdges(edges)})
		if err != nil {
			return err
		}
		if err := s.putFile(s.metaPath(digest), meta, "write-meta", "sync-meta"); err != nil {
			return err
		}
		idx.Entries = append(idx.Entries, Entry{
			Digest: digest,
			Length: len(sample),
			Edges:  normEdges(edges),
		})
		sortEntries(idx.Entries)
		changed = true
		return s.publish(idx)
	})
	return changed, err
}

// Entries returns the entries of the currently published index, ordered by
// length then digest.
func (s *Store) Entries() ([]Entry, error) {
	var out []Entry
	err := s.withLock(func() error {
		idx, err := s.lockedIndex()
		if err != nil {
			return err
		}
		out = append(out, idx.Entries...)
		return nil
	})
	return out, err
}

// Coverage returns the sorted union of edge IDs covered by the corpus.
func (s *Store) Coverage() ([]uint64, error) {
	var cov []uint64
	err := s.withLock(func() error {
		idx, err := s.lockedIndex()
		if err != nil {
			return err
		}
		set := map[uint64]struct{}{}
		for _, e := range idx.Entries {
			for _, edge := range e.Edges {
				set[edge] = struct{}{}
			}
		}
		for edge := range set {
			cov = append(cov, edge)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return normEdges(cov), nil
}

// Compact drops entries whose edges are fully covered by other entries,
// considering candidates shortest-first (ties by digest) so shorter samples
// are preferred. The union of covered edges is preserved exactly. The new
// index is published before any file is deleted, so the index never
// references a missing file even if the process crashes mid-compaction.
func (s *Store) Compact() error {
	return s.withLock(func() error {
		idx, err := s.lockedIndex()
		if err != nil {
			return err
		}
		sorted := append([]Entry(nil), idx.Entries...)
		sortEntries(sorted)
		covered := map[uint64]struct{}{}
		var keep, drop []Entry
		for _, e := range sorted {
			useful := false
			for _, edge := range e.Edges {
				if _, ok := covered[edge]; !ok {
					useful = true
					break
				}
			}
			if useful {
				keep = append(keep, e)
				for _, edge := range e.Edges {
					covered[edge] = struct{}{}
				}
			} else {
				drop = append(drop, e)
			}
		}
		if len(drop) == 0 {
			return nil
		}
		idx.Entries = keep
		if err := s.publish(idx); err != nil {
			return err
		}
		for _, e := range drop {
			if err := s.check("remove"); err != nil {
				return err
			}
			os.Remove(s.samplePath(e.Digest))
			os.Remove(s.metaPath(e.Digest))
		}
		if err := s.syncDir(s.samplesDir()); err != nil {
			return err
		}
		s.pruneIndexes()
		return nil
	})
}

// Validate checks that the published index references only files that exist
// on disk. It does not mutate the store.
func (s *Store) Validate() error {
	return s.withLock(func() error {
		idx, err := s.loadIndex()
		if err != nil {
			return err
		}
		for _, e := range idx.Entries {
			info, err := os.Stat(s.samplePath(e.Digest))
			if err != nil {
				return fmt.Errorf("corpus: index references missing sample %s: %w", e.Digest, err)
			}
			if int(info.Size()) != e.Length {
				return fmt.Errorf("corpus: sample %s has length %d, index says %d", e.Digest, info.Size(), e.Length)
			}
		}
		return nil
	})
}
