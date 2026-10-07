// Package logalloc implements a reserve-then-write log allocator.
//
// Batch sequence numbers are reserved before data is written, so a crash
// between reservation and commit leaves a gap. The publish stream only
// advances its persisted cut point over contiguous decided records, so a
// gap must be closed by an explicit skip record before publishing can move
// past it. On recovery, reserved-but-undecided sequences are reported
// separately from skipped ones so the operator can resolve them.
package logalloc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// RecordState is the decision state of a reserved sequence number.
type RecordState int

const (
	// StatePending means the sequence was reserved but neither committed
	// nor skipped yet (a potential gap after a crash).
	StatePending RecordState = iota
	// StateCommitted means data was written for the sequence.
	StateCommitted
	// StateSkipped means the sequence was explicitly skipped to close a gap.
	StateSkipped
)

func (s RecordState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateCommitted:
		return "committed"
	case StateSkipped:
		return "skipped"
	}
	return "unknown"
}

// Record is a single slot in the log.
type Record struct {
	Seq    uint64
	State  RecordState
	Data   []byte
	Reason string // set for skipped records
}

var (
	// ErrNotReserved is returned when deciding a sequence that was never
	// reserved (or is beyond the reserved high-water mark).
	ErrNotReserved = errors.New("logalloc: sequence not reserved")
	// ErrAlreadyDecided is returned when committing or skipping a sequence
	// that already has a decision.
	ErrAlreadyDecided = errors.New("logalloc: sequence already decided")
	// ErrAlreadyConsumed is returned when deciding a sequence at or below
	// the persisted cut point.
	ErrAlreadyConsumed = errors.New("logalloc: sequence already consumed")
	// ErrInvalidReservation is returned for a zero-count reservation.
	ErrInvalidReservation = errors.New("logalloc: reservation count must be > 0")
)

// WAL entry types.
const (
	entryReserve = "reserve"
	entryCommit  = "commit"
	entrySkip    = "skip"
	entryCut     = "cut"
)

type walEntry struct {
	Type   string `json:"type"`
	Start  uint64 `json:"start,omitempty"`
	Count  uint64 `json:"count,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Log is a durable reserve-then-write log with a publish cut point.
//
// Sequence numbers are 1-based. Reserved() is the next sequence that will
// be allocated; Cut() is the highest sequence such that every sequence at
// or below it is decided (committed or skipped) and consumed.
type Log struct {
	mu       sync.Mutex
	file     *os.File
	reserved uint64             // next sequence to allocate
	cut      uint64             // persisted cut point
	records  map[uint64]*Record // slots with seq > cut
}

// Open opens (or creates) the log stored at path and replays its WAL.
func Open(path string) (*Log, error) {
	l := &Log{records: make(map[uint64]*Record)}
	if err := l.replay(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	l.file = f
	return l, nil
}

func (l *Log) replay(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e walEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("logalloc: corrupt WAL entry: %v", err)
		}
		switch e.Type {
		case entryReserve:
			if e.Start+e.Count > l.reserved {
				l.reserved = e.Start + e.Count
			}
			for seq := e.Start; seq < e.Start+e.Count; seq++ {
				if seq <= l.cut {
					continue
				}
				if _, ok := l.records[seq]; !ok {
					l.records[seq] = &Record{Seq: seq, State: StatePending}
				}
			}
		case entryCommit:
			l.records[e.Seq] = &Record{Seq: e.Seq, State: StateCommitted, Data: e.Data}
		case entrySkip:
			l.records[e.Seq] = &Record{Seq: e.Seq, State: StateSkipped, Reason: e.Reason}
		case entryCut:
			l.cut = e.Seq
		default:
			return fmt.Errorf("logalloc: unknown WAL entry type %q", e.Type)
		}
	}
	return sc.Err()
}

// appendLocked writes and fsyncs a WAL entry. l.mu must be held.
func (l *Log) appendLocked(e walEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := l.file.Write(append(b, '\n')); err != nil {
		return err
	}
	return l.file.Sync()
}

// Reserve allocates count consecutive sequence numbers and returns the
// first one. The reservation is persisted before it is returned, so the
// same numbers are never re-allocated after a crash.
func (l *Log) Reserve(count uint64) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if count == 0 {
		return 0, ErrInvalidReservation
	}
	start := l.reserved
	if start == 0 {
		start = 1
	}
	if err := l.appendLocked(walEntry{Type: entryReserve, Start: start, Count: count}); err != nil {
		return 0, err
	}
	for seq := start; seq < start+count; seq++ {
		l.records[seq] = &Record{Seq: seq, State: StatePending}
	}
	l.reserved = start + count
	return start, nil
}

// decideLocked validates and applies a commit/skip decision.
func (l *Log) decideLocked(seq uint64) (*Record, error) {
	if seq == 0 || seq >= l.reserved {
		return nil, ErrNotReserved
	}
	if seq <= l.cut {
		return nil, ErrAlreadyConsumed
	}
	rec, ok := l.records[seq]
	if !ok {
		return nil, ErrNotReserved
	}
	if rec.State != StatePending {
		return nil, ErrAlreadyDecided
	}
	return rec, nil
}

// Commit writes data for a previously reserved sequence.
func (l *Log) Commit(seq uint64, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.decideLocked(seq)
	if err != nil {
		return err
	}
	if err := l.appendLocked(walEntry{Type: entryCommit, Seq: seq, Data: data}); err != nil {
		return err
	}
	rec.State = StateCommitted
	rec.Data = data
	return nil
}

// Skip closes the slot at seq with an explicit skip record, allowing the
// publish cut point to advance past the gap.
func (l *Log) Skip(seq uint64, reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.decideLocked(seq)
	if err != nil {
		return err
	}
	if err := l.appendLocked(walEntry{Type: entrySkip, Seq: seq, Reason: reason}); err != nil {
		return err
	}
	rec.State = StateSkipped
	rec.Reason = reason
	return nil
}

// AdvanceCut moves the persisted cut point forward over every contiguous
// decided sequence. It stops at the first pending slot and returns the new
// cut point. Slots at or below the cut are considered consumed.
func (l *Log) AdvanceCut() (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.cut
	for {
		rec, ok := l.records[cut+1]
		if !ok || rec.State == StatePending {
			break
		}
		cut++
	}
	if cut == l.cut {
		return l.cut, nil
	}
	if err := l.appendLocked(walEntry{Type: entryCut, Seq: cut}); err != nil {
		return 0, err
	}
	l.cut = cut
	return cut, nil
}

// Cut returns the persisted cut point.
func (l *Log) Cut() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cut
}

// Reserved returns the next sequence number that will be allocated.
func (l *Log) Reserved() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reserved == 0 {
		return 1
	}
	return l.reserved
}

// Pending returns, in order, the reserved-but-undecided sequences above the
// cut point. After a crash these are the slots recovery must resolve by
// either committing recovered data or writing explicit skip records.
func (l *Log) Pending() []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []uint64
	for seq, rec := range l.records {
		if rec.State == StatePending {
			out = append(out, seq)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Read returns up to limit committed records with sequence in (after, Cut()].
// Skipped slots are not returned; their absence is safe because the cut
// point guarantees they were explicit decisions, not lost data.
func (l *Log) Read(after uint64, limit int) []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Record
	for seq := after + 1; seq <= l.cut; seq++ {
		rec, ok := l.records[seq]
		if !ok {
			continue
		}
		if rec.State != StateCommitted {
			continue
		}
		out = append(out, *rec)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Close flushes and closes the underlying WAL file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
