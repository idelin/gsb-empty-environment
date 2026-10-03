// Package store implements a crash-safe write-ahead log with fault
// injection hooks. It is the persistence half of the fault-tolerance
// verification suite: records are framed with magic, length and CRC32, and
// the log self-heals on open by rolling back to the last fully committed
// record after a power loss, disk-full event, or torn write.
package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc64"
	"sync"
)

// Record framing: [magic:4][length:4][crc64:8][payload:length]
// The CRC covers the payload only; the header is protected by the magic.
// Batch records use recordMagicBatch and carry a payload of concatenated
// inner records, so a torn batch fails one CRC and rolls back as a unit.
const (
	headerSize       = 16
	recordMagic      = 0x50424C47 // "PBLG" (single record)
	recordMagicBatch = 0x50424C42 // "PBLB" (atomic batch envelope)
)

var (
	// ErrCorrupt is returned when a record fails its integrity check during
	// normal operation (outside recovery, where corruption is repaired).
	ErrCorrupt = errors.New("store: record failed integrity check")
	// ErrClosed is returned on use of a closed store.
	ErrClosed = errors.New("store: closed")
)

var crcTable = crc64.MakeTable(crc64.ECMA)

// File is the minimal storage interface the WAL needs. *os.File satisfies
// it via osFile; tests substitute fault-injecting implementations.
type File interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Truncate(size int64) error
	Size() int64
	Close() error
}

// Stats reports store geometry for assertions and diagnostics.
type Stats struct {
	Offset   int64 // end of the last committed record
	Durable  int64 // end of the last fsynced prefix
	Records  int   // number of live records
	Recovered bool // true if Open truncated a corrupt tail
}

// Store is an append-only, single-writer/multi-reader WAL. All exported
// methods are safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	f       File
	offset  int64
	durable int64
	records int
	healed  bool
	closed  bool
}

// Open scans f, verifies every record, and rolls the file back to the last
// consistent prefix if the tail is torn (crash mid-write) or corrupt.
// The returned store is immediately writable at the healed offset.
func Open(f File) (*Store, error) {
	s := &Store{f: f}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// recover replays the log from offset 0. The first record that is
// incomplete, has a bad magic, or fails its CRC marks the rollback point:
// everything from there on is truncated. This is the self-healing path for
// sudden power loss (torn tail) and partial disk-full writes.
func (s *Store) recover() error {
	size := s.f.Size()
	var off int64
	var n int
	header := make([]byte, headerSize)
	for off+headerSize <= size {
		if _, err := s.f.ReadAt(header, off); err != nil {
			return fmt.Errorf("store: scan read at %d: %v", off, err)
		}
		magic := binary.LittleEndian.Uint32(header[0:4])
		length := binary.LittleEndian.Uint32(header[4:8])
		crc := binary.LittleEndian.Uint64(header[8:16])
		if magic != recordMagic && magic != recordMagicBatch {
			break // torn or garbage tail: roll back here
		}
		if uint64(length) > uint64(size)-uint64(off)-headerSize {
			break // length runs past EOF: torn write
		}
		payload := make([]byte, length)
		if _, err := s.f.ReadAt(payload, off+headerSize); err != nil {
			return fmt.Errorf("store: scan payload at %d: %v", off, err)
		}
		if crc64.Checksum(payload, crcTable) != crc {
			break // bit rot or half-flushed payload: roll back
		}
		off += headerSize + int64(length)
		if magic == recordMagicBatch {
			inner, err := countInnerRecords(payload)
			if err != nil {
				break // malformed batch envelope: roll back
			}
			n += inner
		} else {
			n++
		}
	}
	if off < size {
		// Self-heal: discard the corrupt/torn suffix.
		if err := s.f.Truncate(off); err != nil {
			return fmt.Errorf("store: rollback truncate to %d: %v", off, err)
		}
		s.healed = true
	}
	s.offset = off
	s.durable = off // after recovery, everything on disk is consistent
	s.records = n
	return nil
}

// Append stages one record. It is durable only after Commit returns nil.
// If the underlying write fails (e.g. disk full), the partial record is
// rolled back immediately and the store stays usable.
func (s *Store) Append(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	rec := encodeRecord(payload)
	if _, err := s.f.WriteAt(rec, s.offset); err != nil {
		// Roll back any torn bytes the failed write may have left behind.
		_ = s.f.Truncate(s.offset)
		return fmt.Errorf("store: append failed, rolled back to %d: %v", s.offset, err)
	}
	s.offset += int64(len(rec))
	s.records++
	return nil
}

// AppendBatch stages multiple records as one atomic unit: either the whole
// batch becomes durable on the next Commit, or (after a crash) none of it
// survives recovery, because a torn batch tail fails the CRC scan.
func (s *Store) AppendBatch(payloads [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	var inner []byte
	for _, p := range payloads {
		inner = append(inner, encodeRecord(p)...)
	}
	buf := encodeRecordWithMagic(recordMagicBatch, inner)
	if _, err := s.f.WriteAt(buf, s.offset); err != nil {
		_ = s.f.Truncate(s.offset)
		return fmt.Errorf("store: batch append failed, rolled back to %d: %v", s.offset, err)
	}
	s.offset += int64(len(buf))
	s.records += len(payloads)
	return nil
}

// Commit fsyncs the log, making every staged record durable.
func (s *Store) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("store: commit sync: %v", err)
	}
	s.durable = s.offset
	return nil
}

// Scan returns all live payloads in order. It reads only the committed
// prefix, so it is consistent even while other goroutines append.
func (s *Store) Scan() ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	var out [][]byte
	header := make([]byte, headerSize)
	for off := int64(0); off < s.offset; {
		if _, err := s.f.ReadAt(header, off); err != nil {
			return nil, err
		}
		magic := binary.LittleEndian.Uint32(header[0:4])
		if magic != recordMagic && magic != recordMagicBatch {
			return nil, ErrCorrupt
		}
		length := binary.LittleEndian.Uint32(header[4:8])
		crc := binary.LittleEndian.Uint64(header[8:16])
		payload := make([]byte, length)
		if _, err := s.f.ReadAt(payload, off+headerSize); err != nil {
			return nil, err
		}
		if crc64.Checksum(payload, crcTable) != crc {
			return nil, ErrCorrupt
		}
		if magic == recordMagicBatch {
			inner, err := decodeInnerRecords(payload)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
		} else {
			out = append(out, payload)
		}
		off += headerSize + int64(length)
	}
	return out, nil
}

// Stats returns a snapshot of store geometry.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Offset:    s.offset,
		Durable:   s.durable,
		Records:   s.records,
		Recovered: s.healed,
	}
}

// Close releases the underlying file. The store must not be used after.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.closed = true
	return s.f.Close()
}

func encodeRecord(payload []byte) []byte {
	return encodeRecordWithMagic(recordMagic, payload)
}

func encodeRecordWithMagic(magic uint32, payload []byte) []byte {
	rec := make([]byte, headerSize+len(payload))
	binary.LittleEndian.PutUint32(rec[0:4], magic)
	binary.LittleEndian.PutUint32(rec[4:8], uint32(len(payload)))
	binary.LittleEndian.PutUint64(rec[8:16], crc64.Checksum(payload, crcTable))
	copy(rec[headerSize:], payload)
	return rec
}

// decodeInnerRecords splits a batch envelope payload into its member
// payloads, verifying each inner record's framing and CRC.
func decodeInnerRecords(data []byte) ([][]byte, error) {
	var out [][]byte
	for len(data) > 0 {
		if len(data) < headerSize {
			return nil, ErrCorrupt
		}
		if binary.LittleEndian.Uint32(data[0:4]) != recordMagic {
			return nil, ErrCorrupt
		}
		length := binary.LittleEndian.Uint32(data[4:8])
		crc := binary.LittleEndian.Uint64(data[8:16])
		if uint64(length) > uint64(len(data))-headerSize {
			return nil, ErrCorrupt
		}
		payload := data[headerSize : headerSize+int(length)]
		if crc64.Checksum(payload, crcTable) != crc {
			return nil, ErrCorrupt
		}
		cp := make([]byte, len(payload))
		copy(cp, payload)
		out = append(out, cp)
		data = data[headerSize+int(length):]
	}
	return out, nil
}

func countInnerRecords(data []byte) (int, error) {
	recs, err := decodeInnerRecords(data)
	if err != nil {
		return 0, err
	}
	return len(recs), nil
}
