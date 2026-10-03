package wal

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
)

// headerMagic reserves the first bytes of the log file so that LSN 0
// is never a valid record position; PrevLSN == 0 therefore
// unambiguously means "no previous record".
var headerMagic = []byte("ARIESWL1")

// ErrNotAWAL is returned when opening a file that is not a WAL.
var ErrNotAWAL = errors.New("wal: file does not start with WAL magic")

// WAL is an append-only log file. Append is safe for concurrent use;
// the LSN of a record is the byte offset of its frame in the file.
type WAL struct {
	mu         sync.Mutex
	f          *os.File
	writeOff   uint64
	flushedLSN uint64
}

// Open opens (or creates) the log file at path. Existing content is
// preserved and considered durable.
func Open(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		if _, err := f.WriteAt(headerMagic, 0); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
		size = int64(len(headerMagic))
	} else {
		magic := make([]byte, len(headerMagic))
		if _, err := f.ReadAt(magic, 0); err != nil {
			f.Close()
			return nil, err
		}
		if string(magic) != string(headerMagic) {
			f.Close()
			return nil, ErrNotAWAL
		}
	}
	end := uint64(size)
	return &WAL{f: f, writeOff: end, flushedLSN: end}, nil
}

// Append serializes rec and appends it to the log, returning its LSN.
// The record is not necessarily durable until Flush is called.
func (w *WAL) Append(rec *Record) (uint64, error) {
	body := rec.encode()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))

	w.mu.Lock()
	defer w.mu.Unlock()
	lsn := w.writeOff
	if _, err := w.f.WriteAt(hdr[:], int64(lsn)); err != nil {
		return 0, err
	}
	if _, err := w.f.WriteAt(body, int64(lsn)+4); err != nil {
		return 0, err
	}
	rec.LSN = lsn
	w.writeOff = lsn + 4 + uint64(len(body))
	return lsn, nil
}

// Flush forces all appended records to stable storage.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		return err
	}
	w.flushedLSN = w.writeOff
	return nil
}

// FlushedLSN returns the LSN up to which the log is durable.
func (w *WAL) FlushedLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushedLSN
}

// End returns the LSN one past the last appended record.
func (w *WAL) End() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeOff
}

// Close flushes and closes the log file.
func (w *WAL) Close() error {
	w.Flush()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

// ReadAt reads the single record starting exactly at lsn.
func (w *WAL) ReadAt(lsn uint64) (*Record, error) {
	w.mu.Lock()
	f := w.f
	w.mu.Unlock()

	var hdr [4]byte
	if _, err := f.ReadAt(hdr[:], int64(lsn)); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	body := make([]byte, n)
	if _, err := f.ReadAt(body, int64(lsn)+4); err != nil {
		return nil, err
	}
	return decodeBody(lsn, body)
}

// Reader iterates records sequentially from a given LSN up to the
// end of the log at the time the reader was created. A torn tail
// (partial record left by a crash) is reported as io.EOF.
type Reader struct {
	f   *os.File
	off uint64
	end uint64
}

// ReaderFrom returns a reader starting at the given LSN. Values
// below the file header are clamped to the first record position.
func (w *WAL) ReaderFrom(lsn uint64) *Reader {
	w.mu.Lock()
	defer w.mu.Unlock()
	if lsn < uint64(len(headerMagic)) {
		lsn = uint64(len(headerMagic))
	}
	return &Reader{f: w.f, off: lsn, end: w.writeOff}
}

// Next returns the next record, or io.EOF at the end of the log.
func (r *Reader) Next() (*Record, error) {
	if r.off+4 > r.end {
		return nil, io.EOF
	}
	var hdr [4]byte
	if _, err := r.f.ReadAt(hdr[:], int64(r.off)); err != nil {
		return nil, io.EOF
	}
	n := uint64(binary.BigEndian.Uint32(hdr[:]))
	if r.off+4+n > r.end {
		return nil, io.EOF // torn tail from a crash
	}
	body := make([]byte, n)
	if _, err := r.f.ReadAt(body, int64(r.off)+4); err != nil {
		return nil, io.EOF
	}
	rec, err := decodeBody(r.off, body)
	if err != nil {
		return nil, err
	}
	r.off += 4 + n
	return rec, nil
}
