package mvcc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrClosed          = errors.New("mvcc: store is closed")
	ErrEmptyKey        = errors.New("mvcc: empty key")
	ErrEmptyBatch      = errors.New("mvcc: batch is empty")
	ErrDuplicateKey    = errors.New("mvcc: duplicate key")
	ErrMissingKey      = errors.New("mvcc: source returned key that was not requested")
	ErrInvalidRevision = errors.New("mvcc: invalid source revision")
	ErrSnapshotExpired = errors.New("mvcc: snapshot has expired")
)

type Stage string

const (
	StageSnapshot Stage = "snapshot"
	StageRead     Stage = "read"
	StageWrite    Stage = "write"
	StageLoad     Stage = "load"
	StageGC       Stage = "gc"
)

type FailFunc func(Stage) error
type ClockFunc func() int64

type Record struct {
	Key      string
	Value    []byte
	Delete   bool
	Revision int64
}

type SourceBatch struct {
	Revision int64
	Records  []Record
}

type SourceFunc func(keys []string) (SourceBatch, error)

func (s *Store) Load(keys []string) (Commit, error) {
	if err := validateKeys(keys); err != nil {
		return Commit{}, err
	}
	if s.source == nil {
		return Commit{}, errors.New("mvcc: no data source configured")
	}
	if err := s.failAt(StageLoad); err != nil {
		return Commit{}, err
	}

	requested := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		requested[key] = struct{}{}
	}
	batch, err := s.source(append([]string(nil), keys...))
	if err != nil {
		return Commit{}, err
	}
	if batch.Revision <= 0 {
		return Commit{}, fmt.Errorf("%w: revision must be positive", ErrInvalidRevision)
	}
	if len(batch.Records) == 0 {
		return Commit{}, ErrEmptyBatch
	}

	seen := make(map[string]struct{}, len(batch.Records))
	entries := make([]entry, 0, len(batch.Records))
	for _, record := range batch.Records {
		if record.Key == "" {
			return Commit{}, ErrEmptyKey
		}
		if _, ok := requested[record.Key]; !ok {
			return Commit{}, fmt.Errorf("%w: %q", ErrMissingKey, record.Key)
		}
		if _, ok := seen[record.Key]; ok {
			return Commit{}, fmt.Errorf("%w: %q", ErrDuplicateKey, record.Key)
		}
		if record.Revision != batch.Revision {
			return Commit{}, fmt.Errorf("%w: record %q revision %d does not match batch revision %d", ErrInvalidRevision, record.Key, record.Revision, batch.Revision)
		}
		if !record.Delete && record.Value == nil {
			return Commit{}, fmt.Errorf("mvcc: source record %q has no value", record.Key)
		}
		seen[record.Key] = struct{}{}
		entries = append(entries, entry{
			value:  cloneBytes(record.Value),
			delete: record.Delete,
			source: batch.Revision,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Commit{}, ErrClosed
	}
	if batch.Revision <= s.sourceRevision {
		return Commit{}, fmt.Errorf("%w: source revision %d is not newer than %d", ErrInvalidRevision, batch.Revision, s.sourceRevision)
	}

	s.version++
	version := s.version
	for i := range entries {
		entries[i].version = version
	}
	for i, record := range batch.Records {
		s.keys[record.Key] = append(s.keys[record.Key], entries[i])
	}
	s.sourceRevision = batch.Revision
	s.collectGC()
	return Commit{Version: version, CommittedAt: s.clock(), SourceRevision: batch.Revision}, nil
}

type Config struct {
	Clock       ClockFunc
	Source      SourceFunc
	Fail        FailFunc
	SnapshotTTL time.Duration
}

type entry struct {
	version int64
	value   []byte
	delete  bool
	source  int64
}

type Store struct {
	mu             sync.Mutex
	clock          ClockFunc
	source         SourceFunc
	fail           FailFunc
	snapshotTTL    time.Duration
	version        int64
	sourceRevision int64
	keys           map[string][]entry
	snapshots      map[*Snapshot]struct{}
	closed         bool
}

type Snapshot struct {
	store     *Store
	version   int64
	createdAt int64
	ttl       time.Duration
	closed    bool
}

type ReadResult struct {
	Version int64
	Values  map[string][]byte
	Missing []string
	Deleted []string
}

type Commit struct {
	Version        int64
	CommittedAt    int64
	SourceRevision int64
}

type GCStats struct {
	EntriesRemoved  int
	ActiveSnapshots int
	Watermark       int64
}

func New(cfg Config) (*Store, error) {
	if cfg.SnapshotTTL < 0 {
		return nil, fmt.Errorf("mvcc: snapshot ttl must not be negative")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	return &Store{
		clock:       clock,
		source:      cfg.Source,
		fail:        cfg.Fail,
		snapshotTTL: cfg.SnapshotTTL,
		keys:        make(map[string][]entry),
		snapshots:   make(map[*Snapshot]struct{}),
	}, nil
}

func (s *Store) failAt(stage Stage) error {
	if s.fail != nil {
		return s.fail(stage)
	}
	return nil
}

func validateKeys(keys []string) error {
	if len(keys) == 0 {
		return ErrEmptyBatch
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			return ErrEmptyKey
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateKey, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	copied := make([]byte, len(value))
	copy(copied, value)
	return copied
}

func (s *Store) Write(records []Record) (Commit, error) {
	if len(records) == 0 {
		return Commit{}, ErrEmptyBatch
	}
	keys := make([]string, len(records))
	for i, record := range records {
		keys[i] = record.Key
	}
	if err := validateKeys(keys); err != nil {
		return Commit{}, err
	}
	for _, record := range records {
		if record.Revision != 0 {
			return Commit{}, fmt.Errorf("%w: write records must not set revision", ErrInvalidRevision)
		}
	}

	entries := make([]entry, len(records))
	for i, record := range records {
		entries[i] = entry{
			value:  cloneBytes(record.Value),
			delete: record.Delete,
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Commit{}, ErrClosed
	}
	if err := s.failAt(StageWrite); err != nil {
		return Commit{}, err
	}

	s.version++
	version := s.version
	for i, record := range records {
		entries[i].version = version
		s.keys[record.Key] = append(s.keys[record.Key], entries[i])
	}
	stats := s.collectGC()
	commit := Commit{Version: version, CommittedAt: s.clock(), SourceRevision: s.sourceRevision}
	_ = stats
	return commit, nil
}

func (s *Store) Snapshot() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if err := s.failAt(StageSnapshot); err != nil {
		return nil, err
	}
	snap := &Snapshot{
		store:     s,
		version:   s.version,
		createdAt: s.clock(),
		ttl:       s.snapshotTTL,
	}
	s.snapshots[snap] = struct{}{}
	return snap, nil
}

func (snap *Snapshot) checkAlive() error {
	if snap.closed {
		return ErrClosed
	}
	if snap.store.closed {
		return ErrClosed
	}
	if snap.expiredLocked() {
		return ErrSnapshotExpired
	}
	return nil
}

func (snap *Snapshot) expiredLocked() bool {
	if snap.ttl == 0 || snap.closed || snap.store.closed {
		return false
	}
	return snap.store.clock() >= snap.createdAt+snap.ttl.Milliseconds()
}

func (snap *Snapshot) Expired() bool {
	store := snap.store
	store.mu.Lock()
	defer store.mu.Unlock()
	return snap.expiredLocked()
}

func (snap *Snapshot) Version() int64   { return snap.version }
func (snap *Snapshot) CreatedAt() int64 { return snap.createdAt }

func (snap *Snapshot) Close() error {
	store := snap.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrClosed
	}
	if snap.closed {
		return nil
	}
	if _, ok := store.snapshots[snap]; !ok {
		return ErrClosed
	}
	delete(store.snapshots, snap)
	snap.closed = true
	return nil
}

func (snap *Snapshot) Read(keys []string) (ReadResult, error) {
	if err := validateKeys(keys); err != nil {
		return ReadResult{}, err
	}
	store := snap.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := snap.checkAlive(); err != nil {
		return ReadResult{}, err
	}
	if err := store.failAt(StageRead); err != nil {
		return ReadResult{}, err
	}
	return readAt(store, snap.version, keys), nil
}

func (s *Store) Read(keys []string) (ReadResult, error) {
	if err := validateKeys(keys); err != nil {
		return ReadResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ReadResult{}, ErrClosed
	}
	if err := s.failAt(StageRead); err != nil {
		return ReadResult{}, err
	}
	return readAt(s, s.version, keys), nil
}

func readAt(store *Store, version int64, keys []string) ReadResult {
	result := ReadResult{
		Version: version,
		Values:  make(map[string][]byte, len(keys)),
	}
	for _, key := range keys {
		chain := store.keys[key]
		index := sort.Search(len(chain), func(i int) bool {
			return chain[i].version > version
		}) - 1
		if index < 0 {
			result.Missing = append(result.Missing, key)
			continue
		}
		found := chain[index]
		if found.delete {
			result.Deleted = append(result.Deleted, key)
		} else {
			result.Values[key] = cloneBytes(found.value)
		}
	}
	return result
}

func (s *Store) GC() (GCStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return GCStats{}, ErrClosed
	}
	if err := s.failAt(StageGC); err != nil {
		return GCStats{}, err
	}
	return s.collectGC(), nil
}

func (s *Store) collectGC() GCStats {
	active := 0
	watermark := int64(1 << 62)
	for snap := range s.snapshots {
		if snap.expiredLocked() {
			continue
		}
		active++
		if snap.version < watermark {
			watermark = snap.version
		}
	}
	if active == 0 {
		watermark = s.version
	}
	stats := GCStats{ActiveSnapshots: active, Watermark: watermark}

	for key, chain := range s.keys {
		start := 0
		for start < len(chain) && chain[start].version <= stats.Watermark {
			start++
		}
		if start > 0 {
			start--
		}
		if start == 0 {
			continue
		}
		retained := make([]entry, len(chain)-start)
		copy(retained, chain[start:])
		stats.EntriesRemoved += start
		s.keys[key] = retained
	}
	return stats
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.keys = make(map[string][]entry)
	for snap := range s.snapshots {
		snap.closed = true
	}
	s.snapshots = make(map[*Snapshot]struct{})
	return nil
}
