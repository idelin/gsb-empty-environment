// Package logstore is an inbox.Store persistence model built on an
// append-only write-ahead log. Each commit appends ONE framed record
// (length + CRC32 + JSON payload) and fsyncs it. Recovery replays valid
// records and truncates a torn tail, so a crash mid-write can only lose
// the uncommitted record, never corrupt committed history.
package logstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

	"inbox"
)

const fileName = "inbox.wal"

const (
	opDedup    = "dedup"
	opPut      = "put"
	opEnqueue  = "enqueue"
	opMarkSent = "marksent"
)

type op struct {
	Kind string               `json:"kind"`
	Key  string               `json:"key,omitempty"`
	Val  []byte               `json:"val,omitempty"`
	Msg  *inbox.OutboxMessage `json:"msg,omitempty"`
}

type record struct {
	Ops []op `json:"ops"`
}

// memState is the in-memory index rebuilt by replaying the log.
type memState struct {
	dedup  map[string]bool
	kv     map[string][]byte
	outbox map[string]inbox.OutboxMessage
	sent   map[string]bool
	order  []string
}

func newMemState() *memState {
	return &memState{
		dedup:  make(map[string]bool),
		kv:     make(map[string][]byte),
		outbox: make(map[string]inbox.OutboxMessage),
		sent:   make(map[string]bool),
	}
}

func (m *memState) clone() *memState {
	c := newMemState()
	for k, v := range m.dedup {
		c.dedup[k] = v
	}
	for k, v := range m.kv {
		vv := make([]byte, len(v))
		copy(vv, v)
		c.kv[k] = vv
	}
	for k, v := range m.outbox {
		c.outbox[k] = v
	}
	for k, v := range m.sent {
		c.sent[k] = v
	}
	c.order = append(c.order, m.order...)
	return c
}

func (m *memState) apply(o op) {
	switch o.Kind {
	case opDedup:
		m.dedup[o.Key] = true
	case opPut:
		vv := make([]byte, len(o.Val))
		copy(vv, o.Val)
		m.kv[o.Key] = vv
	case opEnqueue:
		if _, ok := m.outbox[o.Msg.ID]; !ok {
			m.order = append(m.order, o.Msg.ID)
		}
		m.outbox[o.Msg.ID] = *o.Msg
	case opMarkSent:
		if _, ok := m.outbox[o.Key]; ok {
			m.sent[o.Key] = true
		}
	}
}

// Store implements inbox.Store on top of an append-only WAL.
type Store struct {
	mu   sync.Mutex
	path string
	f    *os.File
	st   *memState
}

// Open replays the WAL in dir, truncates any torn tail record left by a
// crash, and returns a ready store.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, f: f, st: newMemState()}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// replay applies records until EOF or a corrupt/incomplete frame; the
// file is then truncated to the last good offset.
func (s *Store) replay() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var good int64
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(s.f, header); err != nil {
			break // clean EOF or short header: stop here
		}
		length := binary.BigEndian.Uint32(header[0:4])
		crc := binary.BigEndian.Uint32(header[4:8])
		payload := make([]byte, length)
		if _, err := io.ReadFull(s.f, payload); err != nil {
			break // torn payload
		}
		if crc32.ChecksumIEEE(payload) != crc {
			break // corrupt record
		}
		var rec record
		if err := json.Unmarshal(payload, &rec); err != nil {
			break
		}
		for _, o := range rec.Ops {
			s.st.apply(o)
		}
		good, _ = s.f.Seek(0, io.SeekCurrent)
	}
	if err := s.f.Truncate(good); err != nil {
		return err
	}
	_, err := s.f.Seek(good, io.SeekStart)
	return err
}

func (s *Store) Close() error { return s.f.Close() }

// appendRecord frames and durably appends one commit record.
func (s *Store) appendRecord(rec record) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	frame := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)
	if _, err := s.f.Write(frame); err != nil {
		return err
	}
	return s.f.Sync()
}

func (s *Store) Update(ctx context.Context, fn func(inbox.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.st.clone()
	t := &tx{st: next}
	if err := fn(t); err != nil {
		return err // rollback: nothing was appended
	}
	if err := s.appendRecord(record{Ops: t.ops}); err != nil {
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

// tx applies mutations to a cloned state and records them as ops, so a
// successful commit is replayable verbatim after a crash.
type tx struct {
	st  *memState
	ops []op
}

func (t *tx) InsertDedup(eventID string) error {
	if t.st.dedup[eventID] {
		return inbox.ErrDuplicate
	}
	t.st.dedup[eventID] = true
	t.ops = append(t.ops, op{Kind: opDedup, Key: eventID})
	return nil
}

func (t *tx) PutState(key string, value []byte) error {
	vv := make([]byte, len(value))
	copy(vv, value)
	t.st.kv[key] = vv
	t.ops = append(t.ops, op{Kind: opPut, Key: key, Val: vv})
	return nil
}

func (t *tx) GetState(key string) ([]byte, bool, error) {
	v, ok := t.st.kv[key]
	return v, ok, nil
}

func (t *tx) EnqueueOutbox(msg inbox.OutboxMessage) error {
	if _, ok := t.st.outbox[msg.ID]; !ok {
		t.st.order = append(t.st.order, msg.ID)
	}
	t.st.outbox[msg.ID] = msg
	m := msg
	t.ops = append(t.ops, op{Kind: opEnqueue, Msg: &m})
	return nil
}

func (t *tx) PendingOutbox() ([]inbox.OutboxMessage, error) {
	var out []inbox.OutboxMessage
	for _, id := range t.st.order {
		if t.st.sent[id] {
			continue
		}
		out = append(out, t.st.outbox[id])
	}
	return out, nil
}

func (t *tx) MarkSent(id string) error {
	if _, ok := t.st.outbox[id]; ok {
		t.st.sent[id] = true
		t.ops = append(t.ops, op{Kind: opMarkSent, Key: id})
	}
	return nil
}
