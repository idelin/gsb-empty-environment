// Package pairlog is a crash-safe append-only log with per-consumer
// acknowledgement cursors and segment retention watermarks.
package pairlog

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	headerSize        = 16
	stateFileName     = "state.json"
	stateTmpFileName  = "state.json.tmp"
	lockFileName      = "flock"
	stateVersion      = 1
	defaultMaxSegment = int64(64 << 20)
	defaultMaxPayload = 1 << 20
)

// FaultPoint identifies a durable operation step that can be interrupted.
type FaultPoint string

const (
	// FaultStateBeforeWrite fires before the temporary state file is created.
	FaultStateBeforeWrite FaultPoint = "state:before_write"
	// FaultStateBeforeSync fires after the state bytes are buffered.
	FaultStateBeforeSync FaultPoint = "state:before_sync"
	// FaultStateBeforeRename fires after the temporary state file is fsynced.
	FaultStateBeforeRename FaultPoint = "state:before_rename"
	// FaultStateBeforeDirSync fires after the state rename, before the
	// containing directory is fsynced.
	FaultStateBeforeDirSync FaultPoint = "state:before_dir_sync"
	// FaultSegmentBeforeWrite fires before a record is appended.
	FaultSegmentBeforeWrite FaultPoint = "segment:before_write"
	// FaultSegmentBeforeSync fires after record bytes are buffered.
	FaultSegmentBeforeSync FaultPoint = "segment:before_sync"
	// FaultReclaimBeforeState fires after old segment files are unlinked and
	// before the new watermark is committed.
	FaultReclaimBeforeState FaultPoint = "reclaim:before_state"
)

// FaultEvent is delivered to the fault hook. Returning an error simulates
// an immediate process crash at that point: the Log is closed and callers
// must reopen the directory to recover.
type FaultEvent struct {
	Point FaultPoint
	Path  string
}

var (
	// ErrClosed means the log is not usable in this process and must be reopened.
	ErrClosed = errors.New("pairlog: log is closed")
	// ErrCorrupt means on-disk data cannot be reconciled.
	ErrCorrupt = errors.New("pairlog: corrupt log data")
	// ErrNotFound means the consumer is not registered.
	ErrNotFound = errors.New("pairlog: consumer not found")
	// ErrAckRegression means an acknowledgement would move a cursor backwards.
	ErrAckRegression = errors.New("pairlog: acknowledgement would regress cursor")
	// ErrAckBeyondRead means the acknowledged offset has not been delivered yet.
	ErrAckBeyondRead = errors.New("pairlog: acknowledgement beyond read range")
	// ErrInvalidOffset means the offset is not a record boundary.
	ErrInvalidOffset = errors.New("pairlog: offset is not a record boundary")
	// ErrInvalidConsumer means the consumer name is invalid.
	ErrInvalidConsumer = errors.New("pairlog: invalid consumer name")
	// ErrPayloadTooLarge means the payload exceeds Options.MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("pairlog: payload too large")
	// ErrInjectedFault wraps an error returned by a fault hook.
	ErrInjectedFault = errors.New("pairlog: injected fault")
)

// Options configures a Log.
type Options struct {
	// MaxSegmentBytes causes rotation when appending another frame would
	// exceed this size. Must be at least headerSize+1.
	MaxSegmentBytes int64
	// MaxPayloadBytes rejects payloads larger than this value.
	MaxPayloadBytes int
	// FaultHook is used by tests to inject crashes at durability boundaries.
	// A nil hook disables injection.
	FaultHook func(FaultEvent) error
}

// Record is one log record delivered to a consumer.
type Record struct {
	// Offset is the byte offset of the record inside the log.
	Offset uint64
	// Next is the end offset of the record; it is used as an acknowledgement.
	Next uint64
	// Payload is the application payload.
	Payload []byte
}

// SegmentInfo describes one on-disk segment.
type SegmentInfo struct {
	Base uint64
	End  uint64
}

// ReclaimResult reports a Reclaim call.
type ReclaimResult struct {
	// Watermark is the new retention watermark (an absolute byte offset).
	Watermark uint64
	// RemovedBases lists bases of segments deleted by this call.
	RemovedBases []uint64
}

type consumerState struct {
	ack      uint64
	readHigh uint64
}

type segmentMeta struct {
	base uint64
	end  uint64
	path string
}

type diskState struct {
	Version    int               `json:"version"`
	Watermark  uint64            `json:"watermark"`
	LogEnd     uint64            `json:"log_end"`
	ActiveBase uint64            `json:"active_base"`
	Consumers  map[string]uint64 `json:"consumers"`
}

// Log is a durable append-only log.
type Log struct {
	mu        sync.Mutex
	dir       string
	opts      Options
	lock      *os.File
	active    *os.File
	segments  []segmentMeta
	consumers map[string]*consumerState
	ends      map[uint64]struct{}

	watermark  uint64
	logEnd     uint64
	activeBase uint64
	activeSize int64
	closed     bool
}

// Open opens or creates a log in dir. Every file used by the log lives in dir.
func Open(dir string, opts Options) (*Log, error) {
	if opts.MaxSegmentBytes <= 0 {
		opts.MaxSegmentBytes = defaultMaxSegment
	}
	if opts.MaxPayloadBytes <= 0 {
		opts.MaxPayloadBytes = defaultMaxPayload
	}
	if opts.MaxSegmentBytes < headerSize+1 {
		return nil, fmt.Errorf("pairlog: MaxSegmentBytes must be >= %d", headerSize+1)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	lockFile, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("pairlog: cannot lock log directory: %w", err)
	}

	l := &Log{
		dir:       dir,
		opts:      opts,
		lock:      lockFile,
		consumers: make(map[string]*consumerState),
		ends:      make(map[uint64]struct{}),
	}
	fail := func(err error) (*Log, error) {
		l.crashCloseLocked()
		os.Remove(l.stateTmpPath())
		return nil, err
	}

	// A stale temporary state file is always discarded: it was never renamed.
	if err := os.Remove(l.stateTmpPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail(err)
	}
	var metas []segmentMeta
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if base, ok := parseSegmentName(ent.Name()); ok {
			metas = append(metas, segmentMeta{base: base, path: filepath.Join(dir, ent.Name())})
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].base < metas[j].base })

	if len(metas) == 0 {
		if _, err := os.Stat(l.statePath()); err == nil {
			return fail(fmt.Errorf("%w: state file exists without segments", ErrCorrupt))
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		empty, err := os.OpenFile(l.segmentPath(0), os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return fail(err)
		}
		empty.Close()
		if err := syncDir(dir); err != nil {
			return fail(err)
		}
		l.segments = []segmentMeta{{base: 0, end: 0, path: l.segmentPath(0)}}
		if err := l.persistLocked(0, 0, 0, map[string]uint64{}); err != nil {
			return fail(err)
		}
		l.active, err = os.OpenFile(l.segmentPath(0), os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return fail(err)
		}
		return l, nil
	}

	st, err := l.loadState()
	if err != nil {
		return fail(err)
	}

	dirty := false
	for i, seg := range metas {
		isLast := i == len(metas)-1
		end, ends, err := scanSegment(seg.path, seg.base, isLast, l.opts.MaxPayloadBytes)
		if err != nil {
			return fail(err)
		}
		seg.end = end
		metas[i] = seg
		if i > 0 && metas[i-1].end != seg.base {
			return fail(fmt.Errorf("%w: segment gap at %d", ErrCorrupt, seg.base))
		}
		if isLast && end != st.LogEnd {
			dirty = true
		}
		for _, boundary := range ends {
			l.ends[boundary] = struct{}{}
		}
	}

	first := metas[0]
	last := metas[len(metas)-1]
	switch {
	case st.Watermark > first.base:
		return fail(fmt.Errorf("%w: watermark %d beyond oldest segment %d", ErrCorrupt, st.Watermark, first.base))
	case st.LogEnd > last.end:
		return fail(fmt.Errorf("%w: durable end %d below committed end %d", ErrCorrupt, last.end, st.LogEnd))
	case st.ActiveBase > last.base:
		return fail(fmt.Errorf("%w: active base %d beyond last segment %d", ErrCorrupt, st.ActiveBase, last.base))
	}
	if st.Watermark < first.base {
		// Crash after unlinking every reclaimed segment but before state commit.
		st.Watermark = first.base
		dirty = true
	}
	if st.LogEnd < last.end || st.ActiveBase < last.base {
		// Durable appends/rotations that happened after the last state commit.
		st.LogEnd = last.end
		st.ActiveBase = last.base
		dirty = true
	}

	l.segments = metas
	l.watermark = st.Watermark
	l.logEnd = st.LogEnd
	l.activeBase = st.ActiveBase
	l.activeSize = int64(last.end - last.base)

	for name, ack := range st.Consumers {
		if err := l.validateAck(ack); err != nil {
			return fail(fmt.Errorf("consumer %q: %w", name, err))
		}
		l.consumers[name] = &consumerState{ack: ack, readHigh: ack}
	}

	if dirty {
		if err := l.persistLocked(l.watermark, l.logEnd, l.activeBase, l.acksSnapshotLocked()); err != nil {
			return fail(err)
		}
	}

	l.active, err = os.OpenFile(last.path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fail(err)
	}
	return l, nil
}

func (l *Log) segmentPath(base uint64) string {
	return filepath.Join(l.dir, segmentName(base))
}

func (l *Log) statePath() string {
	return filepath.Join(l.dir, stateFileName)
}

func (l *Log) stateTmpPath() string {
	return filepath.Join(l.dir, stateTmpFileName)
}

func segmentName(base uint64) string {
	return fmt.Sprintf("segment-%020d.log", base)
}

func parseSegmentName(name string) (uint64, bool) {
	const prefix, suffix = "segment-", ".log"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	middle := name[len(prefix) : len(name)-len(suffix)]
	base, err := strconv.ParseUint(middle, 10, 64)
	if err != nil || fmt.Sprintf("%020d", base) != middle {
		return 0, false
	}
	return base, true
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (l *Log) loadState() (diskState, error) {
	data, err := os.ReadFile(l.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return diskState{}, fmt.Errorf("%w: missing state file", ErrCorrupt)
		}
		return diskState{}, err
	}
	var st diskState
	if err := json.Unmarshal(data, &st); err != nil {
		return diskState{}, fmt.Errorf("%w: invalid state file: %v", ErrCorrupt, err)
	}
	if st.Version != stateVersion {
		return diskState{}, fmt.Errorf("%w: unsupported state version %d", ErrCorrupt, st.Version)
	}
	if st.Consumers == nil {
		st.Consumers = map[string]uint64{}
	}
	return st, nil
}

func scanSegment(path string, base uint64, allowTailRepair bool, maxPayload int) (uint64, []uint64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	repair := func(pos uint64, cause error) (uint64, []uint64, error) {
		if !allowTailRepair {
			return 0, nil, fmt.Errorf("%w: %v", ErrCorrupt, cause)
		}
		if err := f.Truncate(int64(pos)); err != nil {
			return 0, nil, err
		}
		if err := f.Sync(); err != nil {
			return 0, nil, err
		}
		return base + pos, nil, nil
	}

	var ends []uint64
	var pos uint64
	for {
		var header [headerSize]byte
		_, err := io.ReadFull(f, header[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return repair(pos, fmt.Errorf("partial header at %d: %w", base+pos, err))
		}
		offset := binary.BigEndian.Uint64(header[0:8])
		length := binary.BigEndian.Uint32(header[8:12])
		checksum := binary.BigEndian.Uint32(header[12:16])
		if offset != base+pos || length > uint32(maxPayload) {
			return repair(pos, fmt.Errorf("invalid header at %d", base+pos))
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(f, payload); err != nil {
			return repair(pos, fmt.Errorf("partial payload at %d: %w", base+pos, err))
		}
		if crc32.ChecksumIEEE(payload) != checksum {
			return repair(pos, fmt.Errorf("checksum mismatch at %d", base+pos))
		}
		pos += uint64(headerSize) + uint64(length)
		ends = append(ends, base+pos)
	}
	return base + pos, ends, nil
}

func (l *Log) validateAck(ack uint64) error {
	if ack < l.watermark || ack > l.logEnd {
		return fmt.Errorf("%w: cursor %d outside [%d,%d]", ErrCorrupt, ack, l.watermark, l.logEnd)
	}
	if ack != 0 {
		if _, ok := l.ends[ack]; !ok {
			return fmt.Errorf("%w: cursor %d is not a record boundary", ErrCorrupt, ack)
		}
	}
	return nil
}

func (l *Log) acksSnapshotLocked() map[string]uint64 {
	acks := make(map[string]uint64, len(l.consumers))
	for name, c := range l.consumers {
		acks[name] = c.ack
	}
	return acks
}

// persistLocked writes the cursor state atomically: temp file, fsync, rename,
// directory fsync. Fault hooks fire between steps.
func (l *Log) persistLocked(watermark, logEnd, activeBase uint64, acks map[string]uint64) error {
	if err := l.fault(FaultStateBeforeWrite, l.stateTmpPath()); err != nil {
		return err
	}
	st := diskState{
		Version:    stateVersion,
		Watermark:  watermark,
		LogEnd:     logEnd,
		ActiveBase: activeBase,
		Consumers:  acks,
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.OpenFile(l.stateTmpPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := l.fault(FaultStateBeforeSync, l.stateTmpPath()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := l.fault(FaultStateBeforeRename, l.stateTmpPath()); err != nil {
		return err
	}
	if err := os.Rename(l.stateTmpPath(), l.statePath()); err != nil {
		return err
	}
	if err := l.fault(FaultStateBeforeDirSync, l.statePath()); err != nil {
		return err
	}
	return syncDir(l.dir)
}

func (l *Log) fault(point FaultPoint, path string) error {
	if l.opts.FaultHook == nil {
		return nil
	}
	if err := l.opts.FaultHook(FaultEvent{Point: point, Path: path}); err != nil {
		return fmt.Errorf("%w at %s: %v", ErrInjectedFault, point, err)
	}
	return nil
}

// crashCloseLocked releases resources after a simulated crash or fatal error.
// The directory lock is released so the log can be reopened.
func (l *Log) crashCloseLocked() {
	if l.closed {
		return
	}
	l.closed = true
	if l.active != nil {
		l.active.Close()
		l.active = nil
	}
	if l.lock != nil {
		l.lock.Close()
		l.lock = nil
	}
}

// Close releases the log. It is idempotent.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.crashCloseLocked()
	return nil
}

func (l *Log) checkOpenLocked() error {
	if l.closed {
		return ErrClosed
	}
	return nil
}

// Append writes one record and returns its offset. The record is fsynced
// before Append returns.
func (l *Log) Append(payload []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return 0, err
	}
	if len(payload) > l.opts.MaxPayloadBytes {
		return 0, ErrPayloadTooLarge
	}
	frame := int64(headerSize + len(payload))
	if l.activeSize > 0 && l.activeSize+frame > l.opts.MaxSegmentBytes {
		if err := l.rotateLocked(); err != nil {
			l.crashCloseLocked()
			return 0, err
		}
	}
	offset := l.logEnd
	var header [headerSize]byte
	binary.BigEndian.PutUint64(header[0:8], offset)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[12:16], crc32.ChecksumIEEE(payload))

	if err := l.fault(FaultSegmentBeforeWrite, l.segmentPath(l.activeBase)); err != nil {
		l.crashCloseLocked()
		return 0, err
	}
	if _, err := l.active.Write(header[:]); err != nil {
		l.crashCloseLocked()
		return 0, err
	}
	if len(payload) > 0 {
		if _, err := l.active.Write(payload); err != nil {
			l.crashCloseLocked()
			return 0, err
		}
	}
	if err := l.fault(FaultSegmentBeforeSync, l.segmentPath(l.activeBase)); err != nil {
		l.crashCloseLocked()
		return 0, err
	}
	if err := l.active.Sync(); err != nil {
		l.crashCloseLocked()
		return 0, err
	}
	l.logEnd = offset + uint64(frame)
	l.activeSize += frame
	l.segments[len(l.segments)-1].end = l.logEnd
	l.ends[l.logEnd] = struct{}{}
	return offset, nil
}

func (l *Log) rotateLocked() error {
	if l.active != nil {
		l.active.Close()
		l.active = nil
	}
	base := l.logEnd
	path := l.segmentPath(base)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := syncDir(l.dir); err != nil {
		f.Close()
		return err
	}
	l.active = f
	l.activeBase = base
	l.activeSize = 0
	l.segments = append(l.segments, segmentMeta{base: base, end: base, path: path})
	return nil
}

// Read delivers the next record after the consumer's durable cursor.
// It returns io.EOF when the consumer is caught up. Successfully delivered
// records advance the consumer's in-memory read horizon, which bounds what
// Ack will accept.
func (l *Log) Read(consumer string) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return Record{}, err
	}
	c, ok := l.consumers[consumer]
	if !ok {
		return Record{}, ErrNotFound
	}
	if c.readHigh >= l.logEnd {
		return Record{}, io.EOF
	}
	rec, err := l.readAtLocked(c.readHigh)
	if err != nil {
		return Record{}, err
	}
	c.readHigh = rec.Next
	return rec, nil
}

func (l *Log) readAtLocked(offset uint64) (Record, error) {
	var seg *segmentMeta
	for i := range l.segments {
		s := &l.segments[i]
		if offset >= s.base && offset < s.end {
			seg = s
			break
		}
	}
	if seg == nil {
		return Record{}, fmt.Errorf("%w: no segment contains offset %d", ErrCorrupt, offset)
	}
	f, err := os.Open(seg.path)
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	var header [headerSize]byte
	if _, err := f.ReadAt(header[:], int64(offset-seg.base)); err != nil {
		return Record{}, err
	}
	stored := binary.BigEndian.Uint64(header[0:8])
	length := binary.BigEndian.Uint32(header[8:12])
	checksum := binary.BigEndian.Uint32(header[12:16])
	if stored != offset || length > uint32(l.opts.MaxPayloadBytes) {
		return Record{}, fmt.Errorf("%w: invalid header at %d", ErrCorrupt, offset)
	}
	payload := make([]byte, length)
	if _, err := f.ReadAt(payload, int64(offset-seg.base)+headerSize); err != nil {
		return Record{}, err
	}
	if crc32.ChecksumIEEE(payload) != checksum {
		return Record{}, fmt.Errorf("%w: checksum mismatch at %d", ErrCorrupt, offset)
	}
	return Record{Offset: offset, Next: offset + uint64(headerSize) + uint64(length), Payload: payload}, nil
}

// RegisterConsumer adds a consumer whose cursor starts at the current log
// end: it will only read records appended afterwards. Registering an
// existing consumer is a no-op.
func (l *Log) RegisterConsumer(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return err
	}
	if name == "" {
		return ErrInvalidConsumer
	}
	if _, ok := l.consumers[name]; ok {
		return nil
	}
	acks := l.acksSnapshotLocked()
	acks[name] = l.logEnd
	if err := l.persistLocked(l.watermark, l.logEnd, l.activeBase, acks); err != nil {
		l.crashCloseLocked()
		return err
	}
	l.consumers[name] = &consumerState{ack: l.logEnd, readHigh: l.logEnd}
	return nil
}

// UnregisterConsumer removes a consumer durably. Afterwards its cursor no
// longer holds back the retention watermark. Unknown consumers are a no-op.
func (l *Log) UnregisterConsumer(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return err
	}
	if _, ok := l.consumers[name]; !ok {
		return nil
	}
	acks := l.acksSnapshotLocked()
	delete(acks, name)
	if err := l.persistLocked(l.watermark, l.logEnd, l.activeBase, acks); err != nil {
		l.crashCloseLocked()
		return err
	}
	delete(l.consumers, name)
	return nil
}

// Ack durably advances consumer cursors. The batch is atomic: either every
// update is persisted or none is. A cursor only moves forward, and never
// beyond the records the consumer has read in this process. Re-acknowledging
// the durable position is a successful no-op, so retries after a lost
// response never move the cursor backwards.
func (l *Log) Ack(updates map[string]uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	changed := false
	for name, pos := range updates {
		c, ok := l.consumers[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		switch {
		case pos == c.ack:
			// Idempotent retry of an already durable acknowledgement.
		case pos < c.ack:
			return fmt.Errorf("%w: %q at %d, requested %d", ErrAckRegression, name, c.ack, pos)
		case pos > c.readHigh:
			return fmt.Errorf("%w: %q read up to %d, requested %d", ErrAckBeyondRead, name, c.readHigh, pos)
		default:
			if _, ok := l.ends[pos]; !ok {
				return fmt.Errorf("%w: %d", ErrInvalidOffset, pos)
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	acks := l.acksSnapshotLocked()
	for name, pos := range updates {
		if pos > l.consumers[name].ack {
			acks[name] = pos
		}
	}
	if err := l.persistLocked(l.watermark, l.logEnd, l.activeBase, acks); err != nil {
		l.crashCloseLocked()
		return err
	}
	for name, pos := range updates {
		if pos > l.consumers[name].ack {
			l.consumers[name].ack = pos
		}
	}
	return nil
}

// Reclaim deletes sealed segments whose records are acknowledged by every
// registered consumer. With no consumers, every sealed segment is eligible.
// The active segment is never removed. The watermark is committed only after
// the files are unlinked, and on recovery it is raised to the oldest
// surviving segment, so a crash can never make acknowledged data disappear
// or unacknowledged data be collected.
func (l *Log) Reclaim() (ReclaimResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkOpenLocked(); err != nil {
		return ReclaimResult{}, err
	}
	minAck := l.logEnd
	for _, c := range l.consumers {
		if c.ack < minAck {
			minAck = c.ack
		}
	}
	var doomed []segmentMeta
	for _, s := range l.segments[:len(l.segments)-1] {
		if s.end <= minAck {
			doomed = append(doomed, s)
		}
	}
	if len(doomed) == 0 {
		return ReclaimResult{Watermark: l.watermark}, nil
	}
	for _, s := range doomed {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.crashCloseLocked()
			return ReclaimResult{}, err
		}
	}
	if err := l.fault(FaultReclaimBeforeState, ""); err != nil {
		l.crashCloseLocked()
		return ReclaimResult{}, err
	}
	newWatermark := doomed[len(doomed)-1].end
	if err := l.persistLocked(newWatermark, l.logEnd, l.activeBase, l.acksSnapshotLocked()); err != nil {
		l.crashCloseLocked()
		return ReclaimResult{}, err
	}
	l.segments = l.segments[len(doomed):]
	l.watermark = newWatermark
	removed := make([]uint64, len(doomed))
	for i, s := range doomed {
		removed[i] = s.base
	}
	return ReclaimResult{Watermark: newWatermark, RemovedBases: removed}, nil
}

// Watermark returns the durable retention watermark.
func (l *Log) Watermark() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watermark
}

// End returns the offset just after the newest record.
func (l *Log) End() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.logEnd
}

// Cursor returns the durable acknowledgement and the in-memory read horizon
// of a consumer.
func (l *Log) Cursor(consumer string) (ack uint64, readHigh uint64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.consumers[consumer]
	if !ok {
		return 0, 0, false
	}
	return c.ack, c.readHigh, true
}

// Segments lists the on-disk segments in order.
func (l *Log) Segments() []SegmentInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SegmentInfo, len(l.segments))
	for i, s := range l.segments {
		out[i] = SegmentInfo{Base: s.base, End: s.end}
	}
	return out
}
