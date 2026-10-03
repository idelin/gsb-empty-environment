package wal

import (
	"errors"
	"fmt"
	"os"
)

// Reader parses a log file through a read-only memory map. Decoded records
// alias the mapped buffer: scanning the log performs no per-record copies.
// A Reader is safe for concurrent use (it is read-only after Open).
type Reader struct {
	f   *os.File
	buf []byte
}

// Open maps path read-only. An empty file yields a Reader over zero bytes.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r := &Reader{f: f}
	if fi.Size() > 0 {
		buf, err := mapFile(f, fi.Size())
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("wal: mmap: %w", err)
		}
		r.buf = buf
	}
	return r, nil
}

// Buf exposes the underlying mapped buffer (used by zero-copy assertions).
func (r *Reader) Buf() []byte { return r.buf }

// Len returns the mapped size in bytes.
func (r *Reader) Len() int { return len(r.buf) }

// Close unmaps the buffer and closes the file. Records decoded earlier must
// not be used afterwards unless they were cloned.
func (r *Reader) Close() error {
	err := unmapFile(r.buf)
	r.buf = nil
	if cerr := r.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Records decodes every record strictly: the first malformed record is an
// error. Use for integrity validation.
func (r *Reader) Records() ([]*Record, error) {
	return r.scan(false)
}

// ScanValid decodes records until the first malformed or truncated one and
// returns the valid prefix. This models crash recovery: a torn tail (partial
// write, bad CRC) simply marks the end of the durable log.
func (r *Reader) ScanValid() ([]*Record, error) {
	return r.scan(true)
}

func (r *Reader) scan(tolerateTail bool) ([]*Record, error) {
	var out []*Record
	off := 0
	for off < len(r.buf) {
		rec, n, err := Decode(r.buf[off:])
		if err != nil {
			if tolerateTail {
				return out, nil
			}
			return nil, fmt.Errorf("wal: decode at offset %d: %w", off, err)
		}
		if rec.LSN != uint64(off) {
			if tolerateTail {
				return out, nil
			}
			return nil, fmt.Errorf("wal: LSN mismatch at offset %d", off)
		}
		out = append(out, rec)
		off += n
	}
	return out, nil
}

// RecordAt decodes the single record located at LSN (random access).
func (r *Reader) RecordAt(lsn uint64) (*Record, error) {
	if lsn >= uint64(len(r.buf)) {
		return nil, errors.New("wal: LSN out of range")
	}
	rec, _, err := Decode(r.buf[lsn:])
	return rec, err
}
