// Package snapshot is a persistence model for the inbox based on atomic
// whole-state replacement. Every commit serializes the full state to a
// temporary file, fsyncs it, and atomically renames it over the previous
// snapshot, so a crash leaves either the old or the new consistent state.
package snapshot

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"inbox"
)

// diskState is the complete persisted state.
type diskState struct {
	Seen   map[string]bool
	State  map[string][]byte
	Outbox []inbox.Output // in commit order
}

func (st diskState) clone() diskState {
	next := diskState{
		Seen:   make(map[string]bool, len(st.Seen)),
		State:  make(map[string][]byte, len(st.State)),
		Outbox: make([]inbox.Output, len(st.Outbox)),
	}
	for k, v := range st.Seen {
		next.Seen[k] = v
	}
	for k, v := range st.State {
		next.State[k] = v
	}
	copy(next.Outbox, st.Outbox)
	return next
}

// Store persists inbox state as one atomically replaced snapshot file.
type Store struct {
	mu   sync.Mutex
	path string
	st   diskState
}

// Open loads the snapshot at path, if any. A leftover ".tmp" file from a
// crashed commit is ignored.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.st = diskState{Seen: map[string]bool{}, State: map[string][]byte{}}
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		return nil, err
	}
	if s.st.Seen == nil {
		s.st.Seen = map[string]bool{}
	}
	if s.st.State == nil {
		s.st.State = map[string][]byte{}
	}
	return s, nil
}

// persist writes st to a temp file, fsyncs, and atomically renames it over
// the current snapshot, then fsyncs the directory.
func (s *Store) persist(st diskState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
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
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Begin starts a transaction. The store is serialized: the tx holds the
// store lock until Commit or Rollback.
func (s *Store) Begin() (inbox.Tx, error) {
	s.mu.Lock()
	return &tx{store: s, state: make(map[string][]byte)}, nil
}

func (s *Store) Pending() ([]inbox.Output, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []inbox.Output
	for _, o := range s.st.Outbox {
		if !o.Sent {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *Store) MarkSent(outputID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.st.clone()
	found := false
	for i := range next.Outbox {
		if next.Outbox[i].ID == outputID {
			next.Outbox[i].Sent = true
			found = true
			break
		}
	}
	if !found {
		return errors.New("snapshot: unknown output id " + outputID)
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.st = next
	return nil
}

func (s *Store) Close() error { return nil }

type tx struct {
	store   *Store
	eventID string
	state   map[string][]byte
	outputs []inbox.Output
}

func (t *tx) Seen(eventID string) bool { return t.store.st.Seen[eventID] }

func (t *tx) Record(eventID string) error {
	t.eventID = eventID
	return nil
}

func (t *tx) GetState(key string) ([]byte, bool) {
	if v, ok := t.state[key]; ok {
		return v, true
	}
	v, ok := t.store.st.State[key]
	return v, ok
}

func (t *tx) SetState(key string, value []byte) { t.state[key] = value }

func (t *tx) Enqueue(out inbox.Output) { t.outputs = append(t.outputs, out) }

func (t *tx) Rollback() error {
	t.store.mu.Unlock()
	return nil
}

// Commit builds the next full state, persists it atomically, and only then
// swaps it in. If persist fails, the in-memory state is untouched and the
// event can be retried.
func (t *tx) Commit() error {
	defer t.store.mu.Unlock()
	next := t.store.st.clone()
	next.Seen[t.eventID] = true
	for k, v := range t.state {
		next.State[k] = v
	}
	next.Outbox = append(next.Outbox, t.outputs...)
	if err := t.store.persist(next); err != nil {
		return err
	}
	t.store.st = next
	return nil
}
