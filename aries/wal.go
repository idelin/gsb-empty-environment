package aries

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// WAL is the write-ahead log. Appends are buffered in memory and made
// durable with Flush. LSNs are assigned sequentially starting at 1, so the
// record with LSN n is stored at index n-1.
type WAL struct {
	mu      sync.Mutex
	records []*LogRecord
	nextLSN LSN
	path    string
}

// NewWAL creates an empty WAL persisted at path.
func NewWAL(path string) *WAL {
	return &WAL{path: path, nextLSN: 1}
}

// Append assigns the next LSN to rec and appends it to the log.
func (w *WAL) Append(rec *LogRecord) LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.nextLSN
	w.nextLSN++
	w.records = append(w.records, rec)
	return rec.LSN
}

// Records returns a snapshot of all in-memory log records.
func (w *WAL) Records() []*LogRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*LogRecord(nil), w.records...)
}

// Record returns the record with the given LSN, or nil if out of range.
func (w *WAL) Record(lsn LSN) *LogRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	if lsn < 1 || int(lsn) > len(w.records) {
		return nil
	}
	return w.records[lsn-1]
}

// Flush makes the whole log durable (atomic file replace).
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(w.records); err != nil {
		return fmt.Errorf("wal: encode: %w", err)
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("wal: write: %w", err)
	}
	if err := os.Rename(tmp, w.path); err != nil {
		return fmt.Errorf("wal: rename: %w", err)
	}
	return nil
}

// LoadWAL replays a durable WAL file from disk.
func LoadWAL(path string) (*WAL, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("wal: read: %w", err)
	}
	var records []*LogRecord
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&records); err != nil {
		return nil, fmt.Errorf("wal: decode: %w", err)
	}
	w := &WAL{path: path, records: records, nextLSN: LSN(len(records)) + 1}
	return w, nil
}
