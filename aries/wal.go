package aries

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// WAL is an append-only write-ahead log backed by a single file.
// LSNs are assigned from a monotonically increasing counter starting at 1.
// All exported methods are safe for concurrent use.
type WAL struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	w       *bufio.Writer
	nextLSN uint64
	index   map[uint64]*LogRecord // in-memory LSN -> record index
	closed  bool
}

// OpenWAL opens (or creates) the log file at path and scans it to recover
// the LSN counter and the in-memory record index.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	w := &WAL{
		path:    path,
		f:       f,
		w:       bufio.NewWriterSize(f, 64*1024),
		nextLSN: 1,
		index:   make(map[uint64]*LogRecord),
	}
	maxLSN := uint64(0)
	err = scanFile(f, func(r *LogRecord) bool {
		if r.LSN > maxLSN {
			maxLSN = r.LSN
		}
		w.index[r.LSN] = r
		return true
	})
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("wal: scan %s: %v", path, err)
	}
	w.nextLSN = maxLSN + 1
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Append assigns the next LSN to rec, buffers it, indexes it, and returns
// the assigned LSN. The record is durable only after Flush.
func (w *WAL) Append(rec *LogRecord) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, fmt.Errorf("wal: append on closed log")
	}
	rec.LSN = w.nextLSN
	w.nextLSN++
	data := rec.Marshal()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.w.Write(lenBuf[:]); err != nil {
		return 0, err
	}
	if _, err := w.w.Write(data); err != nil {
		return 0, err
	}
	w.index[rec.LSN] = rec
	return rec.LSN, nil
}

// Flush forces all buffered records to stable storage.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("wal: flush on closed log")
	}
	if err := w.w.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

// FlushedLSN returns the largest LSN that has been appended so far
// (0 if the log is empty).
func (w *WAL) LastLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN - 1
}

// Record returns the in-memory copy of the record with the given LSN,
// or nil if it is not present (e.g. archived away).
func (w *WAL) Record(lsn uint64) *LogRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.index[lsn]
}

// Scan replays every record in LSN order, invoking fn for each.
// Iteration stops early if fn returns false.
func (w *WAL) Scan(fn func(*LogRecord) bool) error {
	return w.ScanFrom(1, fn)
}

// ScanFrom replays all records with LSN >= startLSN in order.
func (w *WAL) ScanFrom(startLSN uint64, fn func(*LogRecord) bool) error {
	w.mu.Lock()
	if !w.closed {
		if err := w.w.Flush(); err != nil {
			w.mu.Unlock()
			return err
		}
	}
	w.mu.Unlock()

	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()
	return scanFile(f, func(r *LogRecord) bool {
		if r.LSN < startLSN {
			return true
		}
		w.mu.Lock()
		w.index[r.LSN] = r
		w.mu.Unlock()
		return fn(r)
	})
}

// Archive moves every record with LSN <= uptoLSN (except records that
// belong to a transaction in keepTxns, which are still needed for undo)
// into archiveDir, and rewrites the live log to contain only the
// remaining records. It returns the archive file path and the number of
// records archived.
func (w *WAL) Archive(archiveDir string, uptoLSN uint64, keepTxns map[uint64]bool) (string, int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return "", 0, fmt.Errorf("wal: archive on closed log")
	}
	if uptoLSN == 0 {
		return "", 0, fmt.Errorf("wal: archive upto LSN must be > 0")
	}
	if err := w.w.Flush(); err != nil {
		return "", 0, err
	}

	var archived, kept []*LogRecord
	rf, err := os.Open(w.path)
	if err != nil {
		return "", 0, err
	}
	err = scanFile(rf, func(r *LogRecord) bool {
		if r.LSN <= uptoLSN && !keepTxns[r.TxnID] {
			archived = append(archived, r)
		} else {
			kept = append(kept, r)
		}
		return true
	})
	rf.Close()
	if err != nil {
		return "", 0, err
	}
	if len(archived) == 0 {
		return "", 0, nil
	}

	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return "", 0, err
	}
	archivePath := filepath.Join(archiveDir, fmt.Sprintf("wal-archive-%012d.log", uptoLSN))
	if err := writeRecordFile(archivePath, archived); err != nil {
		return "", 0, err
	}

	// Rewrite the live log atomically: temp file + rename.
	tmp := w.path + ".tmp"
	if err := writeRecordFile(tmp, kept); err != nil {
		return "", 0, err
	}
	w.f.Close()
	if err := os.Rename(tmp, w.path); err != nil {
		return "", 0, err
	}
	nf, err := os.OpenFile(w.path, os.O_RDWR, 0644)
	if err != nil {
		return "", 0, err
	}
	if _, err := nf.Seek(0, io.SeekEnd); err != nil {
		nf.Close()
		return "", 0, err
	}
	w.f = nf
	w.w = bufio.NewWriterSize(nf, 64*1024)
	for _, r := range archived {
		delete(w.index, r.LSN)
	}
	return archivePath, len(archived), nil
}

// Close flushes and closes the underlying file. It is idempotent.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.w.Flush(); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// scanFile reads length-prefixed records from f starting at offset 0.
func scanFile(f *os.File, fn func(*LogRecord) bool) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rd := bufio.NewReaderSize(f, 64*1024)
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(rd, lenBuf[:])
		if err == io.EOF {
			return nil
		}
		if err == io.ErrUnexpectedEOF {
			// Torn tail write from a crash: ignore the partial frame.
			return nil
		}
		if err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		data := make([]byte, n)
		if _, err := io.ReadFull(rd, data); err != nil {
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				return nil // torn tail
			}
			return err
		}
		var rec LogRecord
		if err := rec.Unmarshal(data); err != nil {
			return err
		}
		if !fn(&rec) {
			return nil
		}
	}
}

func writeRecordFile(path string, recs []*LogRecord) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 64*1024)
	for _, r := range recs {
		data := r.Marshal()
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(data); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
