package wal

import (
	"io"
	"os"
	"sync"
)

// WAL is an append-only log file. LSNs are byte offsets assigned at append
// time. WAL is safe for concurrent use.
type WAL struct {
	mu   sync.Mutex
	f    *os.File
	next LSN // next LSN to assign (current file size)
}

// Open creates or opens the log file at path. Existing content is kept and
// subsequent appends continue after it.
func Open(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &WAL{f: f, next: LSN(st.Size())}, nil
}

// Append serializes rec, assigns its LSN and durably writes it to the log.
// It returns the assigned LSN.
func (w *WAL) Append(rec *Record) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.next
	buf := make([]byte, rec.encodedSize())
	n := rec.encode(buf)
	if _, err := w.f.Write(buf[:n]); err != nil {
		return 0, err
	}
	w.next += LSN(n)
	return rec.LSN, nil
}

// Flush forces buffered log data to stable storage.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

// NextLSN returns the LSN that the next appended record will receive.
func (w *WAL) NextLSN() LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next
}

// Close flushes and closes the log file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Scan reads all valid records starting at LSN from. A corrupt or torn tail
// (as left by a crash) silently terminates the scan.
func (w *WAL) Scan(from LSN) ([]*Record, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Seek(int64(from), io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, w.next-from)
	nr, err := io.ReadFull(w.f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	data := buf[:nr]
	var recs []*Record
	pos := from
	for len(data) > 0 {
		rec, n, err := decodeRecord(data)
		if err == ErrCorruptTail {
			break
		}
		if err != nil {
			return recs, err
		}
		recs = append(recs, rec)
		data = data[n:]
		pos += LSN(n)
	}
	return recs, nil
}

// ScanAll reads every valid record in the log.
func (w *WAL) ScanAll() ([]*Record, error) {
	return w.Scan(0)
}
