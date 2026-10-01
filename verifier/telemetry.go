package verifier

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event types synthesized locally by the verifier. Telemetry never leaves
// the process except through an Emitter supplied by the caller; no external
// monitoring platform is contacted.
const (
	EventChunkStaged    = "chunk_staged"
	EventChunkRejected  = "chunk_rejected"
	EventCommitStarted  = "commit_started"
	EventIssueFound     = "issue_found"
	EventCommitSealed   = "commit_sealed"
	EventCommitRejected = "commit_rejected"
	EventRecovered      = "recovered"
)

// Event is one locally synthesized telemetry record.
type Event struct {
	Seq     int64     `json:"seq"`
	Type    string    `json:"type"`
	Version string    `json:"version,omitempty"`
	File    string    `json:"file,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	Time    time.Time `json:"time"`
}

// Emitter receives telemetry events.
type Emitter interface {
	Emit(Event)
}

// SliceEmitter collects events in memory; used by tests and fixtures.
type SliceEmitter struct {
	mu     sync.Mutex
	events []Event
}

func (s *SliceEmitter) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Seq = int64(len(s.events)) + 1
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	s.events = append(s.events, e)
}

// Events returns a copy of the collected events.
func (s *SliceEmitter) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// FileEmitter appends events as JSONL to a local file.
type FileEmitter struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
	seq int64
}

// NewFileEmitter creates (or appends to) a local JSONL telemetry file.
func NewFileEmitter(path string) (*FileEmitter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileEmitter{f: f, enc: json.NewEncoder(f)}, nil
}

func (fe *FileEmitter) Emit(e Event) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.seq++
	e.Seq = fe.seq
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	_ = fe.enc.Encode(e)
}

// Close flushes and closes the underlying file.
func (fe *FileEmitter) Close() error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	return fe.f.Close()
}

// nopEmitter discards events when the caller passes nil.
type nopEmitter struct{}

func (nopEmitter) Emit(Event) {}
