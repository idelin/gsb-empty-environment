// Package paircache implements size-aware caching policies (2Q and a plain
// LRU baseline) with byte-accounted capacity, read leases, injectable data
// source / clock / failure hooks, and serializable policy metadata.
//
// The package never claims 2Q beats LRU on every workload; the bundled
// tracebench command prints both policies side by side, including traces
// where plain LRU is equal or better.
package paircache

import (
	"errors"
	"sync"
	"time"
)

// ErrNoLoader is returned on a miss when no Loader is configured.
var ErrNoLoader = errors.New("paircache: miss with no loader configured")

// Loader fetches a value from the backing data source. It is injectable so
// tests can simulate slow or failing backends without any remote dependency.
type Loader func(key string) ([]byte, error)

// Options configures a cache. Zero-value fields get sane defaults.
type Options struct {
	// CapacityBytes is the fixed logical capacity, counted in bytes of
	// resident entry payloads (variable-length entries are supported).
	CapacityBytes int64
	// Loader is the backing data source used on misses. May be nil if the
	// caller only uses Add.
	Loader Loader
	// Now supplies the clock; injectable for deterministic tests.
	Now func() time.Time
	// OnFree is called when an entry's memory is actually released, i.e.
	// after eviction AND after all outstanding leases are released.
	OnFree func(key string, size int64)
	// RecentRatio is the fraction of capacity reserved for first-touch
	// entries (2Q only). Defaults to 0.25.
	RecentRatio float64
	// GhostLimit bounds the number of ghost (key-only) records (2Q only).
	// Defaults to 1024. Ghost records are reclaimed FIFO, so deletes and
	// evictions cannot grow metadata without bound.
	GhostLimit int
}

func (o Options) withDefaults() Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RecentRatio <= 0 || o.RecentRatio >= 1 {
		o.RecentRatio = 0.25
	}
	if o.GhostLimit <= 0 {
		o.GhostLimit = 1024
	}
	return o
}

// entry is a cached value plus bookkeeping shared by both policies.
type entry struct {
	key     string
	val     []byte // nil for restored metadata-only entries until reloaded
	size    int64
	refs    int  // outstanding leases
	evicted bool // removed from policy structures; freed once refs == 0
	freed   bool
	at      time.Time // last access, from the injected clock
}

// Stats are directly comparable between the two policies.
type Stats struct {
	Hits              int64
	Misses            int64
	GhostHits         int64 // 2Q only: miss that found a ghost record (promotion)
	Loads             int64
	LoadErrors        int64
	Evictions         int64
	ResidentEntries   int64
	GhostEntries      int64
	ResidentBytes     int64
	PhysicalBytes     int64
	PeakPhysicalBytes int64
}

// Lease pins an entry in memory. Eviction of a leased entry is logical only:
// the bytes are released when the last lease is Released.
type Lease struct {
	e       *entry
	release func()
	once    sync.Once
}

// Value returns the cached payload. It stays valid until Release.
func (l *Lease) Value() []byte { return l.e.val }

// Size is the physical payload size in bytes.
func (l *Lease) Size() int64 { return l.e.size }

// Release drops the lease. It is idempotent.
func (l *Lease) Release() { l.once.Do(l.release) }

// accounting tracks physical occupancy: resident bytes plus evicted but
// still-leased bytes. PhysicalBytes may exceed CapacityBytes while leases
// are held; that is exactly what PeakPhysicalBytes measures.
type accounting struct {
	phys   int64
	peak   int64
	onFree func(key string, size int64)
}

func (a *accounting) alloc(size int64) {
	a.phys += size
	if a.phys > a.peak {
		a.peak = a.phys
	}
}

func (a *accounting) adjust(delta int64) {
	a.phys += delta
	if a.phys > a.peak {
		a.peak = a.phys
	}
}

// free releases e's memory if it is evicted and no longer leased.
func (a *accounting) free(e *entry) {
	if e.evicted && e.refs == 0 && !e.freed {
		e.freed = true
		a.phys -= e.size
		if a.onFree != nil {
			a.onFree(e.key, e.size)
		}
	}
}
