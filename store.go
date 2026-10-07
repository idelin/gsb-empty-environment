// Package objstore implements a durable object library where objects may be
// referenced by multiple named roots. Transactions atomically mutate any
// number of roots and objects, and a concurrent tri-color garbage collector
// reclaims unreachable objects. An insertion write barrier guarantees that
// references created while a GC cycle is in flight enter the retained set
// and are never removed by the concurrent sweeper.
package objstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Object is a node in the object graph. Refs holds the IDs of other objects
// this object points to; the GC treats them as strong edges.
type Object struct {
	ID   string   `json:"id"`
	Data []byte   `json:"data,omitempty"`
	Refs []string `json:"refs,omitempty"`
}

var (
	ErrNotFound  = errors.New("objstore: object not found")
	ErrTxClosed  = errors.New("objstore: transaction already committed or rolled back")
	ErrGCRunning = errors.New("objstore: garbage collection already in progress")
)

// Store is a durable, GC-managed object store. The zero value is not usable;
// construct one with Open.
type Store struct {
	mu      sync.RWMutex
	objects map[string]*Object
	roots   map[string]string // root name -> head object ID
	wal     *os.File
	walEnc  *json.Encoder

	gc    gcState
	hooks gcHooks // test-only interleaving hooks
}

// walRecord is one atomic mutation appended to the write-ahead log. A single
// record may touch many roots and objects; replay applies it as a unit.
type walRecord struct {
	Roots    map[string]string `json:"roots,omitempty"`
	DelRoots []string          `json:"delRoots,omitempty"`
	Puts     []*Object         `json:"puts,omitempty"`
	Dels     []string          `json:"dels,omitempty"`
}

// Open opens (or creates) the store backed by the WAL file at path and
// replays any committed records, restoring the state as of the last
// completed transaction or GC deletion — including after a crash.
func Open(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{
		objects: make(map[string]*Object),
		roots:   make(map[string]string),
		wal:     f,
		walEnc:  json.NewEncoder(f),
	}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, fmt.Errorf("objstore: replaying %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) replay() error {
	if _, err := s.wal.Seek(0, 0); err != nil {
		return err
	}
	sc := bufio.NewScanner(s.wal)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec walRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// A torn tail write from a crash is not a committed record.
			break
		}
		s.apply(rec)
	}
	return sc.Err()
}

// apply mutates in-memory state; caller holds s.mu (or is single-threaded
// during replay).
func (s *Store) apply(rec walRecord) {
	for _, id := range rec.Dels {
		delete(s.objects, id)
	}
	for _, obj := range rec.Puts {
		cp := *obj
		s.objects[cp.ID] = &cp
	}
	for _, name := range rec.DelRoots {
		delete(s.roots, name)
	}
	for name, id := range rec.Roots {
		s.roots[name] = id
	}
}

// appendWAL durably appends one record. The caller must hold s.mu, so WAL
// order always matches the order in which mutations become visible in
// memory; replay therefore converges to the same state.
func (s *Store) appendWAL(rec walRecord) error {
	if err := s.walEnc.Encode(rec); err != nil {
		return err
	}
	return s.wal.Sync()
}

// Close flushes and closes the underlying WAL.
func (s *Store) Close() error {
	return s.wal.Close()
}

// Get returns a copy of the object with the given ID.
func (s *Store) Get(id string) (*Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *obj
	return &cp, nil
}

// Root returns the object ID the named root currently points to.
func (s *Store) Root(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.roots[name]
	if !ok {
		return "", ErrNotFound
	}
	return id, nil
}

// Roots returns a snapshot of all root names and their targets.
func (s *Store) Roots() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.roots))
	for k, v := range s.roots {
		out[k] = v
	}
	return out
}

// Tx is an atomic multi-root, multi-object mutation. It is not safe for
// concurrent use.
type Tx struct {
	store *Store
	done  bool

	roots    map[string]string
	delRoots map[string]struct{}
	puts     map[string]*Object
	dels     map[string]struct{}
}

// Begin starts a new transaction.
func (s *Store) Begin() *Tx {
	return &Tx{
		store:    s,
		roots:    make(map[string]string),
		delRoots: make(map[string]struct{}),
		puts:     make(map[string]*Object),
		dels:     make(map[string]struct{}),
	}
}

// Put stages an object insert or replace.
func (t *Tx) Put(obj *Object) { t.puts[obj.ID] = obj }

// DeleteObject stages an object removal.
func (t *Tx) DeleteObject(id string) { t.dels[id] = struct{}{} }

// SetRoot stages pointing the named root at id, creating the root if needed.
func (t *Tx) SetRoot(name, id string) {
	t.roots[name] = id
	delete(t.delRoots, name)
}

// DeleteRoot stages removing the named root.
func (t *Tx) DeleteRoot(name string) {
	t.delRoots[name] = struct{}{}
	delete(t.roots, name)
}

// Rollback discards the transaction.
func (t *Tx) Rollback() { t.done = true }

// Commit durably applies every staged change as one atomic unit. If a GC
// cycle is in flight, every newly created edge (root -> object and
// object -> object) is passed through the insertion write barrier so the
// targets enter the retained set and cannot be swept concurrently.
func (t *Tx) Commit() error {
	if t.done {
		return ErrTxClosed
	}
	t.done = true
	s := t.store

	rec := walRecord{Roots: t.roots}
	for name := range t.delRoots {
		rec.DelRoots = append(rec.DelRoots, name)
	}
	for _, obj := range t.puts {
		rec.Puts = append(rec.Puts, obj)
	}
	for id := range t.dels {
		rec.Dels = append(rec.Dels, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Referential integrity: every edge created by this commit must target
	// an object that exists once the commit applies. Without this check a
	// commit could attach a root to an object the concurrent sweeper has
	// already reclaimed, and no barrier could resurrect it.
	exists := func(id string) bool {
		if _, ok := t.puts[id]; ok {
			return true
		}
		if _, ok := t.dels[id]; ok {
			return false
		}
		_, ok := s.objects[id]
		return ok
	}
	for _, id := range t.roots {
		if !exists(id) {
			return fmt.Errorf("objstore: root target %q: %w", id, ErrNotFound)
		}
	}
	for _, obj := range t.puts {
		for _, ref := range obj.Refs {
			if !exists(ref) {
				return fmt.Errorf("objstore: object %q references %q: %w", obj.ID, ref, ErrNotFound)
			}
		}
	}

	if err := s.appendWAL(rec); err != nil {
		return err
	}
	s.apply(rec)

	// Insertion write barrier: shade every object that became referenced by
	// this commit while the collector is marking or sweeping.
	if s.gc.active() {
		for _, id := range t.roots {
			s.gc.shade(id)
		}
		for _, obj := range t.puts {
			for _, ref := range obj.Refs {
				s.gc.shade(ref)
			}
		}
	}
	return nil
}
