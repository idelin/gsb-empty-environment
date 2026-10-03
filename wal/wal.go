package wal

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// ErrCorrupt is returned by Reader when a record fails its CRC check
// or cannot be decoded (e.g. a torn write at the tail of the log).
var ErrCorrupt = errors.New("wal: corrupt record")

// On-disk frame layout: [uint32 payload length][uint32 crc32][gob payload].
const frameHeaderSize = 8

// Writer appends checksummed records to a WAL file and assigns LSNs.
type Writer struct {
	mu   sync.Mutex
	f    *os.File
	next LSN
}

// NewWriter creates a WAL file at path, truncating any existing file.
func NewWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, next: 1}, nil
}

// OpenAppender opens an existing WAL for append (e.g. after a crash)
// and continues LSN numbering after the last readable record.
func OpenAppender(path string) (*Writer, error) {
	last, err := LastLSN(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, next: last + 1}, nil
}

// Append assigns the next LSN to rec, persists it and returns the LSN.
func (w *Writer) Append(rec *LogRecord) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.next
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(rec); err != nil {
		return 0, fmt.Errorf("wal: encode: %w", err)
	}
	payload := buf.Bytes()
	frame := make([]byte, frameHeaderSize, frameHeaderSize+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	frame = append(frame, payload...)
	if _, err := w.f.Write(frame); err != nil {
		return 0, fmt.Errorf("wal: write: %w", err)
	}
	w.next++
	return rec.LSN, nil
}

// Sync flushes the log to stable storage.
func (w *Writer) Sync() error { return w.f.Sync() }

// Close syncs and closes the underlying file.
func (w *Writer) Close() error {
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

// Reader iterates over the records of a WAL file sequentially.
type Reader struct {
	f *os.File
}

// NewReader opens the WAL at path for sequential reading.
func NewReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Reader{f: f}, nil
}

// Next returns the next record, io.EOF at a clean end of log, or
// ErrCorrupt when a frame fails validation (torn tail write).
func (r *Reader) Next() (*LogRecord, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r.f, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if err == io.ErrUnexpectedEOF {
				return nil, ErrCorrupt
			}
			return nil, io.EOF
		}
		return nil, err
	}
	length := binary.LittleEndian.Uint32(header[0:4])
	crc := binary.LittleEndian.Uint32(header[4:8])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r.f, payload); err != nil {
		return nil, ErrCorrupt
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, ErrCorrupt
	}
	var rec LogRecord
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&rec); err != nil {
		return nil, ErrCorrupt
	}
	return &rec, nil
}

// Close closes the underlying file.
func (r *Reader) Close() error { return r.f.Close() }

// ScanAll reads every valid record in the WAL at path. A corrupt tail
// (torn write during crash) is tolerated: scanning stops there.
func ScanAll(path string) ([]*LogRecord, error) {
	r, err := NewReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []*LogRecord
	for {
		rec, err := r.Next()
		if err == io.EOF || err == ErrCorrupt {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
}

// LastLSN returns the LSN of the last valid record (0 for an empty log).
func LastLSN(path string) (LSN, error) {
	recs, err := ScanAll(path)
	if err != nil {
		return 0, err
	}
	if len(recs) == 0 {
		return 0, nil
	}
	return recs[len(recs)-1].LSN, nil
}
