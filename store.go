// Package gcstore implements a segment-based block store with snapshot
// roots and copying garbage collection.
//
// Blocks are content-addressed by caller-supplied IDs and may link to
// other blocks, forming a graph. Snapshots pin root block IDs. Garbage
// collection marks every block reachable from any snapshot root, copies
// the live blocks into a fresh segment (duplicate blocks are copied
// exactly once), persists the new block-to-segment mapping, and only
// then deletes the old segments. If the device runs out of space while
// copying, the collection aborts and the old segments and mapping are
// left untouched.
package gcstore

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// ErrNotFound is returned by Get when no live mapping exists for an ID.
	ErrNotFound = errors.New("gcstore: block not found")
	// ErrNoSpace is returned when a write or GC copy would exceed MaxBytes.
	ErrNoSpace = errors.New("gcstore: insufficient space")
)

// Block is a unit of stored data. Links lists IDs of other blocks this
// block references; links are followed during GC reachability marking.
type Block struct {
	ID    string
	Links []string
	Data  []byte
}

// blockRef locates a block inside a segment file.
type blockRef struct {
	Segment string `json:"segment"`
	Offset  int64  `json:"offset"`
}

// manifest is the persisted state: the block mapping and snapshot roots.
// It is written atomically (temp file + rename + directory fsync).
type manifest struct {
	Mapping   map[string]blockRef `json:"mapping"`
	Snapshots map[string][]string `json:"snapshots"`
}

// Store is a block store rooted at a directory on disk.
type Store struct {
	dir      string
	maxBytes int64

	mu        sync.Mutex
	mapping   map[string]blockRef
	snapshots map[string][]string

	active     *os.File
	activeName string
	activeOff  int64
	segSeq     int

	// Test hooks invoked inside GC around the persistence cutpoint.
	// A non-nil error simulates a crash at that exact point.
	beforeMappingPersist func() error
	afterMappingPersist  func() error
}

// Open opens (or creates) the store in dir. maxBytes caps the total size
// of segment files; 0 means unlimited. Open recovers the persisted
// mapping and snapshot roots, and removes partial segments left behind
// by a GC that crashed before its persistence cutpoint.
func Open(dir string, maxBytes int64) (*Store, error) {
	s := &Store{
		dir:       dir,
		maxBytes:  maxBytes,
		mapping:   make(map[string]blockRef),
		snapshots: make(map[string][]string),
	}
	if err := os.MkdirAll(s.segmentsDir(), 0755); err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(s.manifestPath())
	switch {
	case err == nil:
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("gcstore: corrupt manifest: %v", err)
		}
		if m.Mapping != nil {
			s.mapping = m.Mapping
		}
		if m.Snapshots != nil {
			s.snapshots = m.Snapshots
		}
	case os.IsNotExist(err):
	default:
		return nil, err
	}

	entries, err := ioutil.ReadDir(s.segmentsDir())
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		var seq int
		switch {
		case strings.HasPrefix(name, "gc-"):
			// A gc-* file is never referenced by a persisted manifest:
			// it belongs to a GC that crashed before its cutpoint.
			os.Remove(filepath.Join(s.segmentsDir(), name))
			fmt.Sscanf(name, "gc-%d.dat", &seq)
		case strings.HasPrefix(name, "seg-"):
			fmt.Sscanf(name, "seg-%d.dat", &seq)
		}
		if seq > s.segSeq {
			s.segSeq = seq
		}
	}

	if err := s.openActiveLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// Put appends a block to the active segment and updates the in-memory
// mapping. The mapping is persisted by Flush, Close, Snapshot or GC.
func (s *Store) Put(b Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := encodeRecord(b)
	if s.maxBytes > 0 && s.usageLocked()+int64(len(rec)) > s.maxBytes {
		return ErrNoSpace
	}
	off := s.activeOff
	if _, err := s.active.Write(rec); err != nil {
		return err
	}
	s.activeOff += int64(len(rec))
	s.mapping[b.ID] = blockRef{Segment: s.activeName, Offset: off}
	return nil
}

// Get returns the block with the given ID.
func (s *Store) Get(id string) (Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

// Snapshot records a named set of root block IDs and persists it.
func (s *Store) Snapshot(name string, roots []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[name] = append([]string(nil), roots...)
	return s.persistManifestLocked()
}

// DropSnapshot removes a named snapshot and persists the change.
func (s *Store) DropSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, name)
	return s.persistManifestLocked()
}

// Roots returns the root block IDs of a snapshot.
func (s *Store) Roots(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.snapshots[name]...)
}

// Flush syncs the active segment and persists the manifest.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		if err := s.active.Sync(); err != nil {
			return err
		}
	}
	return s.persistManifestLocked()
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.Sync()
		s.active.Close()
		s.active = nil
	}
	return s.persistManifestLocked()
}

func (s *Store) getLocked(id string) (Block, error) {
	ref, ok := s.mapping[id]
	if !ok {
		return Block{}, ErrNotFound
	}
	f, err := os.Open(filepath.Join(s.segmentsDir(), ref.Segment))
	if err != nil {
		return Block{}, err
	}
	defer f.Close()
	if _, err := f.Seek(ref.Offset, io.SeekStart); err != nil {
		return Block{}, err
	}
	return decodeRecord(f)
}

func (s *Store) segmentsDir() string  { return filepath.Join(s.dir, "segments") }
func (s *Store) manifestPath() string { return filepath.Join(s.dir, "manifest.json") }

func segmentName(seq int) string { return fmt.Sprintf("seg-%06d.dat", seq) }
func gcName(seq int) string      { return fmt.Sprintf("gc-%06d.dat", seq) }

func (s *Store) openActiveLocked() error {
	s.segSeq++
	name := segmentName(s.segSeq)
	f, err := os.Create(filepath.Join(s.segmentsDir(), name))
	if err != nil {
		return err
	}
	s.active = f
	s.activeName = name
	s.activeOff = 0
	return nil
}

// usageLocked sums the sizes of all segment files (live and in-progress GC).
func (s *Store) usageLocked() int64 {
	entries, err := ioutil.ReadDir(s.segmentsDir())
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "seg-") || strings.HasPrefix(e.Name(), "gc-") {
			total += e.Size()
		}
	}
	return total
}

// persistManifestLocked atomically writes the manifest: temp file, fsync,
// rename, directory fsync.
func (s *Store) persistManifestLocked() error {
	data, err := json.Marshal(manifest{Mapping: s.mapping, Snapshots: s.snapshots})
	if err != nil {
		return err
	}
	tmp := s.manifestPath() + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.manifestPath()); err != nil {
		return err
	}
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Record encoding: idLen|id|nLinks|(linkLen|link)*|dataLen|data, all
// lengths uint32 big-endian.
func encodeRecord(b Block) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(len(b.ID)))
	buf.WriteString(b.ID)
	binary.Write(&buf, binary.BigEndian, uint32(len(b.Links)))
	for _, l := range b.Links {
		binary.Write(&buf, binary.BigEndian, uint32(len(l)))
		buf.WriteString(l)
	}
	binary.Write(&buf, binary.BigEndian, uint32(len(b.Data)))
	buf.Write(b.Data)
	return buf.Bytes()
}

func readBytes(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func decodeRecord(r io.Reader) (Block, error) {
	id, err := readBytes(r)
	if err != nil {
		return Block{}, err
	}
	var nLinks uint32
	if err := binary.Read(r, binary.BigEndian, &nLinks); err != nil {
		return Block{}, err
	}
	links := make([]string, 0, nLinks)
	for i := uint32(0); i < nLinks; i++ {
		l, err := readBytes(r)
		if err != nil {
			return Block{}, err
		}
		links = append(links, string(l))
	}
	data, err := readBytes(r)
	if err != nil {
		return Block{}, err
	}
	return Block{ID: string(id), Links: links, Data: data}, nil
}
