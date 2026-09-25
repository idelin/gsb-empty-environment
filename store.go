// Package chunkstore implements a content-addressed chunk store.
//
// Large values are split into fixed-size chunks. Each chunk is stored once
// under chunks/<hex[0:2]>/<sha256-hex>, so identical content shared by
// multiple values is stored a single time. A metadata file (meta.json)
// tracks, per key, the ordered chunk hashes and, per chunk, a reference
// count. All metadata mutations are committed as an atomic transaction
// (write temp file, fsync, rename, fsync directory).
//
// Reclaiming space never requires scanning the data directory: overwrite,
// delete, and rollback paths only touch chunks whose reference count drops
// to zero, which the metadata knows directly.
//
// Crash-recovery and power-loss boundaries are documented in README.md.
package chunkstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// DefaultChunkSize is used when Options.ChunkSize is zero.
const DefaultChunkSize = 4 << 20 // 4 MiB

// Hooks are fault-injection points used by tests to simulate failures.
// All hooks run while the store lock is held; they must not call back
// into the Store.
type Hooks struct {
	// AfterChunkWrite, if non-nil, runs after the n-th chunk (1-based) of a
	// Put has been durably written. Returning an error aborts the Put and
	// triggers rollback of the chunks created by this Put.
	AfterChunkWrite func(n int, hash string) error

	// BeforeMetaCommit, if non-nil, runs after all chunks of a Put/Delete
	// are written, just before the metadata transaction is committed.
	// Returning an error aborts the operation and triggers rollback.
	BeforeMetaCommit func() error
}

// Options configures a Store.
type Options struct {
	// ChunkSize in bytes. Zero means DefaultChunkSize.
	ChunkSize int
	// Hooks are optional fault-injection points.
	Hooks Hooks
}

// Store is a content-addressed chunk store rooted at a caller-chosen dir.
type Store struct {
	dir       string
	chunkSize int
	hooks     Hooks

	mu   sync.Mutex
	meta metadata
}

type keyMeta struct {
	Chunks []string `json:"chunks"`
	Size   int64    `json:"size"`
}

type metadata struct {
	Keys map[string]keyMeta `json:"keys"`
	Refs map[string]int     `json:"refs"`
}

// Open opens (or creates) a store rooted at dir. It recovers from a crash
// that happened between the metadata temp-file write and the atomic rename
// by discarding any stale temp file; the last committed meta.json is the
// only source of truth.
func Open(dir string, opts Options) (*Store, error) {
	if opts.ChunkSize < 0 {
		return nil, fmt.Errorf("chunkstore: negative chunk size %d", opts.ChunkSize)
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		dir:       dir,
		chunkSize: opts.ChunkSize,
		hooks:     opts.Hooks,
		meta: metadata{
			Keys: map[string]keyMeta{},
			Refs: map[string]int{},
		},
	}

	metaPath := filepath.Join(dir, "meta.json")
	tmpPath := metaPath + ".tmp"

	// Crash recovery: a leftover temp file means a commit was interrupted
	// before the rename. The committed meta.json (if any) still represents
	// a consistent state, so the temp file is simply discarded.
	if _, err := os.Stat(tmpPath); err == nil {
		if err := os.Remove(tmpPath); err != nil {
			return nil, fmt.Errorf("chunkstore: removing stale temp meta: %w", err)
		}
	}

	data, err := os.ReadFile(metaPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &s.meta); err != nil {
			return nil, fmt.Errorf("chunkstore: corrupt meta.json: %w", err)
		}
		if s.meta.Keys == nil {
			s.meta.Keys = map[string]keyMeta{}
		}
		if s.meta.Refs == nil {
			s.meta.Refs = map[string]int{}
		}
	case errors.Is(err, os.ErrNotExist):
		// Fresh store.
	default:
		return nil, err
	}
	return s, nil
}

// Put stores the content read from r under key, replacing any existing
// value. Content is deduplicated by chunk hash across all keys. If Put
// fails partway, every chunk it created is rolled back; chunks that
// already existed (shared with other keys) are never touched.
func (s *Store) Put(key string, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var hashes []string
	var created []string // chunks this Put created; removed on rollback
	var size int64

	rollback := func() {
		for _, h := range created {
			// These chunks were created by this Put and the metadata was
			// never committed, so nothing can reference them.
			os.Remove(s.chunkPath(h))
		}
		s.fsyncDir(filepath.Join(s.dir, "chunks"))
	}

	buf := make([]byte, s.chunkSize)
	for n := 1; ; n++ {
		nr, err := io.ReadFull(r, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			rollback()
			return err
		}
		if nr == 0 {
			break // clean EOF: empty value or exact chunk-size boundary
		}
		sum := sha256.Sum256(buf[:nr])
		h := hex.EncodeToString(sum[:])
		isNew, werr := s.writeChunk(h, buf[:nr])
		if werr != nil {
			rollback()
			return werr
		}
		if isNew {
			created = append(created, h)
		}
		if s.hooks.AfterChunkWrite != nil {
			if herr := s.hooks.AfterChunkWrite(n, h); herr != nil {
				rollback()
				return herr
			}
		}
		hashes = append(hashes, h)
		size += int64(nr)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
	}

	if s.hooks.BeforeMetaCommit != nil {
		if err := s.hooks.BeforeMetaCommit(); err != nil {
			rollback()
			return err
		}
	}

	// Build the next metadata state. Replaced chunks of an overwritten key
	// have their refcounts decremented; chunks reaching zero are collected
	// for deletion after the commit.
	next := s.meta.clone()
	for _, h := range hashes {
		next.Refs[h]++
	}
	next.Keys[key] = keyMeta{Chunks: hashes, Size: size}
	dead := next.unrefOld(&s.meta, key)

	if err := s.commitMeta(next); err != nil {
		rollback()
		return err
	}
	s.meta = *next
	s.removeChunks(dead)
	return nil
}

// PutBytes is a convenience wrapper around Put.
func (s *Store) PutBytes(key string, data []byte) error {
	return s.Put(key, bytesReader(data))
}

// Get reads the value stored under key, verifying the SHA-256 of every
// chunk against its content-addressed name. Any corruption is reported.
func (s *Store) Get(key string) ([]byte, error) {
	s.mu.Lock()
	km, ok := s.meta.Keys[key]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("chunkstore: key %q not found", key)
	}

	out := make([]byte, 0, km.Size)
	for _, h := range km.Chunks {
		data, err := os.ReadFile(s.chunkPath(h))
		if err != nil {
			return nil, fmt.Errorf("chunkstore: reading chunk %s: %w", h, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != h {
			return nil, fmt.Errorf("chunkstore: checksum mismatch for chunk %s", h)
		}
		out = append(out, data...)
	}
	if int64(len(out)) != km.Size {
		return nil, fmt.Errorf("chunkstore: size mismatch for key %q: meta=%d actual=%d", key, km.Size, len(out))
	}
	return out, nil
}

// Delete removes key and reclaims exactly the chunks whose reference count
// drops to zero. Chunks shared with other keys are kept.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.meta.Keys[key]; !ok {
		return fmt.Errorf("chunkstore: key %q not found", key)
	}
	next := s.meta.clone()
	delete(next.Keys, key)
	dead := next.unrefOld(&s.meta, key)
	if err := s.commitMeta(next); err != nil {
		return err
	}
	s.meta = *next
	s.removeChunks(dead)
	return nil
}

// Stats reports the number of keys and live chunks, for demos and tests.
func (s *Store) Stats() (keys, chunks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.meta.Keys), len(s.meta.Refs)
}

// unrefOld decrements refcounts for chunks of key's previous value in next
// (relative to prev) and returns the hashes whose count reached zero.
func (m *metadata) unrefOld(prev *metadata, key string) []string {
	old, ok := prev.Keys[key]
	if !ok {
		return nil
	}
	var dead []string
	for _, h := range old.Chunks {
		m.Refs[h]--
		if m.Refs[h] == 0 {
			delete(m.Refs, h)
			dead = append(dead, h)
		}
	}
	return dead
}

func (m *metadata) clone() *metadata {
	n := &metadata{
		Keys: make(map[string]keyMeta, len(m.Keys)),
		Refs: make(map[string]int, len(m.Refs)),
	}
	for k, v := range m.Keys {
		chunks := make([]string, len(v.Chunks))
		copy(chunks, v.Chunks)
		n.Keys[k] = keyMeta{Chunks: chunks, Size: v.Size}
	}
	for k, v := range m.Refs {
		n.Refs[k] = v
	}
	return n
}

// commitMeta atomically replaces meta.json: write temp, fsync, rename,
// fsync the directory so the rename itself is durable.
func (s *Store) commitMeta(m *metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	metaPath := filepath.Join(s.dir, "meta.json")
	tmpPath := metaPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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
	if err := os.Rename(tmpPath, metaPath); err != nil {
		return err
	}
	return s.fsyncDir(s.dir)
}

// writeChunk stores data under its content hash if not already present.
// It reports whether this call created the chunk file. The write is
// temp-file + fsync + rename, so readers never observe a partial chunk,
// and the chunks directory is fsynced before returning.
func (s *Store) writeChunk(hash string, data []byte) (bool, error) {
	path := s.chunkPath(hash)
	if _, err := os.Stat(path); err == nil {
		return false, nil // already stored (shared content)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := s.fsyncDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	return true, nil
}

// removeChunks deletes unreferenced chunk files. Failure to remove only
// leaks space, never correctness, so errors are ignored by design.
func (s *Store) removeChunks(hashes []string) {
	for _, h := range hashes {
		os.Remove(s.chunkPath(h))
	}
	if len(hashes) > 0 {
		s.fsyncDir(filepath.Join(s.dir, "chunks"))
	}
}

func (s *Store) chunkPath(hash string) string {
	return filepath.Join(s.dir, "chunks", hash[:2], hash)
}

func (s *Store) fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func bytesReader(b []byte) io.Reader { return &byteSliceReader{b: b} }

type byteSliceReader struct {
	b   []byte
	off int
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}
