// Package reservoir implements a memory-bounded, stratified reservoir
// sampler for intrusion-detection alert streams.
//
// Each alert class keeps a configurable minimum quota of retained
// records; the remaining capacity is contested by priority derived from
// a keyed hash (HMAC-SHA-256) of the event ID. Repeated event IDs count
// once. Shards produced with the same key merge exactly: merging shards
// yields the same retained set as sampling the union of the original
// de-duplicated events. Rotating the key starts a fresh window so an
// adversary cannot correlate priorities with the previous window.
//
// Memory stays bounded by keeping, per class, only the highest-priority
// distinct candidates that class could ever need: its minimum quota
// plus the shared pool of remaining capacity. With m observed classes
// the total candidate set is at most (m-1)*Capacity - (m-2)*sum(quotas),
// i.e. linear in capacity and independent of stream length. Selection
// itself is a deterministic global rule (quota holders first, then the
// globally highest-priority leftovers), recomputed from that bounded
// candidate set. This avoids the drift of greedy streaming eviction,
// where a high-frequency class could fill every slot before a protected
// class arrived.
package reservoir

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"sort"
)

// ErrInsufficientCapacity is returned when the configured capacity
// cannot hold the sum of the per-class minimum quotas.
var ErrInsufficientCapacity = errors.New("reservoir: capacity smaller than sum of minimum quotas")

// ErrWindowMismatch is returned when merging samplers from different
// sampling windows (different key, window id, or configuration).
var ErrWindowMismatch = errors.New("reservoir: cannot merge samplers from different windows")

// Event is one alert observation handed in by a Go probe.
type Event struct {
	// Class is the alert class/category.
	Class string
	// ID uniquely identifies an event; repeated IDs are deduped.
	ID string
	// Payload is the alert content. It does not influence priority.
	Payload []byte
}

// Record is a retained event together with its sampling metadata.
type Record struct {
	Event    Event
	Priority []byte
}

// Config configures a Sampler.
type Config struct {
	// Capacity is the maximum number of distinct events retained.
	Capacity int
	// MinQuota is the minimum number of records kept per alert class
	// whenever that many distinct events of the class were observed.
	MinQuota map[string]int
}

// Stats reports counters for the current window.
type Stats struct {
	// EventsSeen counts Add calls, including repeated IDs.
	EventsSeen int
	// DistinctIDs is the number of unique event IDs currently buffered
	// (the bounded candidate set, which may be larger than Capacity).
	DistinctIDs int
	// Duplicates counts Add calls whose ID was already known.
	Duplicates int
	// ContentConflicts counts repeated IDs whose payload/class differed.
	ContentConflicts int
	// Evictions counts distinct events dropped from the bounded buffers.
	Evictions int
	// Rejections counts distinct Add calls that did not enter a buffer
	// because they lost to an already-buffered higher-priority event.
	Rejections int
}

// Sampler is a single-window stratified reservoir. It is safe for use
// by a single goroutine; use separate Samplers per shard and Merge to
// combine them concurrently.
type Sampler struct {
	cfg      Config
	key      []byte
	windowID uint64

	// candidates maps class -> distinct buffered entries, ordered from
	// highest to lowest priority. Each class keeps at most classLimit
	// entries, so total buffered entries never exceed Capacity.
	candidates map[string][]*entry
	index      map[string]*entry

	seen      int
	dupes     int
	conflicts int
	evicted   int
	rejected  int
}

type entry struct {
	id       string
	class    string
	payload  []byte
	priority []byte
}

// New validates cfg and creates a sampler for window 0 keyed by key.
func New(cfg Config, key []byte) (*Sampler, error) {
	if cfg.Capacity < 0 {
		return nil, ErrInsufficientCapacity
	}
	quotaSum := 0
	for _, q := range cfg.MinQuota {
		if q < 0 {
			return nil, ErrInsufficientCapacity
		}
		quotaSum += q
	}
	if quotaSum > cfg.Capacity {
		return nil, ErrInsufficientCapacity
	}
	return newSampler(cfg, key, 0), nil
}

func newSampler(cfg Config, key []byte, windowID uint64) *Sampler {
	quotas := make(map[string]int, len(cfg.MinQuota))
	for class, q := range cfg.MinQuota {
		quotas[class] = q
	}
	return &Sampler{
		cfg:        Config{Capacity: cfg.Capacity, MinQuota: quotas},
		key:        append([]byte(nil), key...),
		windowID:   windowID,
		candidates: make(map[string][]*entry),
		index:      make(map[string]*entry),
	}
}

// classLimit is the maximum number of candidates a class may buffer:
// its protected quota plus every globally contested slot.
func (s *Sampler) classLimit(class string) int {
	limit := s.cfg.Capacity
	if q := s.cfg.MinQuota[class]; q > 0 {
		limit = s.cfg.Capacity - sumQuotasExcept(s.cfg.MinQuota, class)
	}
	return limit
}

// Add ingests one event. Repeated IDs are counted but never stored a
// second time; a payload or class mismatch on a repeated ID increments
// the content-conflict counter and keeps the first-seen content.
func (s *Sampler) Add(ev Event) {
	s.seen++
	if existing, ok := s.index[ev.ID]; ok {
		s.dupes++
		if existing.class != ev.Class || !bytes.Equal(existing.payload, ev.Payload) {
			s.conflicts++
		}
		return
	}

	candidate := &entry{
		id:       ev.ID,
		class:    ev.Class,
		payload:  append([]byte(nil), ev.Payload...),
		priority: s.priority(ev.ID),
	}

	buf := s.candidates[ev.Class]
	limit := s.classLimit(ev.Class)
	if limit == 0 {
		s.rejected++
		return
	}
	if len(buf) >= limit {
		// The class buffer is full: only a higher-priority event can
		// displace the current lowest-priority candidate.
		weakest := buf[len(buf)-1]
		if !higher(candidate, weakest) {
			s.rejected++
			return
		}
		buf = removeAt(buf, len(buf)-1)
		delete(s.index, weakest.id)
		s.evicted++
	}

	pos := sort.Search(len(buf), func(i int) bool { return higher(candidate, buf[i]) })
	buf = insertAt(buf, pos, candidate)
	s.candidates[ev.Class] = buf
	s.index[ev.ID] = candidate
}

// Merge folds other into s. Both samplers must use the same key and
// window id and have identical capacity and quotas. The result equals
// sampling the union of the two shards' original de-duplicated events.
func (s *Sampler) Merge(other *Sampler) error {
	if s.windowID != other.windowID || !hmac.Equal(s.key, other.key) {
		return ErrWindowMismatch
	}
	if s.cfg.Capacity != other.cfg.Capacity || !sameQuotas(s.cfg.MinQuota, other.cfg.MinQuota) {
		return ErrWindowMismatch
	}

	// Replay in ascending ID order so first-seen conflict resolution and
	// all counters are reproducible regardless of shard layout.
	var incoming []*entry
	for _, buf := range other.candidates {
		incoming = append(incoming, buf...)
	}
	sort.Slice(incoming, func(i, j int) bool { return incoming[i].id < incoming[j].id })

	for _, e := range incoming {
		ev := Event{Class: e.class, ID: e.id, Payload: append([]byte(nil), e.payload...)}
		if existing, ok := s.index[e.id]; ok {
			s.dupes++
			if existing.class != e.class || !bytes.Equal(existing.payload, e.payload) {
				s.conflicts++
			}
			continue
		}
		s.Add(ev)
	}

	s.seen += other.seen
	s.evicted += other.evicted
	s.rejected += other.rejected
	return nil
}

// RotateKey starts a new sampling window with newKey, clearing every
// buffered record and counter while keeping the same configuration.
// It returns the previous window id; the new id is previous + 1.
func (s *Sampler) RotateKey(newKey []byte) uint64 {
	previous := s.windowID
	fresh := newSampler(s.cfg, newKey, previous+1)
	*s = *fresh
	return previous
}

// Snapshot returns the retained records ordered from highest to lowest
// priority. The returned slices are independent copies.
func (s *Sampler) Snapshot() []Record {
	selected := s.selectWinners()
	records := make([]Record, len(selected))
	for i, e := range selected {
		records[i] = Record{
			Event: Event{
				Class:   e.class,
				ID:      e.id,
				Payload: append([]byte(nil), e.payload...),
			},
			Priority: append([]byte(nil), e.priority...),
		}
	}
	return records
}

// selectWinners applies the deterministic global sampling rule: each
// class contributes its top MinQuota candidates first, then the unused
// capacity goes to the globally highest-priority remaining candidates.
func (s *Sampler) selectWinners() []*entry {
	chosen := make(map[string]bool, s.cfg.Capacity)
	var winners []*entry

	// Classes with a quota are handled in a fixed (sorted) order so the
	// result does not depend on map iteration.
	quotaClasses := make([]string, 0, len(s.cfg.MinQuota))
	for class := range s.cfg.MinQuota {
		quotaClasses = append(quotaClasses, class)
	}
	sort.Strings(quotaClasses)

	for _, class := range quotaClasses {
		q := s.cfg.MinQuota[class]
		buf := s.candidates[class]
		if q > len(buf) {
			q = len(buf)
		}
		for i := 0; i < q; i++ {
			chosen[buf[i].id] = true
			winners = append(winners, buf[i])
		}
	}

	var leftovers []*entry
	for _, buf := range s.candidates {
		for _, e := range buf {
			if !chosen[e.id] {
				leftovers = append(leftovers, e)
			}
		}
	}
	sort.Slice(leftovers, func(i, j int) bool { return higher(leftovers[i], leftovers[j]) })

	slots := s.cfg.Capacity - len(winners)
	for i := 0; i < slots && i < len(leftovers); i++ {
		winners = append(winners, leftovers[i])
	}
	sort.Slice(winners, func(i, j int) bool { return higher(winners[i], winners[j]) })
	return winners
}

// Stats returns a copy of the current window counters.
func (s *Sampler) Stats() Stats {
	return Stats{
		EventsSeen:       s.seen,
		DistinctIDs:      len(s.index),
		Duplicates:       s.dupes,
		ContentConflicts: s.conflicts,
		Evictions:        s.evicted,
		Rejections:       s.rejected,
	}
}

// WindowID identifies the sampling window; it increments on RotateKey.
func (s *Sampler) WindowID() uint64 { return s.windowID }

// Key returns a copy of the current window key.
func (s *Sampler) Key() []byte { return append([]byte(nil), s.key...) }

// Capacity returns the configured maximum number of retained records.
func (s *Sampler) Capacity() int { return s.cfg.Capacity }

func (s *Sampler) priority(id string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id))
	return mac.Sum(nil)
}

// higher reports whether a outranks b: larger HMAC first, then the
// smaller ID as a deterministic tiebreaker.
func higher(a, b *entry) bool {
	if c := bytes.Compare(a.priority, b.priority); c != 0 {
		return c > 0
	}
	return a.id < b.id
}

func sumQuotasExcept(quotas map[string]int, skip string) int {
	total := 0
	for class, q := range quotas {
		if class != skip {
			total += q
		}
	}
	return total
}

func sameQuotas(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for class, qa := range a {
		if qb, ok := b[class]; !ok || qa != qb {
			return false
		}
	}
	return true
}

func insertAt(buf []*entry, pos int, e *entry) []*entry {
	buf = append(buf, nil)
	copy(buf[pos+1:], buf[pos:])
	buf[pos] = e
	return buf
}

func removeAt(buf []*entry, pos int) []*entry {
	copy(buf[pos:], buf[pos+1:])
	buf[len(buf)-1] = nil
	return buf[:len(buf)-1]
}
