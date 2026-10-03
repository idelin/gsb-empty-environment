package store

import (
	"fmt"
	"io"
	"sync"

	"wirefmt/wire"
)

// Record framing inside the log:
//
//	[totalLen uvarint][crc32 fixed32][payload ...]
//
// totalLen counts crc32(4) + payload. crc32 (Castagnoli) covers the
// payload only. A record is committed iff it is fully present on disk and
// its checksum verifies; anything beyond the last committed record is
// treated as a torn write and rolled back by recovery.
const crcSize = 4

// Store is an append-only, crash-safe record log.
type Store struct {
	mu       sync.RWMutex
	st       Storage
	tail     int64    // offset just past the last committed record
	payloads [][]byte // in-memory committed view
}

// Open recovers the log on st and returns a ready store. Recovery scans
// committed records, then truncates any torn tail so the file always ends
// exactly after the last valid record (self-healing).
func Open(st Storage) (*Store, error) {
	s := &Store{st: st}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// recover replays the log, materializes committed payloads and rolls the
// file back to the end of the last valid record.
func (s *Store) recover() error {
	size := s.st.Size()
	var off int64
	var committed [][]byte
	for off < size {
		payload, recLen, err := readRecord(s.st, off, size)
		if err != nil {
			// Torn or corrupt tail: roll back to the last good offset.
			if terr := s.st.Truncate(off); terr != nil {
				return fmt.Errorf("store: rollback truncate at %d: %v", off, terr)
			}
			break
		}
		committed = append(committed, payload)
		off += recLen
	}
	s.tail = off
	s.payloads = committed
	return nil
}

func readRecord(st Storage, off, size int64) ([]byte, int64, error) {
	hdr := make([]byte, wire.MaxVarintBytes)
	n := int64(0)
	for n < wire.MaxVarintBytes {
		if off+n >= size {
			return nil, 0, wire.ErrTruncated
		}
		if _, err := st.ReadAt(hdr[n:n+1], off+n); err != nil && err != io.EOF {
			return nil, 0, err
		}
		n++
		if hdr[n-1] < 0x80 {
			break
		}
	}
	total, _, err := wire.ConsumeUvarint(hdr[:n], 0)
	if err != nil {
		return nil, 0, err
	}
	if total < crcSize {
		return nil, 0, fmt.Errorf("store: corrupt record length %d", total)
	}
	if off+n+int64(total) > size {
		return nil, 0, wire.ErrTruncated // torn write: body never fully landed
	}
	body := make([]byte, total)
	if _, err := st.ReadAt(body, off+n); err != nil && err != io.EOF {
		return nil, 0, err
	}
	crc := uint32(body[0]) | uint32(body[1])<<8 | uint32(body[2])<<16 | uint32(body[3])<<24
	payload := body[crcSize:]
	if checksum(payload) != crc {
		return nil, 0, fmt.Errorf("store: crc mismatch at offset %d", off)
	}
	return payload, n + int64(total), nil
}

// Append durably commits payload. If a fault (power loss, disk full)
// interrupts the write, Append rolls the file back to the pre-call tail and
// returns the fault error: the store is always left in its last committed
// state.
func (s *Store) Append(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := encodeRecord(payload)
	if _, err := s.st.WriteAt(rec, s.tail); err != nil {
		// Roll back any torn bytes so the tail stays exactly committed.
		if terr := s.st.Truncate(s.tail); terr != nil {
			return fmt.Errorf("store: rollback after %v failed: %v", err, terr)
		}
		return err
	}
	if err := s.st.Sync(); err != nil {
		if terr := s.st.Truncate(s.tail); terr != nil {
			return fmt.Errorf("store: rollback after sync %v failed: %v", err, terr)
		}
		return err
	}
	s.payloads = append(s.payloads, payload)
	s.tail += int64(len(rec))
	return nil
}

func encodeRecord(payload []byte) []byte {
	total := crcSize + len(payload)
	rec := make([]byte, 0, wire.MaxVarintBytes+total)
	rec = wire.AppendUvarint(rec, uint64(total))
	rec = wire.AppendFixed32(rec, checksum(payload))
	return append(rec, payload...)
}

// Records returns a copy of all committed payloads in order.
func (s *Store) Records() [][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([][]byte, len(s.payloads))
	for i, p := range s.payloads {
		c := make([]byte, len(p))
		copy(c, p)
		out[i] = c
	}
	return out
}

// CommittedBytes returns the durable log size (rollback point).
func (s *Store) CommittedBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tail
}

// Count returns the number of committed records.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.payloads)
}
