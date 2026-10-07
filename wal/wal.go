// Package wal is a persistence model for the inbox based on an append-only
// log. Each committed transaction is a single checksummed record written
// with one write + fsync, so a commit is atomic. On open, the log is
// replayed and any torn tail (crash mid-write) is truncated.
package wal

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"sync"

	"inbox"
)

const (
	kindTx   = "tx"
	kindSent = "sent"
)

// record is one log entry. A "tx" record carries the whole transaction
// (dedup record + state delta + outputs); a "sent" record marks one output.
type record struct {
	Kind     string
	EventID  string
	State    map[string][]byte
	Outputs  []inbox.Output
	OutputID string
}

// Store persists inbox state as an append-only log.
type Store struct {
	mu     sync.Mutex
	f      *os.File
	seen   map[string]bool
	state  map[string][]byte
	outbox map[string]inbox.Output
	order  []string // output ids in commit order
}

// Open opens (or creates) the log at path and replays it, truncating any
// torn tail left by a crash.
func Open(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s := &Store{
		f:      f,
		seen:   make(map[string]bool),
		state:  make(map[string][]byte),
		outbox: make(map[string]inbox.Output),
	}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) replay() error {
	var good int64
	for {
		hdr := make([]byte, 8)
		if _, err := s.f.ReadAt(hdr, good); err != nil {
			break // clean EOF or torn header
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		crc := binary.BigEndian.Uint32(hdr[4:8])
		payload := make([]byte, n)
		if _, err := s.f.ReadAt(payload, good+8); err != nil {
			break // torn payload
		}
		if crc32.ChecksumIEEE(payload) != crc {
			break // corrupt tail
		}
		var r record
		if err := json.Unmarshal(payload, &r); err != nil {
			break
		}
		s.apply(r)
		good += 8 + int64(n)
	}
	if err := s.f.Truncate(good); err != nil {
		return err
	}
	_, err := s.f.Seek(good, 0)
	return err
}

func (s *Store) apply(r record) {
	switch r.Kind {
	case kindTx:
		s.seen[r.EventID] = true
		for k, v := range r.State {
			s.state[k] = v
		}
		for _, o := range r.Outputs {
			if _, ok := s.outbox[o.ID]; !ok {
				s.order = append(s.order, o.ID)
			}
			s.outbox[o.ID] = o
		}
	case kindSent:
		if o, ok := s.outbox[r.OutputID]; ok {
			o.Sent = true
			s.outbox[r.OutputID] = o
		}
	}
}

// appendRecord writes one record durably (single write + fsync).
func (s *Store) appendRecord(r record) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(buf[4:8], crc32.ChecksumIEEE(payload))
	copy(buf[8:], payload)
	if _, err := s.f.Write(buf); err != nil {
		return err
	}
	return s.f.Sync()
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
	for _, id := range s.order {
		if o := s.outbox[id]; !o.Sent {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *Store) MarkSent(outputID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.outbox[outputID]; !ok {
		return errors.New("wal: unknown output id " + outputID)
	}
	r := record{Kind: kindSent, OutputID: outputID}
	if err := s.appendRecord(r); err != nil {
		return err
	}
	s.apply(r)
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

type tx struct {
	store   *Store
	eventID string
	state   map[string][]byte
	outputs []inbox.Output
}

func (t *tx) Seen(eventID string) bool { return t.store.seen[eventID] }

func (t *tx) Record(eventID string) error {
	t.eventID = eventID
	return nil
}

func (t *tx) GetState(key string) ([]byte, bool) {
	if v, ok := t.state[key]; ok {
		return v, true
	}
	v, ok := t.store.state[key]
	return v, ok
}

func (t *tx) SetState(key string, value []byte) { t.state[key] = value }

func (t *tx) Enqueue(out inbox.Output) { t.outputs = append(t.outputs, out) }

func (t *tx) Rollback() error {
	t.store.mu.Unlock()
	return nil
}

// Commit persists the whole transaction as ONE log record, then applies it
// to the in-memory view. If appendRecord fails, nothing was applied and the
// event can be retried.
func (t *tx) Commit() error {
	defer t.store.mu.Unlock()
	r := record{Kind: kindTx, EventID: t.eventID, State: t.state, Outputs: t.outputs}
	if err := t.store.appendRecord(r); err != nil {
		return err
	}
	t.store.apply(r)
	return nil
}
