// Package batchlease implements an append-log based durable batch lease for a
// sensor collection SDK.
//
// A lease binds a batch of sensor records (identified by a contiguous
// sequence range and a content summary) to the startup generation of the
// process that claimed it. Leases and confirmation high-water marks are
// appended to a CRC-protected write-ahead log and fsynced before any mutating
// call returns, so after a power loss the SDK can reopen the log and tell
// exactly which batch a record was promised to. A torn log tail and an
// interrupted log compaction are both recovered automatically.
package batchlease

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// On-disk file names inside the lease directory.
const (
	logName  = "lease.log"
	tmpName  = "lease.log.tmp"
	lockName = "lease.lock"
)

// Fault injection points. A FaultHook installed via WithFaultHook is invoked
// immediately before the corresponding file-system operation, at every
// write/sync/replace site in the package.
const (
	// OpCreate fires before creating or opening the log or the compaction
	// temporary file.
	OpCreate = "create"
	// OpWrite fires before writing a frame to the log or snapshot file.
	OpWrite = "write"
	// OpSync fires before fsync of a data file.
	OpSync = "sync"
	// OpRename fires before the atomic rename that swaps a compacted
	// snapshot into place.
	OpRename = "rename"
	// OpDirSync fires before fsync of the directory after a rename.
	OpDirSync = "dirsync"
)

// Sentinel errors.
var (
	// ErrClosed is returned when the store has been closed.
	ErrClosed = errors.New("batchlease: store closed")
	// ErrFailed is wrapped (use errors.Is) by every error returned after the
	// durable log hit an I/O fault. The process must reopen the store.
	ErrFailed = errors.New("batchlease: store in failed state, reopen required")
	// ErrInvalidToken is returned when a token does not match the stored
	// lease for the batch.
	ErrInvalidToken = errors.New("batchlease: invalid lease token")
	// ErrBatchExists is returned when the batch ID is already leased.
	ErrBatchExists = errors.New("batchlease: batch already leased")
	// ErrOverlap is returned when the requested sequence range overlaps a
	// range already promised to another batch.
	ErrOverlap = errors.New("batchlease: sequence range overlaps an existing lease")
	// ErrNonContiguous is returned when a confirmation does not extend the
	// persisted high-water mark by exactly one sequence.
	ErrNonContiguous = errors.New("batchlease: confirmation must advance the contiguous high-water mark by one")
	// ErrSequenceOutOfRange is returned when a confirmed sequence is outside
	// the leased range of its batch.
	ErrSequenceOutOfRange = errors.New("batchlease: sequence outside leased range")
	// ErrInjected is the default fault returned by an injected failure.
	ErrInjected = errors.New("batchlease: injected fault")
	// ErrShortWrite asks the fault hook to simulate a partial write: half of
	// the frame is persisted, then the write fails.
	ErrShortWrite = errors.New("batchlease: injected short write")

	errTornTail      = errors.New("batchlease: torn log tail")
	errCorruptRecord = errors.New("batchlease: corrupt log record")
)

// FaultHook decides whether a file-system operation should fail. Returning a
// non-nil error aborts the operation before it runs; returning ErrShortWrite
// at an OpWrite point persists half a frame and then fails.
type FaultHook func(op string) error

// Option configures a Store.
type Option func(*config)

type config struct {
	hook FaultHook
}

// WithFaultHook installs a fault injection hook. It is intended for tests;
// pass nil to disable.
func WithFaultHook(hook FaultHook) Option {
	return func(c *config) { c.hook = hook }
}

// BatchSummary describes the data promised to a batch.
type BatchSummary struct {
	// FirstSeq and LastSeq are the inclusive contiguous sensor sequence
	// number range covered by the batch. Sequence numbers start at 1.
	FirstSeq uint64
	LastSeq  uint64
	// Hash is an application-defined digest over the records in the range.
	// It lets a reopened SDK verify that the batch content is unchanged.
	Hash []byte
}

// Token is the claim ticket returned by Acquire. It binds the batch to the
// startup generation of the SDK process and must be presented on Confirm.
type Token struct {
	BatchID string
	Epoch   uint64
}

// Lease is the persisted description of a claimed batch.
type Lease struct {
	Token   Token
	Summary BatchSummary
}

type leaseEntry struct {
	token    Token
	firstSeq uint64
	lastSeq  uint64
	hash     []byte
}

// Store is the durable batch lease log. A Store must be created with Open and
// is safe for concurrent use.
type Store struct {
	dir string

	mu        sync.Mutex
	f         *os.File
	logSize   int64
	lock      fileLock
	epoch     uint64
	firstSeq  uint64
	confirmed uint64
	leases    map[string]*leaseEntry

	closed  bool
	failed  bool
	failErr error
	hook    FaultHook
}

// Open opens (or creates) the lease log in dir and starts a new startup
// generation. The epoch of every lease acquired from the returned Store is
// bound to that generation; reopening the directory always yields a strictly
// larger epoch.
func Open(dir string, opts ...Option) (*Store, error) {
	if dir == "" {
		return nil, errors.New("batchlease: empty directory")
	}
	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("batchlease: create directory: %w", err)
	}
	lock, err := acquireDirLock(dir)
	if err != nil {
		return nil, err
	}

	s := &Store{
		dir:    dir,
		leases: map[string]*leaseEntry{},
		hook:   cfg.hook,
		lock:   lock,
	}

	// A leftover temporary file means a previous compaction was interrupted
	// before the rename; the committed log is untouched and the temp file is
	// dead. Remove it before doing anything else.
	if err := os.Remove(filepath.Join(dir, tmpName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.cleanupAfterOpenFailure()
		return nil, fmt.Errorf("batchlease: remove stale compaction temp: %w", err)
	}

	logPath := filepath.Join(dir, logName)
	if err := s.before(OpCreate); err != nil {
		s.cleanupAfterOpenFailure()
		return nil, err
	}
	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		s.cleanupAfterOpenFailure()
		return nil, fmt.Errorf("batchlease: open log: %w", err)
	}
	s.f = f

	tornAt, err := s.replayLocked()
	if err != nil {
		s.cleanupAfterOpenFailure()
		return nil, err
	}
	if tornAt >= 0 {
		if err := f.Truncate(tornAt); err != nil {
			s.cleanupAfterOpenFailure()
			return nil, fmt.Errorf("batchlease: truncate torn tail: %w", err)
		}
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			s.cleanupAfterOpenFailure()
			return nil, err
		}
		s.logSize = tornAt
	}

	// Every (re)open starts a new generation and durably records it before
	// the Store is handed out.
	s.epoch++
	if err := s.appendLocked(recEpoch, encodeEpoch(s.epoch)); err != nil {
		s.cleanupAfterOpenFailure()
		return nil, err
	}
	return s, nil
}

func (s *Store) cleanupAfterOpenFailure() {
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
}

// replayLocked reads the whole log and rebuilds in-memory state. It returns
// the byte offset at which a torn (or zero-filled) tail starts, or -1 when
// the log is intact. Hard corruption in the middle of the log is an error.
func (s *Store) replayLocked() (int64, error) {
	data, err := io.ReadAll(s.f)
	if err != nil {
		return -1, fmt.Errorf("batchlease: read log: %w", err)
	}
	r := bytes.NewReader(data)
	var offset, tornAt int64 = 0, -1
	firstFrame := true
	for r.Len() > 0 {
		recType, payload, err := readFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if isTornError(err) {
				// A torn tail is only valid when nothing parseable
				// follows it. If a good frame exists later in the log,
				// committed history was corrupted and truncating it would
				// silently destroy leases: fail hard instead.
				if hasValidFrame(data[offset:]) {
					return -1, fmt.Errorf("%w at offset %d: %v", errCorruptRecord, offset, err)
				}
				tornAt = offset
				break
			}
			return -1, err
		}
		if recType == recSnapshot && !firstFrame {
			return -1, fmt.Errorf("%w: snapshot outside log head", errCorruptRecord)
		}
		if err := s.applyRecordLocked(recType, payload); err != nil {
			return -1, err
		}
		offset = int64(len(data)) - int64(r.Len())
		firstFrame = false
	}
	s.logSize = int64(len(data))
	if tornAt >= 0 {
		s.logSize = tornAt
	}
	return tornAt, nil
}

// hasValidFrame reports whether region contains any fully verifiable frame
// at any offset. A torn tail is the last thing in the log, so finding a good
// frame after a damaged one proves hard mid-log corruption.
func hasValidFrame(region []byte) bool {
	for start := 0; start+frameOverhead <= len(region); start++ {
		if region[start] != frameMagic0 || region[start+1] != frameMagic1 {
			continue
		}
		if _, _, err := readFrame(bytes.NewReader(region[start:])); err == nil {
			return true
		}
	}
	return false
}

func isTornError(err error) bool {
	return errors.Is(err, errTornTail) ||
		errors.Is(err, errFrameMagic) ||
		errors.Is(err, errFrameCRC) ||
		errors.Is(err, errFrameTrailer)
}

func (s *Store) applyRecordLocked(recType byte, payload []byte) error {
	switch recType {
	case recEpoch:
		epoch, err := decodeEpoch(payload)
		if err != nil {
			return err
		}
		if epoch > s.epoch {
			s.epoch = epoch
		}
	case recLease:
		tok, firstSeq, lastSeq, hash, err := decodeLease(payload)
		if err != nil {
			return err
		}
		if firstSeq < 1 || lastSeq < firstSeq || len(hash) == 0 {
			return fmt.Errorf("%w: invalid lease range", errCorruptRecord)
		}
		if s.firstSeq == 0 {
			s.firstSeq = firstSeq
		}
		if firstSeq < s.firstSeq {
			return fmt.Errorf("%w: lease below first sequence", errCorruptRecord)
		}
		if err := s.checkPersistedOverlapLocked(firstSeq, lastSeq); err != nil {
			return err
		}
		s.leases[tok.BatchID] = &leaseEntry{
			token:    tok,
			firstSeq: firstSeq,
			lastSeq:  lastSeq,
			hash:     append([]byte(nil), hash...),
		}
		if tok.Epoch > s.epoch {
			s.epoch = tok.Epoch
		}
	case recConfirm:
		seq, err := decodeConfirm(payload)
		if err != nil {
			return err
		}
		if seq < s.firstSeq || !s.sequenceLeasedLocked(seq) {
			return fmt.Errorf("%w: confirmation outside leased range", errCorruptRecord)
		}
		if s.confirmed != 0 && seq <= s.confirmed {
			return fmt.Errorf("%w: non-monotonic confirmation", errCorruptRecord)
		}
		s.confirmed = seq
	case recSnapshot:
		epoch, firstSeq, confirmed, leases, err := decodeSnapshot(payload)
		if err != nil {
			return err
		}
		s.epoch = epoch
		s.firstSeq = firstSeq
		s.confirmed = confirmed
		s.leases = make(map[string]*leaseEntry, len(leases))
		for i := range leases {
			le := leases[i]
			if le.firstSeq < 1 || le.lastSeq < le.firstSeq || len(le.hash) == 0 {
				return fmt.Errorf("%w: invalid snapshot lease", errCorruptRecord)
			}
			snapshotLease := le
			s.leases[le.token.BatchID] = &snapshotLease
		}
	default:
		return fmt.Errorf("%w: unknown record type %d", errCorruptRecord, recType)
	}
	return nil
}

func (s *Store) sequenceLeasedLocked(seq uint64) bool {
	for _, le := range s.leases {
		if le.firstSeq <= seq && seq <= le.lastSeq {
			return true
		}
	}
	return false
}

// checkPersistedOverlapLocked validates log contents during replay; an
// overlapping lease on disk means hard corruption.
func (s *Store) checkPersistedOverlapLocked(firstSeq, lastSeq uint64) error {
	for _, le := range s.leases {
		if firstSeq <= le.lastSeq && le.firstSeq <= lastSeq {
			return fmt.Errorf("%w: overlapping leases in log", errCorruptRecord)
		}
	}
	return nil
}

func (s *Store) readyLocked() error {
	switch {
	case s.failed:
		return fmt.Errorf("%w: %v", ErrFailed, s.failErr)
	case s.closed:
		return ErrClosed
	}
	return nil
}

// before runs the fault hook for an operation point.
func (s *Store) before(op string) error {
	if s.hook == nil {
		return nil
	}
	if err := s.hook(op); err != nil {
		return fmt.Errorf("%w: op %s: %v", ErrInjected, op, err)
	}
	return nil
}

// poison marks the store unusable after a durable-write failure. Nothing
// short of reopening can trust further mutations once a write/sync failed,
// because the on-disk state may be ahead of or behind memory.
func (s *Store) poison(op string, err error) error {
	s.failed = true
	s.failErr = err
	return fmt.Errorf("%w: op %s: %v", ErrFailed, op, err)
}

// appendLocked appends one fsynced frame to the main log. Any failure poisons
// the store.
func (s *Store) appendLocked(recType byte, payload []byte) error {
	frame := buildFrame(recType, payload)
	if err := s.before(OpWrite); err != nil {
		return s.poison(OpWrite, err)
	}
	n, err := s.f.Write(frame)
	if err != nil {
		return s.poison(OpWrite, err)
	}
	if n < len(frame) {
		// Real short writes are also treated as a torn tail plus failure.
		return s.poison(OpWrite, ErrShortWrite)
	}
	if err := s.before(OpSync); err != nil {
		return s.poison(OpSync, err)
	}
	if err := s.f.Sync(); err != nil {
		return s.poison(OpSync, err)
	}
	s.logSize += int64(len(frame))
	return nil
}

// Acquire claims a batch and persists the lease before returning. The token
// is bound to the current startup generation and the batch summary; the
// sequence range must not overlap any lease already held.
func (s *Store) Acquire(batchID string, summary BatchSummary) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.readyLocked(); err != nil {
		return Lease{}, err
	}
	if batchID == "" || len(batchID) > 255 {
		return Lease{}, errors.New("batchlease: batch ID must be 1..255 bytes")
	}
	if summary.FirstSeq < 1 || summary.LastSeq < summary.FirstSeq {
		return Lease{}, errors.New("batchlease: invalid sequence range")
	}
	if len(summary.Hash) == 0 {
		return Lease{}, errors.New("batchlease: batch summary hash is required")
	}
	if _, ok := s.leases[batchID]; ok {
		return Lease{}, fmt.Errorf("%w: %s", ErrBatchExists, batchID)
	}
	for _, le := range s.leases {
		if summary.FirstSeq <= le.lastSeq && le.firstSeq <= summary.LastSeq {
			return Lease{}, fmt.Errorf("%w: [%d,%d] conflicts with batch %s [%d,%d]",
				ErrOverlap, summary.FirstSeq, summary.LastSeq,
				le.token.BatchID, le.firstSeq, le.lastSeq)
		}
	}

	tok := Token{BatchID: batchID, Epoch: s.epoch}
	hash := append([]byte(nil), summary.Hash...)
	if err := s.appendLocked(recLease, encodeLease(tok, summary.FirstSeq, summary.LastSeq, hash)); err != nil {
		return Lease{}, err
	}
	s.leases[batchID] = &leaseEntry{
		token:    tok,
		firstSeq: summary.FirstSeq,
		lastSeq:  summary.LastSeq,
		hash:     hash,
	}
	if s.firstSeq == 0 {
		s.firstSeq = summary.FirstSeq
	}
	return Lease{
		Token:   tok,
		Summary: BatchSummary{FirstSeq: summary.FirstSeq, LastSeq: summary.LastSeq, Hash: hash},
	}, nil
}

// Confirm records that the sensor record with seq has been durably uploaded.
// It only accepts the next contiguous sequence after the persisted
// high-water mark, and only a sequence whose batch lease matches token. The
// confirmation is fsynced before Confirm returns.
func (s *Store) Confirm(token Token, seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.readyLocked(); err != nil {
		return err
	}
	le, ok := s.leases[token.BatchID]
	// The token must match the persisted lease exactly. A ticket minted by
	// a previous generation carries a different epoch and is rejected, while
	// the token recovered from the log after reopen remains valid so the SDK
	// can resume the original batch.
	if !ok || le.token != token {
		return fmt.Errorf("%w: batch %s", ErrInvalidToken, token.BatchID)
	}
	if seq < le.firstSeq || seq > le.lastSeq {
		return fmt.Errorf("%w: seq %d not in batch %s [%d,%d]",
			ErrSequenceOutOfRange, seq, token.BatchID, le.firstSeq, le.lastSeq)
	}
	want := s.firstSeq
	if s.confirmed != 0 {
		want = s.confirmed + 1
	}
	if seq != want {
		return fmt.Errorf("%w: got %d, expected %d", ErrNonContiguous, seq, want)
	}
	if err := s.appendLocked(recConfirm, encodeConfirm(seq)); err != nil {
		return err
	}
	s.confirmed = seq
	return nil
}

// Lease returns the persisted lease for a batch, or false if the batch ID is
// unknown.
func (s *Store) Lease(batchID string) (Lease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	le, ok := s.leases[batchID]
	if !ok {
		return Lease{}, false
	}
	return entryToLease(le), true
}

// Identify reports which batch a sensor sequence number was promised to.
// After reopening the SDK this is how already-leased data keeps its original
// batch identity.
func (s *Store) Identify(seq uint64) (batchID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.leases))
	for id := range s.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		le := s.leases[id]
		if le.firstSeq <= seq && seq <= le.lastSeq {
			return id, true
		}
	}
	return "", false
}

func entryToLease(le *leaseEntry) Lease {
	return Lease{
		Token: le.token,
		Summary: BatchSummary{
			FirstSeq: le.firstSeq,
			LastSeq:  le.lastSeq,
			Hash:     append([]byte(nil), le.hash...),
		},
	}
}

// Epoch returns the startup generation of this Store.
func (s *Store) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// Confirmed returns the persisted contiguous confirmation high-water mark
// (0 when nothing has been confirmed).
func (s *Store) Confirmed() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmed
}

// FirstSeq returns the smallest leased sequence number (0 when empty).
func (s *Store) FirstSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstSeq
}

// LogSize returns the current on-disk log size in bytes.
func (s *Store) LogSize() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logSize
}

// Leases returns a snapshot of every persisted lease.
func (s *Store) Leases() []Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.leases))
	for id := range s.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Lease, 0, len(ids))
	for _, id := range ids {
		out = append(out, entryToLease(s.leases[id]))
	}
	return out
}

// Close flushes, syncs and releases the log. A store in failed state can
// still be closed so its file descriptor is released.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.closed = true
	var syncErr error
	if !s.failed {
		if err := s.before(OpSync); err != nil {
			syncErr = s.poison(OpSync, err)
		} else if err := s.f.Sync(); err != nil {
			syncErr = s.poison(OpSync, err)
		}
	}
	if err := s.f.Close(); err != nil && syncErr == nil {
		syncErr = err
	}
	s.f = nil
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
	return syncErr
}
