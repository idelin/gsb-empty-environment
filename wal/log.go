package wal

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// LogManager appends records to a WAL file and supports sequential
// scans and random access by LSN. All methods are safe for
// concurrent use.
type LogManager struct {
	mu      sync.RWMutex
	f       *os.File
	offsets map[uint64]int64
	lastLSN uint64
	flushed uint64
}

// OpenLog opens (or creates) a WAL file and rebuilds the in-memory
// LSN index. A corrupt or truncated tail is reported as an error.
func OpenLog(path string) (*LogManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	m := &LogManager{f: f, offsets: make(map[uint64]int64)}
	if err := m.reindex(); err != nil {
		f.Close()
		return nil, err
	}
	return m, nil
}

func (m *LogManager) reindex() error {
	var off int64
	for {
		hdr := make([]byte, 8)
		n, err := m.f.ReadAt(hdr, off)
		if err == io.EOF && n == 0 {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wal: reading frame header at offset %d: %w", off, err)
		}
		bodyLen := int64(int32From(hdr[0:4]))
		if bodyLen <= 0 || bodyLen > maxRecordSize {
			return fmt.Errorf("wal: corrupt frame length %d at offset %d", bodyLen, off)
		}
		body := make([]byte, bodyLen)
		if _, err := m.f.ReadAt(body, off+8); err != nil {
			return fmt.Errorf("wal: reading frame body at offset %d: %w", off, err)
		}
		rec, err := decodeFrame(hdr, body)
		if err != nil {
			return fmt.Errorf("wal: corrupt record at offset %d: %w", off, err)
		}
		if _, dup := m.offsets[rec.LSN]; dup {
			return fmt.Errorf("wal: duplicate LSN %d at offset %d", rec.LSN, off)
		}
		m.offsets[rec.LSN] = off
		if rec.LSN > m.lastLSN {
			m.lastLSN = rec.LSN
		}
		off += 8 + bodyLen
	}
}

// Append assigns the next LSN to rec, writes it to the log file and
// returns the assigned LSN. The record is in the OS page cache after
// Append returns; call Flush for durability.
func (m *LogManager) Append(rec *Record) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastLSN++
	rec.LSN = m.lastLSN
	frame := encodeFrame(rec)
	off, err := m.f.Seek(0, io.SeekEnd)
	if err != nil {
		panic(fmt.Errorf("wal: seek: %w", err))
	}
	if _, err := m.f.Write(frame); err != nil {
		panic(fmt.Errorf("wal: append: %w", err))
	}
	m.offsets[rec.LSN] = off
	return rec.LSN
}

// Flush forces all log records up to (and including) the given LSN
// to durable storage.
func (m *LogManager) Flush(upto uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if upto <= m.flushed {
		return
	}
	if err := m.f.Sync(); err != nil {
		panic(fmt.Errorf("wal: sync: %w", err))
	}
	m.flushed = m.lastLSN
}

// FlushedLSN reports the highest LSN known to be durable.
func (m *LogManager) FlushedLSN() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.flushed
}

// LastLSN reports the highest LSN assigned so far.
func (m *LogManager) LastLSN() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastLSN
}

// Read returns the record with the given LSN.
func (m *LogManager) Read(lsn uint64) (*Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	off, ok := m.offsets[lsn]
	if !ok {
		return nil, fmt.Errorf("wal: no record with LSN %d", lsn)
	}
	return m.readAt(off)
}

func (m *LogManager) readAt(off int64) (*Record, error) {
	hdr := make([]byte, 8)
	if _, err := m.f.ReadAt(hdr, off); err != nil {
		return nil, err
	}
	body := make([]byte, int32From(hdr[0:4]))
	if _, err := m.f.ReadAt(body, off+8); err != nil {
		return nil, err
	}
	return decodeFrame(hdr, body)
}

// ScanFrom invokes fn for every record with LSN >= fromLSN, in
// ascending LSN order. Iteration stops at the first fn error.
func (m *LogManager) ScanFrom(fromLSN uint64, fn func(*Record) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	start := fromLSN
	if start < 1 {
		start = 1
	}
	for lsn := start; lsn <= m.lastLSN; lsn++ {
		off, ok := m.offsets[lsn]
		if !ok {
			continue
		}
		rec, err := m.readAt(off)
		if err != nil {
			return err
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the underlying log file.
func (m *LogManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.f.Close()
}

func int32From(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
