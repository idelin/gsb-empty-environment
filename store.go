package netsec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// TripleStatus is the persisted lifecycle state of one triple ID.
type TripleStatus string

const (
	// StatusUnused means the ID has never participated in a check or use.
	StatusUnused TripleStatus = "unused"
	// StatusChecking is a transient reservation held while a sacrifice check
	// runs. On recovery it is treated as burned.
	StatusChecking TripleStatus = "checking"
	// StatusChecked means the candidate passed sacrifice checking and may be
	// consumed exactly once by a multiplication.
	StatusChecked TripleStatus = "checked"
	// StatusMultiplying is a transient reservation held while a multiplication
	// runs. On recovery it is treated as consumed.
	StatusMultiplying TripleStatus = "multiplying"
	// StatusConsumed means the triple was used (or an interrupted use was
	// recovered). It can never be used again.
	StatusConsumed TripleStatus = "consumed"
	// StatusBurned means the ID was sacrificed, rejected, or otherwise
	// permanently discarded.
	StatusBurned TripleStatus = "burned"
)

// TripleRecord binds an ID's lifecycle state to the digest of its shares.
type TripleRecord struct {
	Status TripleStatus      `json:"status"`
	Digest [sha256.Size]byte `json:"digest"`
}

// State is the durable protocol state.
type State struct {
	Version  int                     `json:"version"`
	Sequence uint64                  `json:"sequence"`
	Triples  map[string]TripleRecord `json:"triples"`
}

// Store provides an atomic read/modify/write transaction over State. Update
// must persist all mutations or none of them.
type Store interface {
	Update(func(*State) error) error
	View(func(*State) error) error
	Close() error
}

func newState() State {
	return State{Version: 1, Triples: make(map[string]TripleRecord)}
}

func cloneState(in *State) *State {
	out := &State{Version: in.Version, Sequence: in.Sequence, Triples: make(map[string]TripleRecord, len(in.Triples))}
	for k, v := range in.Triples {
		out.Triples[k] = v
	}
	return out
}

func recordOf(s *State, id string) TripleRecord {
	if rec, ok := s.Triples[id]; ok {
		return rec
	}
	return TripleRecord{Status: StatusUnused}
}

func normalizeInterrupted(s *State) bool {
	changed := false
	for id, rec := range s.Triples {
		switch rec.Status {
		case StatusChecking:
			rec.Status = StatusBurned
			s.Triples[id] = rec
			changed = true
		case StatusMultiplying:
			rec.Status = StatusConsumed
			s.Triples[id] = rec
			changed = true
		}
	}
	return changed
}

// MemoryStore is an in-process Store useful for tests.
type MemoryStore struct {
	mu     sync.Mutex
	state  State
	closed bool
}

// NewMemoryStore constructs an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: newState()}
}

// Update executes fn under the store lock and commits only on success.
func (s *MemoryStore) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	next := cloneState(&s.state)
	if err := fn(next); err != nil {
		return err
	}
	next.Sequence++
	s.state = *next
	return nil
}

// View executes fn on a snapshot.
func (s *MemoryStore) View(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return fn(cloneState(&s.state))
}

// Close marks the store closed.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Snapshot returns a copy of the current state. It is intended for tests and
// diagnostics; protocol code should use View.
func (s *MemoryStore) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *cloneState(&s.state)
}

// FileStore persists State as atomically replaced JSON. A sidecar flock
// serializes independent FileStore instances in the same container.
type FileStore struct {
	path     string
	lockPath string
	mu       sync.Mutex
	closed   bool
}

// NewFileStore opens or creates a file-backed store and recovers interrupted
// operations. Any StatusChecking record becomes burned and any
// StatusMultiplying record becomes consumed.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrStore)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}
	s := &FileStore{path: path, lockPath: path + ".lock"}
	if err := s.Update(func(st *State) error {
		normalizeInterrupted(st)
		return nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the state file path.
func (s *FileStore) Path() string {
	return s.path
}

func (s *FileStore) lockFile() (*os.File, error) {
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: open lock: %v", ErrStore, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: flock: %v", ErrStore, err)
	}
	return f, nil
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func (s *FileStore) loadLocked() (State, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return newState(), nil
		}
		return State{}, fmt.Errorf("%w: read: %v", ErrStore, err)
	}
	if len(data) == 0 {
		return newState(), nil
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("%w: decode: %v", ErrStore, err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	if st.Triples == nil {
		st.Triples = make(map[string]TripleRecord)
	}
	return st, nil
}

func (s *FileStore) persistLocked(st *State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrStore, err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".q0106-state-*")
	if err != nil {
		return fmt.Errorf("%w: create temp: %v", ErrStore, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("%w: chmod temp: %v", ErrStore, err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%w: write temp: %v", ErrStore, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%w: sync temp: %v", ErrStore, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%w: close temp: %v", ErrStore, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("%w: rename: %v", ErrStore, err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Update runs fn under an exclusive file lock and atomically replaces the
// state file on success.
func (s *FileStore) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	lock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer unlockFile(lock)

	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := cloneState(&st)
	if err := fn(next); err != nil {
		return err
	}
	next.Sequence++
	return s.persistLocked(next)
}

// View runs fn on a file-locked snapshot.
func (s *FileStore) View(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	lock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	return fn(cloneState(&st))
}

// Close releases this handle. The durable file remains.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
