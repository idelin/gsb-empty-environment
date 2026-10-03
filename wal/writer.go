package wal

import (
	"os"
	"sync"
)

// Writer appends records to a log file. LSNs are byte offsets into the file,
// so a record's LSN uniquely locates it on disk. Safe for concurrent use.
type Writer struct {
	mu  sync.Mutex
	f   *os.File
	lsn uint64
}

// NewWriter creates a fresh log at path, truncating any existing file.
func NewWriter(path string) (*Writer, error) {
	return openWriter(path, 0)
}

// AppendWriter opens an existing log and continues writing at startLSN,
// truncating anything beyond it (e.g. a torn tail left by a crash).
func AppendWriter(path string, startLSN uint64) (*Writer, error) {
	return openWriter(path, int64(startLSN))
}

func openWriter(path string, offset int64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(offset); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(offset, 0); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, lsn: uint64(offset)}, nil
}

// Append encodes rec, assigns its LSN, and writes it to the log. It returns
// the assigned LSN. The record is not synced to disk; call Sync to force it.
func (w *Writer) Append(rec *Record) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.lsn
	buf, err := Encode(nil, rec)
	if err != nil {
		return 0, err
	}
	if _, err := w.f.Write(buf); err != nil {
		return 0, err
	}
	w.lsn += uint64(len(buf))
	return rec.LSN, nil
}

// Sync flushes the log to durable storage.
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

// NextLSN returns the LSN the next appended record will receive.
func (w *Writer) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lsn
}

// Close syncs and closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}
