// Package snapshot provides lightweight incremental snapshots and checkpoint
// archiving over a concurrent in-memory state store. Snapshot capture holds
// the store lock only long enough to copy the changed (dirty) entries; the
// expensive serialization happens after the lock is released, keeping pause
// times low. Snapshot files are encoded with the hand-rolled protobuf wire
// format from the wire and schema packages, so the whole pipeline has zero
// external dependencies.
package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"wiresnap/schema"
)

// Kind identifies whether a snapshot carries the full state or only the
// entries changed since the previous snapshot.
type Kind uint32

const (
	KindFull        Kind = 0
	KindIncremental Kind = 1
)

// Entry is one key/value mutation inside a snapshot.
type Entry struct {
	Key     string `protobuf:"bytes,1"`
	Value   uint64 `protobuf:"varint,2"`
	Deleted bool   `protobuf:"varint,3"`
}

// fileFormat is the on-disk layout of a snapshot file, decoded with the
// reflection schema engine.
type fileFormat struct {
	Seq     uint64  `protobuf:"varint,1"`
	Version uint64  `protobuf:"varint,2"`
	Kind    uint32  `protobuf:"varint,3"`
	Entries []Entry `protobuf:"message,4"`
}

// Store is a concurrency-safe versioned key/value state container. Every
// mutation bumps the version and records the key in a dirty set, which is
// what makes cheap incremental snapshots possible.
type Store struct {
	mu      sync.RWMutex
	data    map[string]uint64
	dirty   map[string]struct{}
	version uint64
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{
		data:  make(map[string]uint64),
		dirty: make(map[string]struct{}),
	}
}

// Set upserts a key and marks it dirty.
func (s *Store) Set(key string, value uint64) {
	s.mu.Lock()
	s.data[key] = value
	s.dirty[key] = struct{}{}
	s.version++
	s.mu.Unlock()
}

// Delete removes a key and records a tombstone in the dirty set.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		s.dirty[key] = struct{}{}
		s.version++
	}
	s.mu.Unlock()
}

// Get reads a single key.
func (s *Store) Get(key string) (uint64, bool) {
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	return v, ok
}

// Len reports the number of live keys.
func (s *Store) Len() int {
	s.mu.RLock()
	n := len(s.data)
	s.mu.RUnlock()
	return n
}

// Version reports the current mutation version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	v := s.version
	s.mu.RUnlock()
	return v
}

// captureFull copies every live entry and clears the dirty set. The lock is
// held only for the map copy, never for serialization.
func (s *Store) captureFull() (uint64, []Entry) {
	s.mu.Lock()
	entries := make([]Entry, 0, len(s.data))
	for k, v := range s.data {
		entries = append(entries, Entry{Key: k, Value: v})
	}
	s.dirty = make(map[string]struct{}, len(s.dirty))
	v := s.version
	s.mu.Unlock()
	return v, entries
}

// captureDelta copies only the entries mutated since the last capture and
// clears the dirty set.
func (s *Store) captureDelta() (uint64, []Entry) {
	s.mu.Lock()
	entries := make([]Entry, 0, len(s.dirty))
	for k := range s.dirty {
		if v, ok := s.data[k]; ok {
			entries = append(entries, Entry{Key: k, Value: v})
		} else {
			entries = append(entries, Entry{Key: k, Deleted: true})
		}
	}
	s.dirty = make(map[string]struct{}, len(s.dirty))
	v := s.version
	s.mu.Unlock()
	return v, entries
}

// reset clears all state; used when applying a full snapshot during restore.
func (s *Store) reset() {
	s.mu.Lock()
	s.data = make(map[string]uint64)
	s.dirty = make(map[string]struct{})
	s.mu.Unlock()
}

// apply replays snapshot entries and adopts the snapshot version.
func (s *Store) apply(entries []Entry, version uint64) {
	s.mu.Lock()
	for _, e := range entries {
		if e.Deleted {
			delete(s.data, e.Key)
		} else {
			s.data[e.Key] = e.Value
		}
	}
	s.version = version
	s.mu.Unlock()
}

// Snapshot is an immutable, already-captured view of (a delta of) the state.
type Snapshot struct {
	Seq     uint64
	Version uint64
	Kind    Kind
	Entries []Entry
}

// Encode serializes the snapshot with the reflection schema engine.
func (s *Snapshot) Encode() ([]byte, error) {
	f := fileFormat{
		Seq:     s.Seq,
		Version: s.Version,
		Kind:    uint32(s.Kind),
		Entries: s.Entries,
	}
	return schema.Marshal(f)
}

// DecodeSnapshot parses a snapshot previously produced by Encode.
func DecodeSnapshot(data []byte) (*Snapshot, error) {
	var f fileFormat
	if err := schema.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("snapshot: decode: %w", err)
	}
	kind := Kind(f.Kind)
	if kind != KindFull && kind != KindIncremental {
		return nil, fmt.Errorf("snapshot: unknown kind %d", f.Kind)
	}
	return &Snapshot{Seq: f.Seq, Version: f.Version, Kind: kind, Entries: f.Entries}, nil
}

// Snapshotter produces numbered snapshots from a Store. It is safe for
// concurrent use; sequence numbers are handed out atomically.
type Snapshotter struct {
	store *Store
	mu    sync.Mutex
	seq   uint64
}

// NewSnapshotter returns a Snapshotter bound to store.
func NewSnapshotter(store *Store) *Snapshotter {
	return &Snapshotter{store: store}
}

// Full captures every live entry.
func (s *Snapshotter) Full() *Snapshot {
	version, entries := s.store.captureFull()
	return s.finish(version, KindFull, entries)
}

// Incremental captures only the entries changed since the previous capture.
func (s *Snapshotter) Incremental() *Snapshot {
	version, entries := s.store.captureDelta()
	return s.finish(version, KindIncremental, entries)
}

func (s *Snapshotter) finish(version uint64, kind Kind, entries []Entry) *Snapshot {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return &Snapshot{Seq: seq, Version: version, Kind: kind, Entries: entries}
}

// CheckpointInfo describes one archived snapshot file.
type CheckpointInfo struct {
	Seq     uint64
	Kind    Kind
	Version uint64
	Path    string
}

// Archive persists snapshots as checkpoint files in a directory. Writes are
// atomic (temp file + rename) and serialized with a mutex.
type Archive struct {
	dir string
	mu  sync.Mutex
}

// OpenArchive creates (if needed) and opens the checkpoint directory.
func OpenArchive(dir string) (*Archive, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("snapshot: open archive: %w", err)
	}
	return &Archive{dir: dir}, nil
}

func (k Kind) suffix() string {
	if k == KindFull {
		return "full"
	}
	return "inc"
}

// Append encodes snap and stores it as a new checkpoint file.
func (a *Archive) Append(snap *Snapshot) (string, error) {
	data, err := snap.Encode()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("snap-%06d-%s.snap", snap.Seq, snap.Kind.suffix())
	path := filepath.Join(a.dir, name)
	a.mu.Lock()
	defer a.mu.Unlock()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("snapshot: write checkpoint: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("snapshot: commit checkpoint: %w", err)
	}
	return path, nil
}

// Checkpoints lists archived snapshots in sequence order.
func (a *Archive) Checkpoints() ([]CheckpointInfo, error) {
	matches, err := filepath.Glob(filepath.Join(a.dir, "snap-*.snap"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	infos := make([]CheckpointInfo, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot: read checkpoint %s: %w", path, err)
		}
		snap, err := DecodeSnapshot(data)
		if err != nil {
			return nil, fmt.Errorf("snapshot: parse checkpoint %s: %w", path, err)
		}
		infos = append(infos, CheckpointInfo{
			Seq:     snap.Seq,
			Kind:    snap.Kind,
			Version: snap.Version,
			Path:    path,
		})
	}
	return infos, nil
}

// Restore replays every archived checkpoint into store, in sequence order.
// A full snapshot resets the store first; incrementals apply on top. The
// result is exactly the state at the newest checkpoint.
func (a *Archive) Restore(store *Store) error {
	matches, err := filepath.Glob(filepath.Join(a.dir, "snap-*.snap"))
	if err != nil {
		return err
	}
	sort.Strings(matches)
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("snapshot: read checkpoint %s: %w", path, err)
		}
		snap, err := DecodeSnapshot(data)
		if err != nil {
			return fmt.Errorf("snapshot: parse checkpoint %s: %w", path, err)
		}
		if snap.Kind == KindFull {
			store.reset()
		}
		store.apply(snap.Entries, snap.Version)
	}
	return nil
}
