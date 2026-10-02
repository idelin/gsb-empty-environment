package wal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"aries/lfqueue"
)

// WAL is a write-ahead log. Appends are lock-free: the LSN is claimed with
// a CAS loop and the serialized record is pushed onto a lock-free MPMC
// queue. A single Flush drains the queue to stable storage, so the hot
// append path never touches a mutex or the file system.
type WAL struct {
	nextLSN atomic.Uint64
	pending *lfqueue.Queue[[]byte]
	file    *os.File
	// writeMu serializes only the slow Flush path against itself; the
	// append hot path is fully lock-free.
	writeMu sync.Mutex
}

// Open creates (or truncates) the WAL file at path.
func Open(path string, queueCapacity int) (*WAL, error) {
	return open(path, queueCapacity, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 1)
}

// OpenAppend opens an existing WAL for appending (used by recovery to
// write CLRs and END records). nextLSN must be one past the highest LSN
// already present in the file.
func OpenAppend(path string, queueCapacity int, nextLSN LSN) (*WAL, error) {
	return open(path, queueCapacity, os.O_CREATE|os.O_RDWR|os.O_APPEND, nextLSN)
}

func open(path string, queueCapacity int, flags int, firstLSN LSN) (*WAL, error) {
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, err
	}
	q, err := lfqueue.New[[]byte](queueCapacity)
	if err != nil {
		f.Close()
		return nil, err
	}
	w := &WAL{pending: q, file: f}
	w.nextLSN.Store(firstLSN)
	return w, nil
}

// Append assigns the next LSN to rec (CAS-accelerated, no global lock),
// buffers the serialized record and returns the assigned LSN.
func (w *WAL) Append(rec *Record) LSN {
	lsn := w.nextLSN.Add(1) - 1
	rec.LSN = lsn
	buf := rec.Marshal()
	for {
		err := w.pending.Enqueue(buf)
		if err == nil {
			return lsn
		}
		if !errors.Is(err, lfqueue.ErrFull) {
			panic(err)
		}
		// Buffer full: synchronously drain to make room, then retry.
		w.Flush()
	}
}

// Flush writes every buffered record to the file and fsyncs it.
func (w *WAL) Flush() error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	for {
		buf, err := w.pending.Dequeue()
		if errors.Is(err, lfqueue.ErrEmpty) {
			break
		}
		if err != nil {
			return err
		}
		if _, err := w.file.Write(buf); err != nil {
			return err
		}
	}
	return w.file.Sync()
}

// Close flushes and closes the underlying file.
func (w *WAL) Close() error {
	if err := w.Flush(); err != nil {
		return err
	}
	return w.file.Close()
}

// NextLSN reports the LSN the next Append will assign.
func (w *WAL) NextLSN() LSN { return w.nextLSN.Load() }

// Scan reads all records from the WAL file at path, in file order.
func Scan(path string) ([]*Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ScanReader(f)
}

// ScanReader decodes every record from r until EOF. A truncated tail
// record (the classic torn write after a crash) is ignored, mirroring
// real recovery systems that stop at the first incomplete record.
func ScanReader(r io.Reader) ([]*Record, error) {
	br := bufio.NewReader(r)
	data, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	var out []*Record
	for off := 0; off < len(data); {
		rec, n, err := UnmarshalRecord(data[off:])
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break // torn tail write: stop here
			}
			return nil, fmt.Errorf("wal: scan offset %d: %w", off, err)
		}
		out = append(out, rec)
		off += n
	}
	return out, nil
}
