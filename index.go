package gin

import (
	"fmt"
	"sort"
	"sync"
)

const (
	DefaultPendingCapacity      = 1024
	DefaultPostingTreeThreshold = 64
	DefaultMaxExactTIDsPerScan  = 0
)

// Config controls index storage and query-time lossy-page conversion.
type Config struct {
	// PendingCapacity is the maximum number of (key, TID) entries kept in the
	// fast-update pending list before writes atomically flush it.
	PendingCapacity int
	// PostingTreeThreshold is the largest posting list size. A larger posting
	// is represented by an ordered posting tree.
	PostingTreeThreshold int
	// MaxExactTIDsPerScan bounds exact TIDs cached by one query. When exceeded,
	// the densest heap pages are converted to lossy page entries. Zero disables
	// lossy conversion.
	MaxExactTIDsPerScan int
}

// DefaultConfig returns the default index configuration.
func DefaultConfig() Config {
	return Config{
		PendingCapacity:      DefaultPendingCapacity,
		PostingTreeThreshold: DefaultPostingTreeThreshold,
		MaxExactTIDsPerScan:  DefaultMaxExactTIDsPerScan,
	}
}

// Option customizes an Index.
type Option func(*Config)

// WithPendingCapacity sets the fast-update pending-list capacity.
func WithPendingCapacity(capacity int) Option {
	return func(config *Config) {
		if capacity > 0 {
			config.PendingCapacity = capacity
		}
	}
}

// WithPostingTreeThreshold sets the size above which a posting list becomes a
// posting tree.
func WithPostingTreeThreshold(threshold int) Option {
	return func(config *Config) {
		if threshold > 0 {
			config.PostingTreeThreshold = threshold
		}
	}
}

// WithMaxExactTIDsPerScan sets the per-query exact-TID memory limit. A value
// less than or equal to zero disables lossy conversion.
func WithMaxExactTIDsPerScan(limit int) Option {
	return func(config *Config) {
		if limit < 0 {
			limit = 0
		}
		config.MaxExactTIDsPerScan = limit
	}
}

// WithMaxExactTIDs is an alias for WithMaxExactTIDsPerScan.
func WithMaxExactTIDs(limit int) Option {
	return WithMaxExactTIDsPerScan(limit)
}

// WithoutLossyPages disables lossy-page conversion.
func WithoutLossyPages() Option {
	return WithMaxExactTIDsPerScan(0)
}

// New creates an empty index.
func New(options ...Option) *Index {
	config := DefaultConfig()
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return newIndex(config)
}

// NewIndex is an alias for New.
func NewIndex(options ...Option) *Index {
	return New(options...)
}

// GINIndex is a descriptive alias for Index.
type GINIndex = Index

// NewGINIndex is an alias for New.
func NewGINIndex(options ...Option) *Index {
	return New(options...)
}

// NewIndexWithConfig creates an index from config. Zero-valued fields receive
// their defaults.
func NewIndexWithConfig(config Config) *Index {
	defaults := DefaultConfig()
	if config.PendingCapacity <= 0 {
		config.PendingCapacity = defaults.PendingCapacity
	}
	if config.PostingTreeThreshold <= 0 {
		config.PostingTreeThreshold = defaults.PostingTreeThreshold
	}
	if config.MaxExactTIDsPerScan == 0 {
		config.MaxExactTIDsPerScan = defaults.MaxExactTIDsPerScan
	}
	if config.MaxExactTIDsPerScan < 0 {
		config.MaxExactTIDsPerScan = 0
	}
	return newIndex(config)
}

func newIndex(config Config) *Index {
	if config.PendingCapacity <= 0 {
		config.PendingCapacity = DefaultPendingCapacity
	}
	if config.PostingTreeThreshold <= 0 {
		config.PostingTreeThreshold = DefaultPostingTreeThreshold
	}
	if config.MaxExactTIDsPerScan < 0 {
		config.MaxExactTIDsPerScan = 0
	}
	return &Index{
		config:   config,
		entries:  NewEntryTree(),
		versions: make(map[TID]uint64),
		live:     make(map[TID]bool),
		active:   make(map[TID]map[string]struct{}),
		dirty:    make(map[string]struct{}),
	}
}

type pendingEntry struct {
	key     string
	tid     TID
	version uint64
}

// Index is a concurrent GIN index with a fast-update pending list.
type Index struct {
	mu sync.RWMutex

	config  Config
	entries *EntryTree
	pending []pendingEntry

	nextVersion uint64
	versions    map[TID]uint64
	live        map[TID]bool
	active      map[TID]map[string]struct{}
	dirty       map[string]struct{}
}

// Config returns a copy of the active configuration.
func (idx *Index) Config() Config {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.config
}

// Insert adds a TID and its keys. Reinserting an existing TID starts a new
// logical version, so stale pending entries cannot resurrect old keys.
func (idx *Index) Insert(tid TID, keys ...string) error {
	return idx.InsertKeys(tid, keys)
}

// Add is an alias for Insert.
func (idx *Index) Add(tid TID, keys ...string) error {
	return idx.Insert(tid, keys...)
}

// Upsert is an alias for Insert.
func (idx *Index) Upsert(tid TID, keys ...string) error {
	return idx.Insert(tid, keys...)
}

// Index is an alias for InsertKeys.
func (idx *Index) Index(tid TID, keys []string) error {
	return idx.InsertKeys(tid, keys)
}

// InsertKeys adds a TID and a key slice.
func (idx *Index) InsertKeys(tid TID, keys []string) error {
	keys = uniqueSortedStrings(keys)

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.live[tid] {
		current := idx.active[tid]
		for key := range current {
			if !containsString(keys, key) {
				idx.dirty[key] = struct{}{}
			}
		}
	}

	version := idx.newVersionLocked()
	idx.versions[tid] = version
	idx.live[tid] = true

	active := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		active[key] = struct{}{}
		idx.pending = append(idx.pending, pendingEntry{key: key, tid: tid, version: version})
	}
	idx.active[tid] = active

	if len(idx.pending) > idx.config.PendingCapacity {
		idx.flushLocked()
	}
	return nil
}

// Delete marks a TID deleted. Its pending entries remain until the next flush
// but are ignored immediately because the TID's version has changed.
func (idx *Index) Delete(tid TID) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !idx.live[tid] {
		return false
	}
	for key := range idx.active[tid] {
		idx.dirty[key] = struct{}{}
	}
	idx.versions[tid] = idx.newVersionLocked()
	idx.live[tid] = false
	delete(idx.active, tid)
	return true
}

// Remove is an alias for Delete.
func (idx *Index) Remove(tid TID) bool {
	return idx.Delete(tid)
}

// Flush atomically drains the pending list into the main entry tree. It also
// purges deleted TIDs from affected postings. Queries observe either the state
// before or after the complete merge, never a partial drain.
func (idx *Index) Flush() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.flushLocked()
}

// FlushPending is an alias for Flush.
func (idx *Index) FlushPending() error {
	return idx.Flush()
}

func (idx *Index) flushLocked() error {
	keys := make(map[string]struct{}, len(idx.dirty)+16)
	for key := range idx.dirty {
		keys[key] = struct{}{}
	}

	pendingByKey := make(map[string][]TID, len(idx.pending))
	for _, entry := range idx.pending {
		keys[entry.key] = struct{}{}
		if entry.version != idx.versions[entry.tid] || !idx.live[entry.tid] {
			continue
		}
		if _, ok := idx.active[entry.tid][entry.key]; !ok {
			continue
		}
		pendingByKey[entry.key] = append(pendingByKey[entry.key], entry.tid)
	}

	for key := range keys {
		var tids []TID
		if posting := idx.entries.Get(key); posting != nil {
			tids = posting.TIDs()
		}
		tids = append(tids, pendingByKey[key]...)

		write := 0
		for _, tid := range tids {
			if _, ok := idx.active[tid][key]; !ok {
				continue
			}
			tids[write] = tid
			write++
		}
		tids = uniqueSortedTIDs(tids[:write])

		if len(tids) == 0 {
			idx.entries.Delete(key)
			continue
		}
		posting := newPosting(idx.config.PostingTreeThreshold)
		posting.setTIDs(tids)
		idx.entries.Set(key, posting)
	}

	idx.pending = nil
	idx.dirty = make(map[string]struct{})
	return nil
}

func (idx *Index) newVersionLocked() uint64 {
	idx.nextVersion++
	if idx.nextVersion == 0 {
		idx.nextVersion = 1
	}
	return idx.nextVersion
}

// Lookup returns the current TIDs for key, merging main postings with the
// pending list. Results are unique and ascending.
func (idx *Index) Lookup(key string) []TID {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	scan := idx.keyTIDsLocked(key)
	return scan.sortedTIDs()
}

// Get is an alias for Lookup.
func (idx *Index) Get(key string) []TID {
	return idx.Lookup(key)
}

// Contains reports whether key currently resolves to tid.
func (idx *Index) Contains(tid TID, key string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if !idx.live[tid] {
		return false
	}
	_, ok := idx.active[tid][key]
	return ok
}

// PostingInfo describes the current state of a key. MainLen excludes pending
// entries; Len includes them.
type PostingInfo struct {
	Key        string
	MainLen    int
	PendingLen int
	Len        int
	IsTree     bool
	Height     int
	TIDs       []TID
}

// PostingInfo returns diagnostic information for key.
func (idx *Index) PostingInfo(key string) (PostingInfo, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	info := PostingInfo{Key: key}
	main := make(map[TID]struct{})
	if posting := idx.entries.Get(key); posting != nil {
		info.MainLen = posting.Len()
		info.IsTree = posting.IsTree()
		if posting.tree != nil {
			info.Height = posting.tree.Height()
		}
		posting.ForEach(func(tid TID) bool {
			if _, ok := idx.active[tid][key]; ok {
				main[tid] = struct{}{}
			}
			return true
		})
	}
	pending := make(map[TID]struct{})
	for _, entry := range idx.pending {
		if entry.key != key || entry.version != idx.versions[entry.tid] || !idx.live[entry.tid] {
			continue
		}
		if _, ok := idx.active[entry.tid][key]; !ok {
			continue
		}
		pending[entry.tid] = struct{}{}
	}
	all := make(map[TID]struct{}, len(main)+len(pending))
	for tid := range main {
		all[tid] = struct{}{}
	}
	for tid := range pending {
		all[tid] = struct{}{}
	}
	info.PendingLen = len(pending)
	info.Len = len(all)
	info.TIDs = sortedTIDSet(all)
	return info, info.MainLen > 0 || info.PendingLen > 0
}

// Entries returns the indexed keys in ascending order.
func (idx *Index) Entries() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.entries.Keys()
}

// Len returns the number of live TIDs.
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	count := 0
	for _, live := range idx.live {
		if live {
			count++
		}
	}
	return count
}

// PendingLen returns the current number of pending (key, TID) entries.
func (idx *Index) PendingLen() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.pending)
}

// IsPending reports whether tid has a pending entry that can still affect a
// query.
func (idx *Index) IsPending(tid TID) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if !idx.live[tid] {
		return false
	}
	for _, entry := range idx.pending {
		if entry.tid == tid && entry.version == idx.versions[tid] {
			return true
		}
	}
	return false
}

// Stats summarizes index state.
type Stats struct {
	Entries             int
	EntryTreeHeight     int
	Postings            int
	PostingTIDs         int
	PostingTrees        int
	PendingEntries      int
	LiveTIDs            int
	DeletedTIDs         int
	DeletedPendingTIDs  int
	MaxExactTIDsPerScan int
}

// Stats returns a point-in-time summary.
func (idx *Index) Stats() Stats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	stats := Stats{
		Entries:             idx.entries.Len(),
		EntryTreeHeight:     idx.entries.Height(),
		PendingEntries:      len(idx.pending),
		MaxExactTIDsPerScan: idx.config.MaxExactTIDsPerScan,
	}
	for _, live := range idx.live {
		if live {
			stats.LiveTIDs++
		} else {
			stats.DeletedTIDs++
		}
	}
	idx.entries.Ascend(func(_ string, posting *Posting) bool {
		stats.Postings++
		stats.PostingTIDs += posting.Len()
		if posting.IsTree() {
			stats.PostingTrees++
		}
		return true
	})
	seenStale := make(map[TID]struct{})
	for _, entry := range idx.pending {
		if entry.version == idx.versions[entry.tid] && idx.live[entry.tid] {
			continue
		}
		if _, seen := seenStale[entry.tid]; !seen {
			seenStale[entry.tid] = struct{}{}
			stats.DeletedPendingTIDs++
		}
	}
	return stats
}

// IsLive reports whether tid is currently indexed.
func (idx *Index) IsLive(tid TID) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.live[tid]
}

// IsDeleted reports whether tid has a logical deletion marker and is not live.
func (idx *Index) IsDeleted(tid TID) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, known := idx.versions[tid]
	return known && !idx.live[tid]
}

// HasPostingTree reports whether key's main posting has been upgraded.
func (idx *Index) HasPostingTree(key string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	posting := idx.entries.Get(key)
	return posting != nil && posting.IsTree()
}

// SetMaxExactTIDsPerScan changes the default per-query exact-TID limit.
func (idx *Index) SetMaxExactTIDsPerScan(limit int) {
	if limit < 0 {
		limit = 0
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.config.MaxExactTIDsPerScan = limit
}

// SetPendingCapacity changes the pending-list capacity. If the new capacity is
// already exceeded, the pending list is flushed atomically.
func (idx *Index) SetPendingCapacity(capacity int) {
	if capacity <= 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.config.PendingCapacity = capacity
	if len(idx.pending) > capacity {
		_ = idx.flushLocked()
	}
}

// Validate checks storage invariants. Pending entries may be stale by design,
// but every main posting must reference an active (TID, key) pair.
func (idx *Index) Validate() error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if err := idx.entries.Validate(); err != nil {
		return err
	}
	var validationErr error
	idx.entries.Ascend(func(key string, posting *Posting) bool {
		tids := posting.TIDs()
		for i, tid := range tids {
			if i > 0 && tids[i-1] >= tid {
				validationErr = fmt.Errorf("posting %q is not strictly sorted at index %d", key, i)
				return false
			}
			if _, ok := idx.active[tid][key]; !ok {
				if _, pendingCleanup := idx.dirty[key]; !pendingCleanup {
					validationErr = fmt.Errorf("posting %q references inactive TID %d without a dirty marker", key, tid)
					return false
				}
			}
		}
		return true
	})
	return validationErr
}

type keyScan struct {
	tids  map[TID]struct{}
	pages map[uint64]struct{}
}

func newKeyScan() *keyScan {
	return &keyScan{
		tids:  make(map[TID]struct{}),
		pages: make(map[uint64]struct{}),
	}
}

func (s *keyScan) add(tid TID) {
	s.tids[tid] = struct{}{}
	s.pages[TIDPage(tid)] = struct{}{}
}

func (s *keyScan) sortedTIDs() []TID {
	if s == nil || len(s.tids) == 0 {
		return nil
	}
	return sortedTIDSet(s.tids)
}

func (idx *Index) keyTIDsLocked(key string) *keyScan {
	scans := idx.keyScansLocked([]string{key})
	if scan := scans[key]; scan != nil {
		return scan
	}
	return newKeyScan()
}

func (idx *Index) keyScansLocked(keys []string) map[string]*keyScan {
	scans := make(map[string]*keyScan, len(keys))
	for _, key := range keys {
		if _, exists := scans[key]; exists {
			continue
		}
		scan := newKeyScan()
		scans[key] = scan
		if posting := idx.entries.Get(key); posting != nil {
			posting.ForEach(func(tid TID) bool {
				if _, ok := idx.active[tid][key]; ok {
					scan.add(tid)
				}
				return true
			})
		}
	}
	for _, entry := range idx.pending {
		scan := scans[entry.key]
		if scan == nil {
			continue
		}
		if entry.version != idx.versions[entry.tid] || !idx.live[entry.tid] {
			continue
		}
		if _, ok := idx.active[entry.tid][entry.key]; !ok {
			continue
		}
		scan.add(entry.tid)
	}
	return scans
}

func (idx *Index) liveTIDsOnPageLocked(page uint64) []TID {
	var result []TID
	for tid, live := range idx.live {
		if live && TIDPage(tid) == page {
			result = append(result, tid)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sortedTIDSet(set map[TID]struct{}) []TID {
	result := make([]TID, 0, len(set))
	for tid := range set {
		result = append(result, tid)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func containsString(sortedValues []string, value string) bool {
	index := sort.SearchStrings(sortedValues, value)
	return index < len(sortedValues) && sortedValues[index] == value
}

func (idx *Index) String() string {
	stats := idx.Stats()
	return fmt.Sprintf("gin(entries=%d live=%d pending=%d trees=%d)", stats.Entries, stats.LiveTIDs, stats.PendingEntries, stats.PostingTrees)
}
