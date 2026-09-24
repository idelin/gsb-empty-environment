package cache

import (
	"container/list"
	"context"
	"errors"
	"time"
)

const currentSnapshotVersion = 1

var (
	ErrInvalidCapacity   = errors.New("cache: capacity must be positive")
	ErrInvalidOption     = errors.New("cache: invalid option")
	ErrEntryTooLarge     = errors.New("cache: entry is larger than capacity")
	ErrInsufficientSpace = errors.New("cache: insufficient releasable capacity")
	ErrZeroLease         = errors.New("cache: lease is not active")
	ErrActiveLease       = errors.New("cache: cannot replace or snapshot cache while a lease is active")
)

type Source interface {
	Load(ctx context.Context, key string) (string, error)
}

type SourceFunc func(ctx context.Context, key string) (string, error)

func (f SourceFunc) Load(ctx context.Context, key string) (string, error) {
	return f(ctx, key)
}

type CostFunc func(key, value string) int64

func physicalBytes(key, value string) int64 {
	return int64(len(key) + len(value))
}

type Outcome string

const (
	Hit  Outcome = "hit"
	Miss Outcome = "miss"
)

type Result struct {
	Outcome    Outcome
	Segment    string
	FromGhost  bool
	LoadedAt   time.Time
	ExpiresAt  time.Time
}

type Stats struct {
	Hits            int64
	Misses          int64
	GhostHits       int64
	Loads            int64
	LoadErrors       int64
	ColdEvictions    int64
	HotEvictions     int64
	GhostEvictions   int64
	Deletes          int64
	ExpiredRemovals  int64
	LiveBytes        int64
	BorrowedBytes    int64
	PhysicalBytes     int64
	PeakLiveBytes     int64
	PeakBorrowedBytes int64
	PeakPhysicalBytes int64
}

type Options struct {
	Capacity       int64
	HotFraction    float64
	GhostCapacity  int64
	Source         Source
	Now            func() time.Time
	LeaseTTL       time.Duration
	Cost           CostFunc
}

func (o Options) withDefaults() (Options, error) {
	if o.Capacity <= 0 {
		return o, ErrInvalidCapacity
	}
	if o.Source == nil {
		return o, errors.Join(ErrInvalidOption, errors.New("cache: source is required"))
	}
	if o.HotFraction == 0 {
		o.HotFraction = 0.5
	}
	if o.HotFraction < 0 || o.HotFraction > 1 {
		return o, errors.Join(ErrInvalidOption, errors.New("cache: HotFraction must be between 0 and 1"))
	}
	if o.GhostCapacity == 0 {
		o.GhostCapacity = o.Capacity
	}
	if o.GhostCapacity < 0 {
		return o, errors.Join(ErrInvalidOption, errors.New("cache: GhostCapacity cannot be negative"))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.LeaseTTL < 0 {
		return o, errors.Join(ErrInvalidOption, errors.New("cache: LeaseTTL cannot be negative"))
	}
	if o.Cost == nil {
		o.Cost = physicalBytes
	}
	return o, nil
}

type entry struct {
	key      string
	value    string
	cost     int64
	refs     int
	expires  time.Time
}

type ghost struct {
	key  string
	cost int64
}

type Lease struct {
	Key      string
	Value    string
	Acquired time.Time
	Expires  time.Time

	released bool
	release  func() error
}

func (l *Lease) Release() error {
	if l == nil || l.release == nil || l.released {
		return ErrZeroLease
	}
	l.released = true
	return l.release()
}

func (l *Lease) Expired(now time.Time) bool {
	return !l.Expires.IsZero() && !now.Before(l.Expires)
}

type SnapshotEntry struct {
	Key     string    `json:"key"`
	Cost    int64     `json:"cost"`
	Expires time.Time `json:"expires,omitempty"`
}

type SnapshotGhost struct {
	Key  string `json:"key"`
	Cost int64  `json:"cost"`
}

type Snapshot struct {
	Version        int             `json:"version"`
	Kind           string          `json:"kind"`
	Capacity       int64           `json:"capacity"`
	HotCapacity    int64           `json:"hot_capacity,omitempty"`
	GhostCapacity  int64           `json:"ghost_capacity,omitempty"`
	Cold           []SnapshotEntry `json:"cold,omitempty"`
	Hot            []SnapshotEntry `json:"hot,omitempty"`
	Entries        []SnapshotEntry `json:"entries,omitempty"`
	Ghosts         []SnapshotGhost `json:"ghosts,omitempty"`
}

type baseMemory struct {
	liveBytes     int64
	borrowedBytes int64
	peakLive      int64
	peakBorrowed  int64
	peakPhysical  int64
	borrowed      map[*entry]struct{}
}

func newBaseMemory() baseMemory {
	return baseMemory{borrowed: make(map[*entry]struct{})}
}

func (b *baseMemory) physical() int64 {
	return b.liveBytes + b.borrowedBytes
}

func (b *baseMemory) recordPeak() {
	if b.liveBytes > b.peakLive {
		b.peakLive = b.liveBytes
	}
	if b.borrowedBytes > b.peakBorrowed {
		b.peakBorrowed = b.borrowedBytes
	}
	if physical := b.physical(); physical > b.peakPhysical {
		b.peakPhysical = physical
	}
}

func (b *baseMemory) detach(e *entry) {
	b.liveBytes -= e.cost
	if e.refs > 0 {
		b.borrowed[e] = struct{}{}
		b.borrowedBytes += e.cost
	}
}

func (b *baseMemory) release(e *entry) {
	if _, ok := b.borrowed[e]; ok {
		delete(b.borrowed, e)
		b.borrowedBytes -= e.cost
	}
	e.value = ""
}

func (b *baseMemory) activeBorrows() bool {
	return len(b.borrowed) != 0
}

func oldestToNewest(l *list.List) []*entry {
	out := make([]*entry, 0, l.Len())
	for ele := l.Back(); ele != nil; ele = ele.Prev() {
		out = append(out, ele.Value.(*entry))
	}
	return out
}

func newestToOldest(l *list.List) []*entry {
	out := make([]*entry, 0, l.Len())
	for ele := l.Front(); ele != nil; ele = ele.Next() {
		out = append(out, ele.Value.(*entry))
	}
	return out
}
