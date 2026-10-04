// Package seqlog implements an ordered write-ahead log manager that allows
// multiple producers to reserve append ranges concurrently and complete them
// out of order, while readers only ever observe the contiguous, persisted
// prefix of completed records.
//
// Guarantees:
//   - Reserved offsets are allocated monotonically and are never reused, so a
//     cancelled reservation can never cause its range to be reassigned to a
//     different record.
//   - The visible prefix advances only over contiguous completed segments;
//     out-of-order completion never exposes a gap to readers.
//   - A failed reservation is materialised on disk as an identifiable hole
//     record so the prefix can advance past it; holes are visible to readers
//     as such.
//   - Commit implies durability: a record only becomes visible after its
//     bytes have been fsynced.
package seqlog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

const (
	// HeaderSize is the fixed per-record header length in bytes.
	HeaderSize = 24

	magicNumber = 0x53514C47 // "SQLG"
	version     = 1

	recData = 1
	recHole = 2
)

var (
	ErrClosed        = errors.New("seqlog: log is closed")
	ErrBadSize       = errors.New("seqlog: payload size does not match reservation")
	ErrAlreadyDone   = errors.New("seqlog: reservation already completed")
	ErrNotWritten    = errors.New("seqlog: commit requires Write or Fail first")
	ErrCannotCancel  = errors.New("seqlog: cannot cancel after bytes were written; range is never reassigned")
	ErrCorruptHeader = errors.New("seqlog: corrupt record header")
	ErrOutOfRange    = errors.New("seqlog: read beyond visible prefix")
)

// RecordType classifies records visible to readers.
type RecordType uint8

const (
	DataRecord RecordType = recData
	HoleRecord RecordType = recHole
)

// Record is a single decoded record inside the visible prefix.
type Record struct {
	Type    RecordType
	Offset  int64 // offset of the record header in the file
	Payload []byte
}

// IsHole reports whether the record is a hole left by a failed or cancelled
// reservation.
func (r Record) IsHole() bool { return r.Type == HoleRecord }

// segState is the lifecycle state of a reserved segment.
type segState uint8

const (
	segPending segState = iota // reserved, not yet completed
	segDone                    // completed (committed or failed) and persisted
)

type segment struct {
	offset int64
	length int64 // header + payload
	state  segState
}

// Log manages ordered, visible-prefix access to an append-only file.
type Log struct {
	f *os.File

	mu       sync.Mutex
	cond     *sync.Cond
	allocOff int64              // next offset to allocate; never decreases
	visible  int64              // contiguous completed+persisted prefix
	segments map[int64]*segment // pending segments keyed by offset
	closed   bool
}

// Open creates or opens the managed log file at path. Existing content is
// truncated: the manager owns the whole file.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	l := &Log{
		f:        f,
		segments: make(map[int64]*segment),
	}
	l.cond = sync.NewCond(&l.mu)
	return l, nil
}

// Close releases the file. Pending reservations can no longer complete.
func (l *Log) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrClosed
	}
	l.closed = true
	l.cond.Broadcast()
	l.mu.Unlock()
	return l.f.Close()
}

// VisibleOffset returns the length of the contiguous, persisted prefix that
// readers may observe.
func (l *Log) VisibleOffset() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.visible
}

// AllocatedOffset returns the end of the highest reserved range. Offsets in
// [VisibleOffset, AllocatedOffset) are in flight.
func (l *Log) AllocatedOffset() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allocOff
}

// Reserve allocates a contiguous range for a record with the given payload
// size and returns a handle for completing it. The allocated range is never
// reused, even if the reservation is later cancelled or failed.
func (l *Log) Reserve(payloadSize int) (*Reservation, error) {
	if payloadSize < 0 {
		return nil, ErrBadSize
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	total := int64(HeaderSize + payloadSize)
	seg := &segment{offset: l.allocOff, length: total, state: segPending}
	l.segments[seg.offset] = seg
	l.allocOff += total
	return &Reservation{
		log:     l,
		seg:     seg,
		payload: int64(payloadSize),
	}, nil
}

// Barrier captures the current allocation frontier. Wait blocks until every
// reservation made before the barrier has completed (committed or failed)
// and been persisted, i.e. until the visible prefix reaches the frontier.
func (l *Log) Barrier() *Barrier {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &Barrier{log: l, target: l.allocOff}
}

// Barrier is a synchronisation point over a set of reservations.
type Barrier struct {
	log    *Log
	target int64
}

// Wait blocks until the visible prefix reaches the barrier frontier and
// returns the visible offset observed.
func (b *Barrier) Wait() (int64, error) {
	l := b.log
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.visible < b.target && !l.closed {
		l.cond.Wait()
	}
	if l.visible < b.target {
		return l.visible, ErrClosed
	}
	return l.visible, nil
}

// ReadAt reads from the visible prefix only. Reads that would cross the
// visible frontier fail with ErrOutOfRange.
func (l *Log) ReadAt(p []byte, off int64) (int, error) {
	l.mu.Lock()
	visible := l.visible
	l.mu.Unlock()
	if off < 0 || off+int64(len(p)) > visible {
		return 0, ErrOutOfRange
	}
	return l.f.ReadAt(p, off)
}

// Iterator decodes records inside the visible prefix, in offset order.
type Iterator struct {
	log *Log
	off int64
	end int64
}

// NewIterator returns an iterator over the currently visible prefix. The
// visible end is captured at creation time.
func (l *Log) NewIterator() *Iterator {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &Iterator{log: l, end: l.visible}
}

// Next returns the next record, or io.EOF when the visible prefix is
// exhausted.
func (it *Iterator) Next() (Record, error) {
	if it.off >= it.end {
		return Record{}, io.EOF
	}
	hdr := make([]byte, HeaderSize)
	if _, err := it.log.f.ReadAt(hdr, it.off); err != nil {
		return Record{}, err
	}
	recType, payloadLen, err := decodeHeader(hdr)
	if err != nil {
		return Record{}, err
	}
	end := it.off + HeaderSize + int64(payloadLen)
	if end > it.end {
		return Record{}, ErrCorruptHeader
	}
	var payload []byte
	if recType == recData && payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := it.log.f.ReadAt(payload, it.off+HeaderSize); err != nil {
			return Record{}, err
		}
	}
	rec := Record{Type: RecordType(recType), Offset: it.off, Payload: payload}
	it.off = end
	return rec, nil
}

// Reservation is a producer's handle to one reserved range.
type Reservation struct {
	log     *Log
	seg     *segment
	payload int64

	mu    sync.Mutex
	wrote bool
	done  bool
}

// Offset returns the file offset of this record's header.
func (r *Reservation) Offset() int64 { return r.seg.offset }

// PayloadOffset returns the file offset at which Write places the payload.
func (r *Reservation) PayloadOffset() int64 { return r.seg.offset + HeaderSize }

// Size returns the total reserved length including the header.
func (r *Reservation) Size() int64 { return r.seg.length }

// Write stores p as the record payload. len(p) must equal the reserved
// payload size. The record is not visible until Commit.
func (r *Reservation) Write(p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return ErrAlreadyDone
	}
	if int64(len(p)) != r.payload {
		return ErrBadSize
	}
	if r.payload > 0 {
		if _, err := r.log.f.WriteAt(p, r.seg.offset+HeaderSize); err != nil {
			return err
		}
	}
	r.wrote = true
	return nil
}

// Commit persists the record (header + payload + fsync) and marks the
// segment complete, allowing the visible prefix to advance over it once all
// earlier segments are complete.
func (r *Reservation) Commit() error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return ErrAlreadyDone
	}
	if !r.wrote {
		r.mu.Unlock()
		return ErrNotWritten
	}
	r.done = true
	r.mu.Unlock()

	if err := r.writeHeader(recData); err != nil {
		return err
	}
	if err := r.log.f.Sync(); err != nil {
		return err
	}
	r.log.complete(r.seg)
	return nil
}

// Fail materialises the reservation as an identifiable hole record and
// completes the segment, so the visible prefix can advance past it.
func (r *Reservation) Fail() error {
	return r.fail()
}

// Cancel abandons the reservation. If no bytes were written, the range is
// converted into a hole record (never reassigned). If bytes were already
// written, Cancel fails with ErrCannotCancel: a written range can never be
// reassigned to another record, so the caller must Commit or Fail instead.
func (r *Reservation) Cancel() error {
	r.mu.Lock()
	written := r.wrote
	r.mu.Unlock()
	if written {
		return ErrCannotCancel
	}
	return r.fail()
}

func (r *Reservation) fail() error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return ErrAlreadyDone
	}
	r.done = true
	r.mu.Unlock()

	// Zero the payload region so stale bytes are never mistaken for data,
	// then mark the record as a hole.
	if r.payload > 0 {
		zeros := make([]byte, r.payload)
		if _, err := r.log.f.WriteAt(zeros, r.seg.offset+HeaderSize); err != nil {
			return err
		}
	}
	if err := r.writeHeader(recHole); err != nil {
		return err
	}
	if err := r.log.f.Sync(); err != nil {
		return err
	}
	r.log.complete(r.seg)
	return nil
}

func (r *Reservation) writeHeader(recType uint8) error {
	hdr := encodeHeader(recType, uint64(r.payload))
	_, err := r.log.f.WriteAt(hdr, r.seg.offset)
	return err
}

// complete marks a segment done and advances the visible prefix over every
// contiguous completed segment.
func (l *Log) complete(seg *segment) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seg.state = segDone
	for {
		next, ok := l.segments[l.visible]
		if !ok || next.state != segDone {
			break
		}
		delete(l.segments, l.visible)
		l.visible += next.length
	}
	l.cond.Broadcast()
}

// Validate checks structural invariants: pending segments never overlap each
// other or the visible prefix, and the visible prefix contains a well-formed
// sequence of records. Intended for tests and diagnostics.
func (l *Log) Validate() error {
	l.mu.Lock()
	segs := make([]segment, 0, len(l.segments))
	for _, s := range l.segments {
		segs = append(segs, *s)
	}
	visible, alloc := l.visible, l.allocOff
	l.mu.Unlock()

	for i := range segs {
		s := segs[i]
		if s.offset < visible {
			return fmt.Errorf("seqlog: pending segment at %d overlaps visible prefix %d", s.offset, visible)
		}
		if s.offset+s.length > alloc {
			return fmt.Errorf("seqlog: segment at %d exceeds allocation frontier %d", s.offset, alloc)
		}
		for j := i + 1; j < len(segs); j++ {
			o := segs[j]
			if s.offset < o.offset+o.length && o.offset < s.offset+s.length {
				return fmt.Errorf("seqlog: overlapping segments at %d and %d", s.offset, o.offset)
			}
		}
	}

	// Walk the visible prefix; it must decode as contiguous records.
	it := &Iterator{log: l, end: visible}
	for {
		_, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("seqlog: visible prefix not contiguous: %v", err)
		}
	}
	if it.off != visible {
		return fmt.Errorf("seqlog: visible prefix walk ended at %d, want %d", it.off, visible)
	}
	return nil
}

func encodeHeader(recType uint8, payloadLen uint64) []byte {
	hdr := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(hdr[0:4], magicNumber)
	hdr[4] = version
	hdr[5] = recType
	binary.BigEndian.PutUint64(hdr[8:16], payloadLen)
	crc := crc32.ChecksumIEEE(hdr[0:16])
	binary.BigEndian.PutUint32(hdr[16:20], crc)
	return hdr
}

func decodeHeader(hdr []byte) (recType uint8, payloadLen uint64, err error) {
	if len(hdr) != HeaderSize {
		return 0, 0, ErrCorruptHeader
	}
	if binary.BigEndian.Uint32(hdr[0:4]) != magicNumber || hdr[4] != version {
		return 0, 0, ErrCorruptHeader
	}
	crc := crc32.ChecksumIEEE(hdr[0:16])
	if binary.BigEndian.Uint32(hdr[16:20]) != crc {
		return 0, 0, ErrCorruptHeader
	}
	recType = hdr[5]
	if recType != recData && recType != recHole {
		return 0, 0, ErrCorruptHeader
	}
	return recType, binary.BigEndian.Uint64(hdr[8:16]), nil
}
