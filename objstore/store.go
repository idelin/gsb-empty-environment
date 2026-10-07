// Package objstore implements a small persistent object library.
//
// Objects are immutable blobs with outgoing references to other objects.
// Named roots designate entry points; one object may be referenced by any
// number of roots. Transactions apply object puts and root changes to
// multiple roots atomically: a commit is a single fsynced WAL record.
//
// Reclamation uses a tri-color (white/grey/black) mark-sweep collector that
// runs concurrently with transactions. An insertion write barrier (Dijkstra
// style) shades every object that becomes referenced while marking is in
// progress, so the concurrent sweeper can never reclaim a live object.
package objstore

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

// ID uniquely identifies an object.
type ID string

// Object is an immutable blob plus references to other objects.
type Object struct {
	ID   ID     `json:"id"`
	Data []byte `json:"data"`
	Refs []ID   `json:"refs"`
}

// ErrClosed is returned by operations on a closed store.
var ErrClosed = errors.New("objstore: store is closed")

// ErrTxDone is returned when a transaction is used after Commit/Rollback.
var ErrTxDone = errors.New("objstore: transaction already finished")

const (
	walFileName  = "wal.log"
	snapFileName = "snapshot.json"
	snapTmpName  = "snapshot.json.tmp"
)

// Store is a persistent object store. It is safe for concurrent use.
type Store struct {
	dir string

	mu      sync.Mutex
	objects map[ID]*Object
	roots   map[string]ID
	wal     *os.File
	closed  bool

	gcMu sync.Mutex // serializes GC cycles
	gc   *gcState   // non-nil while a mark phase is active

	// markHook, when non-nil, is invoked by GC without locks held.
	// drained=true means the grey worklist was observed empty and the
	// collector is about to re-check it before sweeping. Test-only hook.
	markHook func(drained bool)
}

// Open opens (or creates) the store in dir, recovering committed state from
// the snapshot and WAL. A partially written trailing WAL record left by a
// crash is discarded.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:     dir,
		objects: make(map[ID]*Object),
		roots:   make(map[string]ID),
	}
	if err := s.loadSnapshot(); err != nil {
		return nil, err
	}
	walPath := filepath.Join(dir, walFileName)
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	valid, err := replayWAL(f, func(rec *walRecord) { s.applyRecordLocked(rec) })
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi, err := f.Stat(); err != nil {
		f.Close()
		return nil, err
	} else if fi.Size() > valid {
		if err := f.Truncate(valid); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	s.wal = f
	return s, nil
}

// Close flushes and closes the WAL. The store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.wal.Close()
}

// loadSnapshot reads snapshot.json if present.
func (s *Store) loadSnapshot() error {
	data, err := ioutil.ReadFile(filepath.Join(s.dir, snapFileName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap diskSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("objstore: corrupt snapshot: %v", err)
	}
	if snap.Objects != nil {
		s.objects = snap.Objects
	}
	if snap.Roots != nil {
		s.roots = snap.Roots
	}
	return nil
}

type diskSnapshot struct {
	Objects map[ID]*Object `json:"objects"`
	Roots   map[string]ID  `json:"roots"`
}

// applyRecordLocked applies a WAL record to in-memory state.
// Callers must hold s.mu.
func (s *Store) applyRecordLocked(rec *walRecord) {
	for _, o := range rec.Puts {
		cp := *o
		s.objects[cp.ID] = &cp
	}
	for name, id := range rec.SetRoots {
		s.roots[name] = id
	}
	for _, name := range rec.DelRoots {
		delete(s.roots, name)
	}
	for _, id := range rec.Dels {
		delete(s.objects, id)
	}
}

// Get returns a copy of the object with the given ID.
func (s *Store) Get(id ID) (*Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok {
		return nil, false
	}
	cp := *o
	cp.Data = append([]byte(nil), o.Data...)
	cp.Refs = append([]ID(nil), o.Refs...)
	return &cp, true
}

// Root returns the object ID bound to the named root.
func (s *Store) Root(name string) (ID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.roots[name]
	return id, ok
}

// Roots returns a snapshot of all root bindings.
func (s *Store) Roots() map[string]ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ID, len(s.roots))
	for k, v := range s.roots {
		out[k] = v
	}
	return out
}

// Resolve returns the object bound to the named root.
func (s *Store) Resolve(name string) (*Object, bool) {
	s.mu.Lock()
	id, ok := s.roots[name]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	return s.Get(id)
}

// Snapshot persists a compacted image of the current state and truncates
// the WAL. The snapshot file is replaced atomically via rename.
func (s *Store) Snapshot() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	data, err := json.Marshal(diskSnapshot{Objects: s.objects, Roots: s.roots})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, snapTmpName)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
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
	if err := os.Rename(tmp, filepath.Join(s.dir, snapFileName)); err != nil {
		return err
	}
	syncDir(s.dir)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.wal.Close()
	nf, err := os.OpenFile(filepath.Join(s.dir, walFileName), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	s.wal = nf
	syncDir(s.dir)
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// newID derives a unique object ID from a random nonce and the content.
func newID(data []byte, refs []ID) ID {
	h := sha1.New()
	var nonce [16]byte
	rand.Read(nonce[:])
	h.Write(nonce[:])
	h.Write(data)
	for _, r := range refs {
		h.Write([]byte(r))
	}
	return ID(hex.EncodeToString(h.Sum(nil)))
}

// Tx is an atomic multi-root transaction. Objects are created and roots are
// changed in memory, then applied (and persisted) as one indivisible unit by
// Commit. A Tx is not safe for concurrent use.
type Tx struct {
	s        *Store
	puts     []*Object
	setRoots map[string]ID
	delRoots []string
	done     bool
}

// Begin starts a new transaction.
func (s *Store) Begin() *Tx {
	return &Tx{s: s, setRoots: make(map[string]ID)}
}

// Put stages a new object and returns its ID. Refs must point to objects
// that exist in the store or are staged earlier in the same transaction;
// this is validated at Commit.
func (t *Tx) Put(data []byte, refs []ID) ID {
	id := newID(data, refs)
	cp := append([]byte(nil), data...)
	rp := append([]ID(nil), refs...)
	t.puts = append(t.puts, &Object{ID: id, Data: cp, Refs: rp})
	return id
}

// SetRoot stages binding the named root to id.
func (t *Tx) SetRoot(name string, id ID) {
	t.setRoots[name] = id
}

// DeleteRoot stages removal of the named root.
func (t *Tx) DeleteRoot(name string) {
	t.delRoots = append(t.delRoots, name)
	delete(t.setRoots, name)
}

// Commit validates, persists (single fsynced WAL record) and applies all
// staged changes atomically. While a GC mark phase is active, the insertion
// write barrier shades every newly referenced object so the concurrent
// sweeper cannot reclaim it.
func (t *Tx) Commit() error {
	s := t.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if t.done {
		return ErrTxDone
	}
	t.done = true

	staged := make(map[ID]bool, len(t.puts))
	for _, o := range t.puts {
		staged[o.ID] = true
	}
	exists := func(id ID) bool {
		if staged[id] {
			return true
		}
		_, ok := s.objects[id]
		return ok
	}
	for _, o := range t.puts {
		for _, r := range o.Refs {
			if !exists(r) {
				return fmt.Errorf("objstore: object %s has dangling ref %s", o.ID, r)
			}
		}
	}
	for name, id := range t.setRoots {
		if !exists(id) {
			return fmt.Errorf("objstore: root %q targets missing object %s", name, id)
		}
	}

	rec := &walRecord{Puts: t.puts, SetRoots: t.setRoots, DelRoots: t.delRoots}
	if err := appendWALRecord(s.wal, rec); err != nil {
		return err
	}
	s.applyRecordLocked(rec)

	// Insertion write barrier: anything that became reachable (or was
	// created) during the mark phase is shaded grey so the sweeper,
	// which only reclaims white objects, cannot delete it.
	if s.gc != nil {
		for _, o := range t.puts {
			s.shadeLocked(o.ID)
		}
		for _, id := range t.setRoots {
			s.shadeLocked(id)
		}
	}
	return nil
}

// Rollback discards all staged changes.
func (t *Tx) Rollback() {
	t.done = true
}
