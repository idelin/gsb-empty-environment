package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// WAL is an append-only log file. LSNs are dense sequence numbers
// starting at 1. Safe for concurrent use.
type WAL struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	records []*Record // in-memory index, records[lsn-1]
}

// Open opens (or creates) the log file and scans it to rebuild the
// in-memory index.
func Open(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{f: f, path: path}
	if err := w.scan(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *WAL) scan() error {
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(w.f, lenBuf[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.f, payload); err != nil {
			if err == io.ErrUnexpectedEOF {
				break // tolerate torn tail write from a crash
			}
			return err
		}
		rec, err := DecodeRecord(payload)
		if err != nil {
			return fmt.Errorf("wal: scan: %w", err)
		}
		w.records = append(w.records, rec)
	}
	// Truncate any torn tail so future appends stay well-formed.
	off, err := w.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	size, err := w.f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if size > off {
		if err := w.f.Truncate(off); err != nil {
			return err
		}
	}
	_, err = w.f.Seek(0, io.SeekEnd)
	return err
}

// Append assigns the next LSN to r, persists it, and indexes it.
func (w *WAL) Append(r *Record) LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	r.LSN = LSN(len(w.records)) + 1
	payload := r.Encode()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.f.Write(lenBuf[:]); err != nil {
		panic(err)
	}
	if _, err := w.f.Write(payload); err != nil {
		panic(err)
	}
	w.records = append(w.records, r)
	return r.LSN
}

// Sync flushes the log to stable storage.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

// Get returns the record with the given LSN, or nil.
func (w *WAL) Get(lsn LSN) *Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	if lsn == NilLSN || int(lsn) > len(w.records) {
		return nil
	}
	return w.records[lsn-1]
}

// Records returns a snapshot of all records in LSN order.
func (w *WAL) Records() []*Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Record, len(w.records))
	copy(out, w.records)
	return out
}

// Len returns the number of records (== the next LSN - 1).
func (w *WAL) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.records)
}

// LastLSN returns the LSN of the most recent record, or NilLSN.
func (w *WAL) LastLSN() LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	return LSN(len(w.records))
}

// Close flushes and closes the log file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}
