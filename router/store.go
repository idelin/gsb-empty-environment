package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// recordKind identifies the mutation a WAL record carries.
const (
	recNode    = "node"
	recSession = "session"
	recRequest = "request"
)

// record is one write-ahead log entry. Every state mutation is appended
// (and fsynced) before it is applied in memory, so a crash can never lose
// a generation bump that was already acknowledged.
type record struct {
	Kind    string         `json:"kind"`
	Node    string         `json:"node,omitempty"`
	Healthy bool           `json:"healthy,omitempty"`
	Session *Session       `json:"session,omitempty"`
	Request *RequestRecord `json:"request,omitempty"`
}

// Store is an append-only, fsynced JSON-lines write-ahead log.
type Store struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	records []record
}

// OpenStore opens (or creates) the log at path and loads all records.
func OpenStore(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, f: f}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Tolerate a torn tail write from a crash: stop replay there.
			break
		}
		s.records = append(s.records, rec)
	}
	if _, err := f.Seek(0, 2); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// append writes one record and fsyncs before returning.
func (s *Store) append(rec record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	s.records = append(s.records, rec)
	return nil
}

// Close flushes and closes the log.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

// Len reports how many records the log holds (test observability).
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func (s *Store) String() string { return fmt.Sprintf("Store(%s)", s.path) }
