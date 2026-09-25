// Package chunkstore is a content-addressed chunked value store.
//
// Layout inside the caller-specified directory:
//
//	<dir>/blocks/<hex[0:2]>/<hex>   content-addressed block files (sha256)
//	<dir>/meta/state.json           committed metadata transaction image
//
// Large values are split into fixed-size blocks. Each block is stored once
// under its sha256 digest, so identical blocks are shared between values.
// Metadata transactions (JSON image, temp-file + rename + fsync) maintain
// the value->block-list mapping and per-block reference counts. Every read
// re-hashes each block and verifies it against its digest.
//
// Overwrite, delete and rollback only touch blocks whose reference count
// drops to zero; the normal write path never scans the data directory.
//
// Crash / power-loss boundary:
//   - Block files are written to a temp file, fsynced, renamed, then the
//     directory is fsynced: a committed block is never torn.
//   - The metadata commit is a single atomic rename; a crash leaves either
//     the old or the new state, never a partial one.
//   - A crash between block writes and the metadata commit leaks orphan
//     block files (space, never correctness). A crash after commit but
//     before reclaim leaks zero-reference blocks. Both are reclaimed by an
//     explicit CollectGarbage scan; they are never scanned on the normal
//     write path.
//   - Latent on-disk corruption (bit rot) is detected at read time by the
//     per-block digest verification.
package chunkstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// DefaultBlockSize is used when Open is given a non-positive block size.
const DefaultBlockSize = 1 << 20 // 1 MiB

// ErrNotFound is returned by Get/Delete for unknown value names.
var ErrNotFound = errors.New("chunkstore: value not found")

// ErrCorrupt is returned when a block fails digest verification on read.
var ErrCorrupt = errors.New("chunkstore: block digest mismatch")

type metaState struct {
	Values map[string][]string `json:"values"` // name -> ordered block digests
	Refs   map[string]int      `json:"refs"`   // digest -> reference count
}

func (m metaState) clone() metaState {
	values := make(map[string][]string, len(m.Values))
	for k, v := range m.Values {
		values[k] = append([]string(nil), v...)
	}
	refs := make(map[string]int, len(m.Refs))
	for k, v := range m.Refs {
		refs[k] = v
	}
	return metaState{Values: values, Refs: refs}
}

// Store is a content-addressed chunked value store rooted at a directory.
type Store struct {
	dir       string
	blockSize int

	mu     sync.Mutex
	meta   metaState
	faults *FaultInjector
	hook   func(point string) // optional crash-simulation hook, fires at fault points
}

// Open creates or opens a store rooted at dir, creating it if needed.
// blockSize <= 0 selects DefaultBlockSize.
func Open(dir string, blockSize int) (*Store, error) {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	if err := os.MkdirAll(filepath.Join(dir, "blocks"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		return nil, err
	}
	// Recovery: an uncommitted transaction image is simply discarded; the
	// committed state.json is either fully old or fully new.
	_ = os.Remove(filepath.Join(dir, "meta", "state.json.tmp"))

	s := &Store{
		dir:       dir,
		blockSize: blockSize,
		meta: metaState{
			Values: make(map[string][]string),
			Refs:   make(map[string]int),
		},
	}
	data, err := os.ReadFile(s.metaPath())
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.meta); err != nil {
		return nil, fmt.Errorf("chunkstore: corrupt metadata: %w", err)
	}
	if s.meta.Values == nil {
		s.meta.Values = make(map[string][]string)
	}
	if s.meta.Refs == nil {
		s.meta.Refs = make(map[string]int)
	}
	return s, nil
}

// SetFaultInjector installs a fault injector (nil disables injection).
func (s *Store) SetFaultInjector(f *FaultInjector) { s.faults = f }

// SetPointHook installs a hook invoked at every fault point. Tests use it
// to simulate a process crash (e.g. by panicking) at an exact interleaving.
func (s *Store) SetPointHook(h func(point string)) { s.hook = h }

// BlockSize returns the store's block size in bytes.
func (s *Store) BlockSize() int { return s.blockSize }

func (s *Store) point(name string) {
	if s.hook != nil {
		s.hook(name)
	}
}

func (s *Store) metaPath() string { return filepath.Join(s.dir, "meta", "state.json") }

func (s *Store) blockPath(digest string) string {
	return filepath.Join(s.dir, "blocks", digest[:2], digest)
}

// Put stores the content of r under name, replacing any existing value.
// Content is streamed through fixed-size blocks; blocks already present
// (by digest) are shared and not rewritten. On any failure the blocks
// written by this call are removed again and the previous state is kept.
func (s *Store) Put(name string, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var digests []string
	var newBlocks []string // block files created by this call, for rollback
	cleanup := func() {
		for _, d := range newBlocks {
			_ = os.Remove(s.blockPath(d))
		}
	}

	buf := make([]byte, s.blockSize)
	for {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			sum := sha256.Sum256(buf[:n])
			d := hex.EncodeToString(sum[:])
			digests = append(digests, d)
			if s.meta.Refs[d] == 0 {
				// Not referenced: either absent, or a leaked orphan from an
				// earlier crash. Adopt it only if it verifies; else rewrite.
				if err := s.verifyBlock(d); err != nil {
					if werr := s.writeBlock(d, buf[:n]); werr != nil {
						cleanup()
						return werr
					}
					newBlocks = append(newBlocks, d)
				}
			}
			s.point("block.write")
			if err := s.faults.maybeFail("block.write"); err != nil {
				cleanup()
				return err
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			cleanup()
			return rerr
		}
	}

	next := s.meta.clone()
	if old, ok := next.Values[name]; ok {
		for _, d := range old {
			next.Refs[d]--
		}
	}
	next.Values[name] = digests
	for _, d := range digests {
		next.Refs[d]++
	}
	zeros := dropZeroRefs(next.Refs)

	if err := s.commitMeta(&next); err != nil {
		cleanup()
		return err
	}
	s.meta = next
	s.reclaim(zeros)
	return nil
}

// Get returns the value stored under name, verifying every block's digest.
func (s *Store) Get(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	digests, ok := s.meta.Values[name]
	if !ok {
		return nil, ErrNotFound
	}
	var out []byte
	for _, d := range digests {
		data, err := os.ReadFile(s.blockPath(d))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != d {
			return nil, fmt.Errorf("%w: %s", ErrCorrupt, d)
		}
		out = append(out, data...)
	}
	return out, nil
}

// Delete removes the value stored under name and reclaims blocks whose
// reference count drops to zero. Blocks shared with other values survive.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.meta.Values[name]
	if !ok {
		return ErrNotFound
	}
	next := s.meta.clone()
	delete(next.Values, name)
	for _, d := range old {
		next.Refs[d]--
	}
	zeros := dropZeroRefs(next.Refs)

	if err := s.commitMeta(&next); err != nil {
		return err
	}
	s.meta = next
	s.reclaim(zeros)
	return nil
}

// List returns the names of all stored values.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.meta.Values))
	for name := range s.meta.Values {
		names = append(names, name)
	}
	return names
}

// CollectGarbage scans the blocks directory and removes block files that
// are not referenced by the committed metadata, plus leftover temp files.
// It exists solely to reclaim space leaked by crashes; the normal write
// path never calls it and never scans the data directory.
func (s *Store) CollectGarbage() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	root := filepath.Join(s.dir, "blocks")
	return filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		name := e.Name()
		if len(name) != sha256.Size*2 { // stray temp file from a crashed write
			return os.Remove(path)
		}
		if s.meta.Refs[name] == 0 {
			return os.Remove(path)
		}
		return nil
	})
}

// commitMeta durably and atomically replaces the committed metadata image.
func (s *Store) commitMeta(m *metaState) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := s.metaPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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
	if err := f.Close(); err != nil {
		return err
	}
	s.point("meta.commit")
	if err := s.faults.maybeFail("meta.commit"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.metaPath()); err != nil {
		return err
	}
	return syncDir(filepath.Join(s.dir, "meta"))
}

// reclaim unlinks zero-reference blocks after the metadata transaction has
// committed. Failures only leak space; CollectGarbage can recover it later.
func (s *Store) reclaim(digests []string) {
	for _, d := range digests {
		s.point("reclaim")
		if s.faults.maybeFail("reclaim") != nil {
			continue
		}
		_ = os.Remove(s.blockPath(d))
	}
}

func dropZeroRefs(refs map[string]int) []string {
	var zeros []string
	for d, n := range refs {
		if n <= 0 {
			zeros = append(zeros, d)
			delete(refs, d)
		}
	}
	return zeros
}

func (s *Store) writeBlock(digest string, data []byte) error {
	p := s.blockPath(digest)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return syncDir(filepath.Dir(p))
}

// verifyBlock returns nil if the block file for digest exists and matches.
func (s *Store) verifyBlock(digest string) error {
	data, err := os.ReadFile(s.blockPath(digest))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return ErrCorrupt
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
