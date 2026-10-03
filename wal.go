package twopc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// RecordType classifies WAL records.
type RecordType string

const (
	// RecordTxnBegin starts a transaction; Detail holds the comma-joined
	// participant IDs.
	RecordTxnBegin RecordType = "TXN_BEGIN"
	// RecordVote persists a participant's vote.
	RecordVote RecordType = "VOTE"
	// RecordState persists a coordinator state transition.
	RecordState RecordType = "STATE"
	// RecordAudit is a pending-decision audit event, written when a
	// decision cannot be delivered to a participant within the retry
	// limit. It is the durable trace of an in-doubt transaction.
	RecordAudit RecordType = "AUDIT"
)

// Record is a single WAL entry (one JSON object per line).
type Record struct {
	TxnID       string     `json:"txn_id"`
	Type        RecordType `json:"type"`
	Participant string     `json:"participant,omitempty"`
	Vote        Vote       `json:"vote,omitempty"`
	State       TxnState   `json:"state,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	Time        time.Time  `json:"time"`
}

// WAL is an append-only write-ahead log. Every state transition is appended
// and fsynced before the coordinator acts on it.
type WAL struct {
	mu   sync.Mutex
	path string
	f    *os.File
	enc  *json.Encoder
}

// OpenWAL opens (or creates) the log file at path for appending.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &WAL{path: path, f: f, enc: json.NewEncoder(f)}, nil
}

// Append writes a record and flushes it to stable storage.
func (w *WAL) Append(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	r.Time = time.Now()
	if err := w.enc.Encode(r); err != nil {
		return err
	}
	return w.f.Sync()
}

// Replay invokes fn for every complete record in the log, in order. A torn
// tail record (from a crash mid-write) is ignored.
func (w *WAL) Replay(fn func(Record) error) error {
	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue // torn tail
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Close flushes and closes the log file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
