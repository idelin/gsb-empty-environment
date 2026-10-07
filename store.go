// Package gcstore implements a content-addressed block store with
// snapshot-based garbage collection. Blocks are appended to segment
// files; a hash->location mapping and the snapshot table are persisted
// atomically. GC marks live blocks from every snapshot root, copies
// them into a fresh segment (deduplicating repeated blocks), persists
// the new mapping as a cut point, and only then deletes old segments.
package gcstore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Hash identifies a block by the SHA-256 of its references and data.
type Hash [32]byte

// Block is a unit of storage. Refs point to child blocks, forming a
// graph whose roots are held by snapshots.
type Block struct {
	Refs []Hash
	Data []byte
}

// Location is the physical position of a block record.
type Location struct {
	Seg uint64 `json:"seg"`
	Off int64  `json:"off"`
	Len uint32 `json:"len"`
}

var (
	ErrNotFound = errors.New("gcstore: block not found")
	ErrNoSpace  = errors.New("gcstore: insufficient space")
	ErrCorrupt  = errors.New("gcstore: corrupt store")
)

func hashBlock(refs []Hash, data []byte) Hash {
	h := sha256.New()
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(refs)))
	h.Write(tmp[:])
	for _, r := range refs {
		h.Write(r[:])
	}
	binary.BigEndian.PutUint32(tmp[:], uint32(len(data)))
	h.Write(tmp[:])
	h.Write(data)
	var sum Hash
	copy(sum[:], h.Sum(nil))
	return sum
}

func encodeRecord(b *Block) []byte {
	buf := make([]byte, 0, 8+len(b.Refs)*32+len(b.Data))
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(b.Refs)))
	buf = append(buf, tmp[:]...)
	for _, r := range b.Refs {
		buf = append(buf, r[:]...)
	}
	binary.BigEndian.PutUint32(tmp[:], uint32(len(b.Data)))
	buf = append(buf, tmp[:]...)
	return append(buf, b.Data...)
}

func decodeRecord(raw []byte) (*Block, error) {
	if len(raw) < 4 {
		return nil, ErrCorrupt
	}
	n := binary.BigEndian.Uint32(raw)
	raw = raw[4:]
	if uint64(len(raw)) < uint64(n)*32+4 {
		return nil, ErrCorrupt
	}
	refs := make([]Hash, n)
	for i := range refs {
		copy(refs[i][:], raw[i*32:(i+1)*32])
	}
	raw = raw[n*32:]
	dl := binary.BigEndian.Uint32(raw)
	raw = raw[4:]
	if uint64(len(raw)) != uint64(dl) {
		return nil, ErrCorrupt
	}
	data := make([]byte, dl)
	copy(data, raw)
	return &Block{Refs: refs, Data: data}, nil
}

func parseHash(s string) (Hash, error) {
	var h Hash
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return h, ErrCorrupt
	}
	copy(h[:], raw)
	return h, nil
}

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

type diskMapping struct {
	Entries map[string]Location `json:"entries"`
	NextSeg uint64              `json:"next_seg"`
}

// Store is a block store backed by segment files in a directory.
type Store struct {
	dir      string
	maxBytes int64 // 0 means unlimited

	mu        sync.Mutex
	mapping   map[Hash]Location
	snapshots map[string]Hash
	nextSeg   uint64

	active    *os.File
	activeID  uint64
	activeOff int64
}

// Open loads (or creates) a store in dir. maxBytes caps the total size
// of segment files; 0 means unlimited. Open performs crash recovery:
// segment files not referenced by the persisted mapping (e.g. left
// behind by a GC that crashed before its cut point) are removed.
func Open(dir string, maxBytes int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		maxBytes:  maxBytes,
		mapping:   make(map[Hash]Location),
		snapshots: make(map[string]Hash),
		nextSeg:   1,
	}
	raw, err := ioutil.ReadFile(filepath.Join(dir, "mapping.json"))
	switch {
	case err == nil:
		var dm diskMapping
		if err := json.Unmarshal(raw, &dm); err != nil {
			return nil, err
		}
		for k, loc := range dm.Entries {
			h, err := parseHash(k)
			if err != nil {
				return nil, err
			}
			s.mapping[h] = loc
		}
		if dm.NextSeg > s.nextSeg {
			s.nextSeg = dm.NextSeg
		}
	case !os.IsNotExist(err):
		return nil, err
	}
	raw, err = ioutil.ReadFile(filepath.Join(dir, "snapshots.json"))
	switch {
	case err == nil:
		var tbl map[string]string
		if err := json.Unmarshal(raw, &tbl); err != nil {
			return nil, err
		}
		for name, hexRoot := range tbl {
			root, err := parseHash(hexRoot)
			if err != nil {
				return nil, err
			}
			s.snapshots[name] = root
		}
	case !os.IsNotExist(err):
		return nil, err
	}
	if err := s.recoverLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// recoverLocked deletes orphan segment files and stale temp files.
func (s *Store) recoverLocked() error {
	referenced := make(map[uint64]bool)
	for _, loc := range s.mapping {
		referenced[loc.Seg] = true
	}
	files, err := ioutil.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, fi := range files {
		name := fi.Name()
		if strings.HasSuffix(name, ".tmp") {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
				return err
			}
			continue
		}
		id, ok := parseSegName(name)
		if ok && !referenced[id] {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseSegName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "seg-") || !strings.HasSuffix(name, ".seg") {
		return 0, false
	}
	id, err := strconv.ParseUint(name[4:len(name)-4], 10, 64)
	return id, err == nil
}

func (s *Store) segPath(id uint64) string {
	return filepath.Join(s.dir, fmt.Sprintf("seg-%06d.seg", id))
}

func (s *Store) segmentIDsLocked() ([]uint64, error) {
	files, err := ioutil.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, fi := range files {
		if id, ok := parseSegName(fi.Name()); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *Store) dirBytesLocked() (int64, error) {
	files, err := ioutil.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, fi := range files {
		if _, ok := parseSegName(fi.Name()); ok {
			total += fi.Size()
		}
	}
	return total, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func atomicWriteJSON(path string, v interface{}) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func (s *Store) persistMappingLocked() error {
	dm := diskMapping{Entries: make(map[string]Location, len(s.mapping)), NextSeg: s.nextSeg}
	for h, loc := range s.mapping {
		dm.Entries[h.String()] = loc
	}
	return atomicWriteJSON(filepath.Join(s.dir, "mapping.json"), dm)
}

func (s *Store) persistSnapshotsLocked() error {
	tbl := make(map[string]string, len(s.snapshots))
	for name, root := range s.snapshots {
		tbl[name] = root.String()
	}
	return atomicWriteJSON(filepath.Join(s.dir, "snapshots.json"), tbl)
}

func (s *Store) ensureActiveLocked() error {
	if s.active != nil {
		return nil
	}
	id := s.nextSeg
	s.nextSeg++
	f, err := os.OpenFile(s.segPath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	s.active = f
	s.activeID = id
	s.activeOff = 0
	return s.persistMappingLocked()
}

// Put appends a block and returns its hash. Writing identical content
// twice creates two physical copies; GC deduplicates them.
func (s *Store) Put(refs []Hash, data []byte) (Hash, error) {
	b := &Block{Refs: refs, Data: data}
	h := hashBlock(refs, data)
	rec := encodeRecord(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxBytes > 0 {
		used, err := s.dirBytesLocked()
		if err != nil {
			return Hash{}, err
		}
		if used+int64(len(rec)) > s.maxBytes {
			return Hash{}, ErrNoSpace
		}
	}
	if err := s.ensureActiveLocked(); err != nil {
		return Hash{}, err
	}
	off := s.activeOff
	if _, err := s.active.Write(rec); err != nil {
		return Hash{}, err
	}
	if err := s.active.Sync(); err != nil {
		return Hash{}, err
	}
	s.activeOff += int64(len(rec))
	s.mapping[h] = Location{Seg: s.activeID, Off: off, Len: uint32(len(rec))}
	if err := s.persistMappingLocked(); err != nil {
		return Hash{}, err
	}
	return h, nil
}

// Get reads the block with the given hash.
func (s *Store) Get(h Hash) (*Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, ok := s.mapping[h]
	if !ok {
		return nil, ErrNotFound
	}
	return s.readAtLocked(loc)
}

// Has reports whether the block is present in the mapping.
func (s *Store) Has(h Hash) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.mapping[h]
	return ok
}

func (s *Store) readAtLocked(loc Location) (*Block, error) {
	f, err := os.Open(s.segPath(loc.Seg))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw := make([]byte, loc.Len)
	if _, err := f.ReadAt(raw, loc.Off); err != nil {
		return nil, err
	}
	return decodeRecord(raw)
}

// Snapshot records root as a GC root under name, replacing any
// previous root for that name.
func (s *Store) Snapshot(name string, root Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mapping[root]; !ok {
		return ErrNotFound
	}
	s.snapshots[name] = root
	return s.persistSnapshotsLocked()
}

// DeleteSnapshot removes a GC root, potentially making its graph
// collectable.
func (s *Store) DeleteSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, name)
	return s.persistSnapshotsLocked()
}

// Root returns the root hash of a snapshot.
func (s *Store) Root(name string) (Hash, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.snapshots[name]
	return h, ok
}

// Rotate seals the active segment so subsequent writes go to a new
// segment and the sealed one becomes a GC candidate.
func (s *Store) Rotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		if err := s.active.Close(); err != nil {
			return err
		}
		s.active = nil
	}
	return nil
}

// Close releases the active segment handle.
func (s *Store) Close() error {
	return s.Rotate()
}
