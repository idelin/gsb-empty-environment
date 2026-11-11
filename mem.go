package netsec

import (
	"context"
	"errors"
	"hash/crc32"
	"io"
	"sync"
)

// memDisk is the modelled persistent medium. It survives MemorySink reopen
// but not MemoryDisk.PowerLoss, which wipes only frames that were not synced.
type memDisk struct {
	mu     sync.Mutex
	data   []byte
	closed bool
}

// MemoryDisk is the simulated storage medium backing one or more MemorySink
// instances. The zero value is ready to use.
type MemoryDisk struct {
	disk *memDisk
}

// NewMemoryDisk creates an empty simulated medium.
func NewMemoryDisk() *MemoryDisk {
	return &MemoryDisk{disk: &memDisk{}}
}

// PowerLoss discards every unsynced frame and closes the sink that lost power.
// Synced frames remain readable after Reopen, modelling a durable disk that
// survives process crash but loses unflushed page cache.
func (d *MemoryDisk) PowerLoss() {
	disk := d.disk
	disk.mu.Lock()
	disk.closed = true
	disk.mu.Unlock()
}

// MemorySink is an in-process Sink used for deterministic fault-injection
// tests. A successful Append is equivalent to write+fsync: the frame is moved
// onto the shared MemoryDisk and therefore survives reopen.
type MemorySink struct {
	mu       sync.Mutex // protects sink-local closed flag
	faultMu  sync.Mutex // serializes fault callbacks; never held while data is locked
	disk     *memDisk
	maxBytes int
	faults   Faults
	closed   bool
}

// NewMemorySink opens a sink over the given medium. maxBytes <= 0 means the
// only bound is the Committer slot limit; otherwise Append fails with
// ErrSinkFull once the durable log would exceed maxBytes (after a Compact).
func NewMemorySink(disk *MemoryDisk, maxBytes int, faults Faults) *MemorySink {
	disk.disk.mu.Lock()
	disk.disk.closed = false
	disk.disk.mu.Unlock()
	return &MemorySink{disk: disk.disk, maxBytes: maxBytes, faults: faults}
}

// Reopen simulates process restart: it opens a fresh sink over the same
// medium. Frames that were synced before PowerLoss are still present.
func (d *MemoryDisk) Reopen(faults Faults) *MemorySink {
	d.disk.mu.Lock()
	d.disk.closed = false
	d.disk.mu.Unlock()
	return &MemorySink{disk: d.disk, faults: faults}
}

// Append implements Sink. Durability is modelled as immediate sync, so a nil
// return guarantees Replay after reopen will observe the record.
func (s *MemorySink) Append(ctx context.Context, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.faults.BeforeAppend != nil {
		before := s.faults.BeforeAppend
		s.mu.Unlock()
		s.faultMu.Lock()

		err := before(rec)
		s.faultMu.Unlock()
		if err != nil {
			return err
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return ErrClosed
		}
	}
	s.mu.Unlock()

	s.disk.mu.Lock()
	if s.disk.closed {
		s.disk.mu.Unlock()
		return ErrClosed
	}

	frame := encodeFrame(rec)
	if s.maxBytes > 0 && len(s.disk.data)+len(frame) > s.maxBytes {
		if err := s.compactLocked(ctx); err != nil {
			s.disk.mu.Unlock()
			return err
		}
		if len(s.disk.data)+len(frame) > s.maxBytes {
			s.disk.mu.Unlock()
			return ErrSinkFull
		}
	}

	s.disk.data = append(s.disk.data, frame...)

	if s.faults.AfterAppend != nil {
		after := s.faults.AfterAppend
		s.disk.mu.Unlock()
		// The frame is already durable: an injected fault here must not erase
		// it, mirroring a crash that happens after fsync returns.
		s.faultMu.Lock()
		err := after(rec)
		s.faultMu.Unlock()
		return err
	}
	s.disk.mu.Unlock()
	return nil
}

// Replay implements Sink.
func (s *MemorySink) Replay(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	return decodeFrames(s.disk.data)
}

// Compact implements Sink by retaining only the newest frame of every OpID.
// Pending frames are always retained because their outcome is still unknown.
func (s *MemorySink) Compact(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	return s.compactLocked(ctx)
}

// compactLocked performs compaction with the caller already holding s.mu.
func (s *MemorySink) compactLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	records, err := decodeFrames(s.disk.data)
	if err != nil {
		return err
	}

	keep := make(map[string]Record, len(records))
	for _, rec := range records {
		keep[rec.OpID] = rec
	}

	var out []byte
	for _, rec := range records {
		if latest, ok := keep[rec.OpID]; ok && latest.Seq == rec.Seq {
			out = append(out, encodeFrame(rec)...)
		}
	}
	s.disk.data = out
	return nil
}

// Bytes reports the current durable log size, including frame overhead.
func (s *MemorySink) Bytes() int {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	return len(s.disk.data)
}

// Close implements Sink.
func (s *MemorySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.closed = true
	return nil
}

// decodeFrames parses every frame in order. A trailing torn frame returns the
// valid prefix together with ErrCorrupt so callers never treat a partial
// write as an acknowledged event.
func decodeFrames(data []byte) ([]Record, error) {
	var records []Record
	pos := 0
	for pos < len(data) {
		if pos+framePrefix > len(data) {
			return records, errors.Join(ErrCorrupt, io.ErrUnexpectedEOF)
		}
		n := int(bigEndianUint32(data[pos:]))
		pos += framePrefix
		if n <= 0 || pos+n+crcSize > len(data) {
			return records, errors.Join(ErrCorrupt, io.ErrUnexpectedEOF)
		}
		payload := data[pos : pos+n]
		checksum := bigEndianUint32(data[pos+n:])
		if crc32.Checksum(payload, crcTable) != checksum {
			return records, ErrCorrupt
		}
		rec, err := decodeRecord(payload)
		if err != nil {
			return records, errors.Join(ErrCorrupt, err)
		}
		records = append(records, rec)
		pos += n + crcSize
	}
	return records, nil
}

func bigEndianUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
