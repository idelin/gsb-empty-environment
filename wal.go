package aries

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// WAL is an append-only write-ahead log. LSNs are byte offsets into the
// file, assigned monotonically by Append. Append is safe for concurrent use.
type WAL struct {
	mu     sync.Mutex
	f      *os.File
	next   uint64
	closed bool
}

func writeFileHeader(f *os.File) error {
	hdr := make([]byte, FileHeaderSize)
	binary.BigEndian.PutUint32(hdr[0:4], FileMagic)
	binary.BigEndian.PutUint16(hdr[4:6], CurrentVersion)
	_, err := f.WriteAt(hdr, 0)
	return err
}

func parseFileHeader(data []byte) (uint16, error) {
	if len(data) < FileHeaderSize {
		return 0, ErrShortBuffer
	}
	if binary.BigEndian.Uint32(data[0:4]) != FileMagic {
		return 0, ErrBadMagic
	}
	v := binary.BigEndian.Uint16(data[4:6])
	if v < Version1 || v > CurrentVersion {
		return 0, fmt.Errorf("%w: %d", ErrUnsupportedVersion, v)
	}
	return v, nil
}

// validLogLength returns the offset just past the last fully-decodable
// record; anything beyond it is a torn write left by a crash.
func validLogLength(data []byte, version uint16) uint64 {
	off := uint64(FileHeaderSize)
	for off < uint64(len(data)) {
		_, n, err := DecodeRecord(data[off:], version)
		if err != nil {
			break
		}
		off += uint64(n)
	}
	return off
}

// CreateWAL creates a fresh WAL file (truncating any existing one).
func CreateWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if err := writeFileHeader(f); err != nil {
		f.Close()
		return nil, err
	}
	return &WAL{f: f, next: FileHeaderSize}, nil
}

// OpenWAL opens an existing WAL for appending. A torn tail (partial record
// left by a crash mid-write) is truncated so new appends stay contiguous.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	version, err := parseFileHeader(data)
	if err != nil {
		f.Close()
		return nil, err
	}
	end := validLogLength(data, version)
	if end < uint64(len(data)) {
		if err := f.Truncate(int64(end)); err != nil {
			f.Close()
			return nil, err
		}
	}
	return &WAL{f: f, next: end}, nil
}

// Append serializes rec and writes it at the next LSN. The assigned LSN is
// stored into rec.LSN and returned.
func (w *WAL) Append(rec *Record) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}
	buf := rec.Encode()
	if _, err := w.f.WriteAt(buf, int64(w.next)); err != nil {
		return 0, err
	}
	rec.LSN = w.next
	w.next += uint64(len(buf))
	return rec.LSN, nil
}

// NextLSN returns the LSN the next appended record will receive.
func (w *WAL) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next
}

// Sync flushes the log to stable storage.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	return w.f.Sync()
}

// Close syncs and closes the log file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.closed = true
	w.f.Sync()
	return w.f.Close()
}

// WALImage is the whole log mapped into memory once, so records can be
// parsed in place with zero copying: decoded OldData/NewData slices point
// directly into Data.
type WALImage struct {
	Data    []byte
	Version uint16
}

// MapWAL loads the WAL file into an in-memory image for zero-copy scanning.
func MapWAL(path string) (*WALImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	version, err := parseFileHeader(data)
	if err != nil {
		return nil, err
	}
	return &WALImage{Data: data, Version: version}, nil
}

// Scan iterates all records from the start of the log. Iteration stops when
// fn returns false, or silently at a torn tail record.
func (img *WALImage) Scan(fn func(*Record) bool) {
	img.ScanFrom(FileHeaderSize, fn)
}

// ScanFrom iterates records starting at the given LSN (file offset).
func (img *WALImage) ScanFrom(off uint64, fn func(*Record) bool) {
	for off < uint64(len(img.Data)) {
		rec, n, err := DecodeRecord(img.Data[off:], img.Version)
		if err != nil {
			return // torn write at the tail: everything valid has been seen
		}
		rec.LSN = off
		if !fn(rec) {
			return
		}
		off += uint64(n)
	}
}

// DecodeAt decodes the single record located exactly at the given LSN.
func (img *WALImage) DecodeAt(lsn uint64) (*Record, error) {
	if lsn >= uint64(len(img.Data)) {
		return nil, ErrShortBuffer
	}
	rec, _, err := DecodeRecord(img.Data[lsn:], img.Version)
	if err != nil {
		return nil, err
	}
	rec.LSN = lsn
	return rec, nil
}
