// Package idedup implements a durable operation-deduplication layer.
//
// A caller submits an operation ID plus its content. The first submission
// executes the business function and commits the dedup entry and the
// business record in a single atomic log append. A retry with the same ID
// and identical content returns the original result; the same ID with
// different content is rejected as a conflict.
//
// Dedup entries have a bounded retention: an entry is reclaimed only when
// BOTH the caller-confirmed watermark and the retention rule allow it.
// Reclaimed IDs are not forgotten silently: a retry of an ID at or below
// the reclaimed watermark fails explicitly with ErrExpired.
//
// Durability contract: once Execute returns success, the record is fsynced
// to the WAL and survives process crash and power loss. A record that was
// written but not fsynced may be lost; the caller's retry then re-executes
// the operation. Either way at most one business record is ever published
// per operation ID.
package idedup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrConflict is returned when an operation ID is resubmitted with
	// different content bytes. The original record is never overwritten.
	ErrConflict = errors.New("idedup: operation ID already exists with different content")
	// ErrExpired is returned when an operation ID is at or below the
	// reclaimed watermark. This is the explicit rule for expired dedup
	// IDs: they are rejected, never silently re-executed.
	ErrExpired = errors.New("idedup: operation ID expired (dedup entry reclaimed)")
	// ErrOutOfOrder is returned when a NEW operation ID is not
	// lexicographically greater than the last committed ID. Watermark
	// semantics require caller-assigned IDs to be monotonically
	// increasing (e.g. timestamp- or sequence-prefixed IDs).
	ErrOutOfOrder = errors.New("idedup: operation IDs must be lexicographically increasing")
	// ErrCrashed is returned by every operation after a fault-injection
	// crash until the store is reopened with Open.
	ErrCrashed = errors.New("idedup: store crashed (injected fault), reopen required")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("idedup: store closed")
)

// Fault-injection points. Tests (and only tests) arm a hook via
// SetFaultHook; when the hook returns a non-nil error at a point, the
// store simulates a crash+power-loss exactly at that point.
const (
	PointBeforeWrite       = "before-write"        // op: before appending the WAL frame
	PointAfterWrite        = "after-write"         // op: frame written, not yet fsynced
	PointAfterSync         = "after-sync"          // op: fsynced, not yet applied in memory
	PointBeforeRespond     = "before-respond"      // op: fully committed, result not yet returned
	PointSnapBeforeRename  = "snap-before-rename"  // reclaim: snapshot.tmp fsynced, not renamed
	PointSnapAfterRename   = "snap-after-rename"   // reclaim: snapshot renamed, WAL not truncated
	PointSnapAfterTruncate = "snap-after-truncate" // reclaim: WAL truncated, memory not updated
)

const (
	walFileName      = "op.log"
	snapshotFileName = "snapshot"
	snapshotTmpName  = "snapshot.tmp"
)

// Entry is one committed operation: the dedup state and the business
// record are the same atomically committed unit.
type Entry struct {
	ID      string
	Content []byte
	Result  []byte
}

// Options configures a Store.
type Options struct {
	// MinRetain is the retention rule: the newest MinRetain committed
	// entries are never reclaimed, even if the caller confirmed a
	// watermark beyond them. Reclaim drops an entry only when the
	// caller-confirmed watermark AND this rule both allow it.
	MinRetain int
}

// Store is a durable dedup store rooted at a caller-specified directory.
// It is safe for concurrent use.
type Store struct {
	dir       string
	minRetain int

	mu             sync.Mutex
	log            *os.File
	syncedLen      int64 // bytes of op.log known to be fsynced
	entries        map[string]Entry
	order          []string // committed IDs in commit order (survivors only)
	reclaimedUpTo  string   // all IDs <= this have been reclaimed
	confirmedWM    string   // caller-confirmed watermark
	reclaimedCount uint64

	hook    func(point string) error
	crashed bool
	closed  bool
}

// Open opens (or creates) a store in dir and replays any persisted state.
// Replay never appends: a restart cannot duplicate business records.
func Open(dir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		minRetain: opts.MinRetain,
		entries:   make(map[string]Entry),
	}
	if err := s.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := s.openLog(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetFaultHook installs a fault-injection hook. It is intended for tests
// and the demo; passing nil disables injection.
func (s *Store) SetFaultHook(hook func(point string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = hook
}

// Execute submits an operation. Exactly-once semantics:
//
//   - first submission of id: runs fn, commits dedup entry + business
//     record atomically (one fsync), returns the result;
//   - retry with the same id and same content: returns the original
//     result without re-running fn, even after a crash that happened
//     between the commit and the original response;
//   - same id with different content: ErrConflict, nothing is written;
//   - id at or below the reclaimed watermark: ErrExpired (explicit rule);
//   - new id not greater than the last committed id: ErrOutOfOrder.
func (s *Store) Execute(id string, content []byte, fn func() []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	if e, ok := s.entries[id]; ok {
		if bytes.Equal(e.Content, content) {
			return e.Result, nil
		}
		return nil, fmt.Errorf("%w: id %q", ErrConflict, id)
	}
	if s.reclaimedUpTo != "" && id <= s.reclaimedUpTo {
		return nil, fmt.Errorf("%w: id %q, reclaimed watermark is %q", ErrExpired, id, s.reclaimedUpTo)
	}
	if n := len(s.order); n > 0 && id <= s.order[n-1] {
		return nil, fmt.Errorf("%w: id %q not greater than last committed id %q", ErrOutOfOrder, id, s.order[n-1])
	}

	result := fn()

	if err := s.fail(PointBeforeWrite); err != nil {
		return nil, err
	}
	frame := appendFrame(nil, encodeOpRecord(id, content, result))
	if _, err := s.log.Write(frame); err != nil {
		return nil, err
	}
	if err := s.fail(PointAfterWrite); err != nil {
		return nil, err
	}
	if err := s.log.Sync(); err != nil {
		return nil, err
	}
	s.syncedLen += int64(len(frame))
	if err := s.fail(PointAfterSync); err != nil {
		return nil, err
	}
	s.entries[id] = Entry{ID: id, Content: clone(content), Result: clone(result)}
	s.order = append(s.order, id)
	if err := s.fail(PointBeforeRespond); err != nil {
		return nil, err
	}
	return result, nil
}

// ConfirmWatermark records the caller-confirmed watermark: the caller
// promises it will never need results for operations with ID <= id.
// The watermark is durable (fsynced) and monotonic; a lower or equal
// watermark is a no-op.
func (s *Store) ConfirmWatermark(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	if id <= s.confirmedWM {
		return nil
	}
	frame := appendFrame(nil, encodeWatermarkRecord(id))
	if _, err := s.log.Write(frame); err != nil {
		return err
	}
	if err := s.log.Sync(); err != nil {
		return err
	}
	s.syncedLen += int64(len(frame))
	s.confirmedWM = id
	return nil
}

// Reclaim drops dedup entries that BOTH the caller-confirmed watermark
// and the retention rule (Options.MinRetain) allow to be dropped. It
// compacts the store: survivors are written to a new snapshot which is
// atomically renamed into place, then the WAL is truncated. It returns
// the number of reclaimed entries.
func (s *Store) Reclaim() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return 0, err
	}
	// Caller watermark gate: leading committed IDs at or below the
	// confirmed watermark (commit order == lexicographic order).
	k := 0
	for k < len(s.order) && s.order[k] <= s.confirmedWM {
		k++
	}
	// Retention-rule gate: the newest MinRetain entries are untouchable.
	protected := len(s.order) - s.minRetain
	if protected < 0 {
		protected = 0
	}
	n := k
	if protected < n {
		n = protected
	}
	if n == 0 {
		return 0, nil
	}

	sn := &snapshot{
		reclaimedUpTo:  s.order[n-1],
		confirmedWM:    s.confirmedWM,
		reclaimedCount: s.reclaimedCount + uint64(n),
	}
	for _, id := range s.order[n:] {
		sn.entries = append(sn.entries, s.entries[id])
	}

	tmp := filepath.Join(s.dir, snapshotTmpName)
	if err := os.WriteFile(tmp, encodeSnapshot(sn), 0o644); err != nil {
		return 0, err
	}
	if f, err := os.Open(tmp); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	if err := s.fail(PointSnapBeforeRename); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, snapshotFileName)); err != nil {
		return 0, err
	}
	syncDir(s.dir)
	if err := s.fail(PointSnapAfterRename); err != nil {
		return 0, err
	}
	// Snapshot is durable; truncate the WAL. If we crash before this,
	// recovery replays the old WAL on top of the snapshot and skips
	// everything already covered (replay is idempotent).
	if err := s.log.Close(); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, walFileName), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	s.log = f
	s.syncedLen = 0
	if err := s.fail(PointSnapAfterTruncate); err != nil {
		return 0, err
	}

	for _, id := range s.order[:n] {
		delete(s.entries, id)
	}
	s.order = append([]string(nil), s.order[n:]...)
	s.reclaimedUpTo = sn.reclaimedUpTo
	s.reclaimedCount = sn.reclaimedCount
	return n, nil
}

// BusinessLog returns the surviving business records in commit order.
func (s *Store) BusinessLog() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.entries[id])
	}
	return out
}

// ReclaimedWatermark returns the reclaimed watermark: every operation ID
// less than or equal to it has had its dedup entry reclaimed.
func (s *Store) ReclaimedWatermark() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaimedUpTo
}

// ConfirmedWatermark returns the caller-confirmed watermark.
func (s *Store) ConfirmedWatermark() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmedWM
}

// ReclaimedCount returns how many dedup entries have been reclaimed.
func (s *Store) ReclaimedCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaimedCount
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.crashed {
		return nil
	}
	s.closed = true
	return s.log.Close()
}

func (s *Store) usable() error {
	if s.crashed {
		return ErrCrashed
	}
	if s.closed {
		return ErrClosed
	}
	return nil
}

// fail runs the fault-injection hook. An injected error simulates a
// crash with power loss: unsynced WAL bytes are discarded (the file is
// truncated to the last fsynced length) and the store becomes unusable
// until reopened.
func (s *Store) fail(point string) error {
	if s.hook == nil {
		return nil
	}
	if err := s.hook(point); err != nil {
		s.crashLocked()
		return err
	}
	return nil
}

func (s *Store) crashLocked() {
	if s.log != nil {
		_ = s.log.Truncate(s.syncedLen) // power loss keeps only fsynced bytes
		_ = s.log.Close()
	}
	s.crashed = true
}

func (s *Store) loadSnapshot() error {
	// A leftover snapshot.tmp is an incomplete reclaim; discard it.
	_ = os.Remove(filepath.Join(s.dir, snapshotTmpName))
	data, err := os.ReadFile(filepath.Join(s.dir, snapshotFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	sn, err := decodeSnapshot(data)
	if err != nil {
		return err
	}
	s.reclaimedUpTo = sn.reclaimedUpTo
	s.confirmedWM = sn.confirmedWM
	s.reclaimedCount = sn.reclaimedCount
	for _, e := range sn.entries {
		s.entries[e.ID] = e
		s.order = append(s.order, e.ID)
	}
	return nil
}

func (s *Store) openLog() error {
	path := filepath.Join(s.dir, walFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_ = f.Close()
		return err
	}
	// Replay valid frames; stop at the first torn/corrupt frame and
	// truncate it (that tail was never fsynced, so nothing committed
	// is lost).
	off := 0
	for off < len(data) {
		payload, consumed, ok := decodeFrame(data[off:])
		if !ok {
			break
		}
		s.replay(payload)
		off += consumed
	}
	if off < len(data) {
		if err := f.Truncate(int64(off)); err != nil {
			_ = f.Close()
			return err
		}
	}
	s.syncedLen = int64(off)
	if _, err := f.Seek(0, 2); err != nil {
		_ = f.Close()
		return err
	}
	s.log = f
	return nil
}

func (s *Store) replay(payload []byte) {
	switch payload[0] {
	case recOp:
		id, content, result, err := decodeOpRecord(payload)
		if err != nil {
			return
		}
		// Idempotent replay: skip records already covered by the
		// snapshot (reclaimed or surviving). This is what makes a
		// crash between snapshot rename and WAL truncate safe.
		if s.reclaimedUpTo != "" && id <= s.reclaimedUpTo {
			return
		}
		if _, ok := s.entries[id]; ok {
			return
		}
		s.entries[id] = Entry{ID: id, Content: clone(content), Result: clone(result)}
		s.order = append(s.order, id)
	case recWatermark:
		if id, err := decodeWatermarkRecord(payload); err == nil && id > s.confirmedWM {
			s.confirmedWM = id
		}
	}
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
