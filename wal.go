package aries

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

// walMagic reserves the first 8 bytes of the file so that LSN 0 is never a
// valid record offset and can serve as NilLSN.
var walMagic = []byte("ARIESWL1")

// WAL is an append-only write-ahead log backed by a single file. Appends are
// serialized by a mutex, so LSN allocation is race-free. On open, any torn
// tail left by a crash is detected via CRC and truncated.
type WAL struct {
	mu   sync.Mutex
	f    *os.File
	tail LSN // offset of the next append
	inj  *FaultInjector
}

// OpenWAL opens (or creates) the WAL at path. The file is scanned up to the
// first corrupt/torn record and truncated there, so appends always resume at
// a clean boundary.
func OpenWAL(path string, inj *FaultInjector) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{f: f, inj: inj}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.Size() == 0 {
		if _, err := f.Write(walMagic); err != nil {
			f.Close()
			return nil, err
		}
		w.tail = LSN(len(walMagic))
		return w, nil
	}
	data, err := io.ReadAll(io.NewSectionReader(f, 0, info.Size()))
	if err != nil {
		f.Close()
		return nil, err
	}
	if !bytes.HasPrefix(data, walMagic) {
		f.Close()
		return nil, fmt.Errorf("aries: %s is not a WAL file", path)
	}
	// Find the crash point: first record that fails to decode.
	off := len(walMagic)
	for off < len(data) {
		_, n, err := decodeRecord(data[off:])
		if err != nil {
			break
		}
		off += n
	}
	if off < len(data) {
		if err := f.Truncate(int64(off)); err != nil {
			f.Close()
			return nil, err
		}
	}
	w.tail = LSN(off)
	return w, nil
}

// Append assigns rec.LSN and durably appends it. The injected fault (if any)
// is returned as an error; a torn prefix may already have been written, which
// is exactly what a real power loss produces.
func (w *WAL) Append(rec *LogRecord) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.tail
	buf := encodeRecord(rec)
	n, err := w.inj.beforeLogWrite(len(buf))
	if n > 0 {
		if n > len(buf) {
			n = len(buf)
		}
		if _, werr := w.f.WriteAt(buf[:n], int64(w.tail)); werr != nil {
			return 0, werr
		}
		w.f.Sync()
	}
	if err != nil {
		return 0, err
	}
	w.tail += LSN(len(buf))
	return rec.LSN, nil
}

// Tail returns the LSN at which the next record will be appended.
func (w *WAL) Tail() LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tail
}

// Scan decodes every valid record from the start of the log.
func (w *WAL) Scan() ([]*LogRecord, error) {
	return w.ScanFrom(LSN(len(walMagic)))
}

// ScanFrom decodes every valid record at or after the given LSN. Decoding
// stops silently at the first torn/corrupt record (the crash point).
func (w *WAL) ScanFrom(from LSN) ([]*LogRecord, error) {
	w.mu.Lock()
	data, err := io.ReadAll(io.NewSectionReader(w.f, 0, int64(w.tail)))
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []*LogRecord
	off := int(from)
	for off < len(data) {
		rec, n, err := decodeRecord(data[off:])
		if err != nil {
			break
		}
		rec.LSN = LSN(off)
		out = append(out, rec)
		off += n
	}
	return out, nil
}

// ReadAt decodes the single record stored at the given LSN.
func (w *WAL) ReadAt(lsn LSN) (*LogRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if lsn >= w.tail {
		return nil, fmt.Errorf("aries: LSN %d beyond WAL tail %d", lsn, w.tail)
	}
	data, err := io.ReadAll(io.NewSectionReader(w.f, int64(lsn), int64(w.tail-lsn)))
	if err != nil {
		return nil, err
	}
	rec, _, err := decodeRecord(data)
	if err != nil {
		return nil, err
	}
	rec.LSN = lsn
	return rec, nil
}

// Close flushes and closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
