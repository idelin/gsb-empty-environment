// Package rrset provides a trust-aware, whole-set DNS resource record set
// cache for security-validating resolver hosts.
//
// A resource record set is cached and expired only as a whole: the cache
// never splits a set into subsets, because a subset may no longer match the
// RRSIG that validated the original set. The set key is the owner name, RR
// type and class. The whole set expires at the minimum TTL found in the
// set, and updates atomically replace the entire cached set while
// preserving the validation level invariant: a lower-trust result never
// displaces a still-valid higher-trust set.
package rrset

import (
	"errors"
	"sync"
	"time"
)

// ValidationLevel is the DNSSEC validation state attached to a cached set.
// While a cached set is still valid, a higher level always wins over a
// lower one.
type ValidationLevel int

const (
	// LevelInsecure is data that was not DNSSEC-validated.
	LevelInsecure ValidationLevel = iota
	// LevelSecure is data validated against its RRSIG chain of trust.
	LevelSecure
)

// String returns a human-readable name for the validation level.
func (l ValidationLevel) String() string {
	switch l {
	case LevelSecure:
		return "secure"
	case LevelInsecure:
		return "insecure"
	default:
		return "unknown"
	}
}

// Record is a single resource record. TTL is the record's own time to live
// in seconds.
type Record struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  string
}

// Key identifies a resource record set: owner name, RR type and class.
type Key struct {
	Name  string
	Type  uint16
	Class uint16
}

// KeyOf returns the set key a record belongs to.
func KeyOf(r Record) Key {
	return Key{Name: r.Name, Type: r.Type, Class: r.Class}
}

var (
	// ErrEmptySet is returned when storing a set with no records.
	ErrEmptySet = errors.New("rrset: empty record set")
	// ErrMixedSet is returned when the records do not all share one
	// name/type/class key; the cache refuses to split or merge sets.
	ErrMixedSet = errors.New("rrset: records do not share one name/type/class key")
)

// Set is an immutable view of a cached resource record set.
type Set struct {
	Key     Key
	Records []Record // deduplicated; TTLs clamped to the whole-set TTL
	Level   ValidationLevel
	Expires time.Time
}

// TTL returns the remaining whole-set lifetime at now. It is zero or
// negative once the set has expired.
func (s Set) TTL(now time.Time) time.Duration {
	return s.Expires.Sub(now)
}

type storedSet struct {
	records []Record
	level   ValidationLevel
	expires time.Time
}

// Cache stores resource record sets keyed by name, type and class. It is
// safe for concurrent use.
type Cache struct {
	now  func() time.Time
	mu   sync.RWMutex
	sets map[Key]storedSet
}

// NewCache returns an empty cache. now supplies the current time; a nil
// clock uses time.Now. Tests pass a fake clock to control expiry.
func NewCache(now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{now: now, sets: make(map[Key]storedSet)}
}

// Store atomically replaces the cached set for the records' key and
// reports whether the set was installed.
//
// The whole set expires at the minimum TTL among its records, and stored
// records have their TTLs clamped to that minimum so any answer built from
// the cache stays consistent with the validated set. A set whose minimum
// TTL is zero is uncacheable (RFC 2181 section 8): it is not installed and
// never displaces an existing entry. A lower-trust update never displaces
// a still-valid higher-trust set; once the cached set has expired, any
// trust level may replace it.
func (c *Cache) Store(records []Record, level ValidationLevel) (bool, error) {
	key, cleaned, minTTL, err := normalize(records)
	if err != nil {
		return false, err
	}
	if minTTL == 0 {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if cur, ok := c.sets[key]; ok && now.Before(cur.expires) && cur.level > level {
		return false, nil
	}
	for i := range cleaned {
		cleaned[i].TTL = minTTL
	}
	c.sets[key] = storedSet{
		records: cleaned,
		level:   level,
		expires: now.Add(time.Duration(minTTL) * time.Second),
	}
	return true, nil
}

// Get returns the cached set for key. The second result is false when the
// set is absent or expired; expired sets are evicted lazily. The returned
// records are a copy and may be used without holding the cache lock.
func (c *Cache) Get(key Key) (Set, bool) {
	c.mu.RLock()
	cur, ok := c.sets[key]
	c.mu.RUnlock()
	if !ok {
		return Set{}, false
	}
	if !c.now().Before(cur.expires) {
		c.mu.Lock()
		if cur, ok := c.sets[key]; ok && !c.now().Before(cur.expires) {
			delete(c.sets, key)
		}
		c.mu.Unlock()
		return Set{}, false
	}
	records := make([]Record, len(cur.records))
	copy(records, cur.records)
	return Set{
		Key:     key,
		Records: records,
		Level:   cur.level,
		Expires: cur.expires,
	}, true
}

// Len reports the number of sets currently held by the cache.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.sets)
}

// normalize validates that all records share one key, removes duplicate
// records (identical name, type, class and data), and returns the minimum
// TTL in the set.
func normalize(records []Record) (Key, []Record, uint32, error) {
	if len(records) == 0 {
		return Key{}, nil, 0, ErrEmptySet
	}
	key := KeyOf(records[0])
	minTTL := records[0].TTL
	seen := make(map[string]struct{}, len(records))
	cleaned := make([]Record, 0, len(records))
	for _, r := range records {
		if KeyOf(r) != key {
			return Key{}, nil, 0, ErrMixedSet
		}
		if r.TTL < minTTL {
			minTTL = r.TTL
		}
		if _, dup := seen[r.Data]; dup {
			continue
		}
		seen[r.Data] = struct{}{}
		cleaned = append(cleaned, r)
	}
	return key, cleaned, minTTL, nil
}
