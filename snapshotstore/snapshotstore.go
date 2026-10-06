// Package snapshotstore is an inbox.Store persistence model that writes the
// full state as one JSON snapshot per commit, using write-temp-file +
// fsync + atomic rename. A crash can therefore leave either the old or the
// new snapshot, never a half-written one.
package snapshotstore

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"inbox"
)

const fileName = "state.json"

type state struct {
	Dedup  map[string]bool
	KV     map[string][]byte
	Outbox map[string]inbox.OutboxMessage
	Sent   map[string]bool
	Order  []string
}

func newState() *state {
	return &state{
		Dedup:  make(map[string]bool),
		KV:     make(map[string][]byte),
		Outbox: make(map[string]inbox.OutboxMessage),
		Sent:   make(map[string]bool),
	}
}

func (s *state) clone() *state {
	c := newState()
	for k, v := range s.Dedup {
		c.Dedup[k] = v
	}
	for k, v := range s.KV {
		vv := make([]byte, len(v))
		copy(vv, v)
		c.KV[k] = vv
	}
	for k, v := range s.Outbox {
		c.Outbox[k] = v
	}
	for k, v := range s.Sent {
		c.Sent[k] = v
	}
	c.Order = append(c.Order, s.Order...)
	return c
}

// Store implements inbox.Store on top of a single JSON snapshot file.
type Store struct {
	mu   sync.Mutex
	path string
	st   *state
}

// Open loads the snapshot in dir, or starts empty if none exists.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, fileName), st: newState()}
	data, err := ioutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s.st); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return nil }

// commit persists the whole state atomically: tmp file, fsync, rename.
func (s *Store) commit(st *state) error {
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
	return os.Rename(tmp, s.path)
}

func (s *Store) Update(ctx context.Context, fn func(inbox.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.st.clone()
	if err := fn(&tx{st: next}); err != nil {
		return err // rollback: the clone is discarded
	}
	if err := s.commit(next); err != nil {
		return err
	}
	s.st = next
	return nil
}

func (s *Store) View(ctx context.Context, fn func(inbox.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&tx{st: s.st.clone()})
}

type tx struct{ st *state }

func (t *tx) InsertDedup(eventID string) error {
	if t.st.Dedup[eventID] {
		return inbox.ErrDuplicate
	}
	t.st.Dedup[eventID] = true
	return nil
}

func (t *tx) PutState(key string, value []byte) error {
	vv := make([]byte, len(value))
	copy(vv, value)
	t.st.KV[key] = vv
	return nil
}

func (t *tx) GetState(key string) ([]byte, bool, error) {
	v, ok := t.st.KV[key]
	return v, ok, nil
}

func (t *tx) EnqueueOutbox(msg inbox.OutboxMessage) error {
	if _, ok := t.st.Outbox[msg.ID]; !ok {
		t.st.Order = append(t.st.Order, msg.ID)
	}
	t.st.Outbox[msg.ID] = msg
	return nil
}

func (t *tx) PendingOutbox() ([]inbox.OutboxMessage, error) {
	var out []inbox.OutboxMessage
	for _, id := range t.st.Order {
		if t.st.Sent[id] {
			continue
		}
		out = append(out, t.st.Outbox[id])
	}
	return out, nil
}

func (t *tx) MarkSent(id string) error {
	if _, ok := t.st.Outbox[id]; ok {
		t.st.Sent[id] = true
	}
	return nil
}
