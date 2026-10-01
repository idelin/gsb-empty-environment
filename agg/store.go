package agg

import "sort"

// versionedEvent is an event plus the version range in which it is visible:
// [addedAt, removedAt). removedAt == 0 means still active.
type versionedEvent struct {
	Event
	addedAt   int
	removedAt int
}

// bucket holds the active events of one fixed-width bucket, sorted by
// (TS, ID), plus an incrementally maintained sum. Extrema (min/max) and
// first/last are recomputed from the sorted details whenever needed, so a
// retracted peak is replaced by the next-largest value rather than merely
// adjusting the sum.
type bucket struct {
	events []Event
	sum    float64
}

func (b *bucket) insert(e Event) {
	i := sort.Search(len(b.events), func(i int) bool { return !lessEvent(b.events[i], e) })
	b.events = append(b.events, Event{})
	copy(b.events[i+1:], b.events[i:])
	b.events[i] = e
	b.sum += e.Value
}

func (b *bucket) remove(e Event) bool {
	i := sort.Search(len(b.events), func(i int) bool { return !lessEvent(b.events[i], e) })
	if i < len(b.events) && b.events[i].ID == e.ID && b.events[i].TS == e.TS {
		copy(b.events[i:], b.events[i+1:])
		b.events = b.events[:len(b.events)-1]
		b.sum -= e.Value
		return true
	}
	return false
}

// summary aggregates the bucket's active events. Count and Sum come from the
// incrementally maintained state; extrema are derived from the details.
func (b *bucket) summary() Summary {
	if len(b.events) == 0 {
		return Summary{}
	}
	s := summarizeEvents(b.events)
	s.Sum = b.sum
	return s
}

// eventsInRange returns the active events with lo <= TS < hi. The bucket's
// events are sorted by (TS, ID) so this is two binary searches.
func eventsInRange(events []Event, lo, hi int64) []Event {
	from := sort.Search(len(events), func(i int) bool { return events[i].TS >= lo })
	to := sort.Search(len(events), func(i int) bool { return events[i].TS >= hi })
	return events[from:to]
}

// IngestReport describes the result of ingesting one batch.
type IngestReport struct {
	Version    int // store version after the ingest
	Added      int
	Duplicates int // events skipped because their ID was already active
}

// CorrectionRecord is the audit entry for one correction or deletion. It
// records the buckets and the detail time range the correction accessed.
type CorrectionRecord struct {
	Version     int
	Kind        string // "correct" or "delete"
	Start       int64  // requested interval [Start, End)
	End         int64
	Buckets     []int64 // bucket indexes accessed
	DetailStart int64   // detail range actually scanned, clamped to bucket edges
	DetailEnd   int64
	Scanned     int // detail entries examined
	Removed     int
	Added       int
}

// Store is a versioned fixed-bucket aggregation store.
type Store struct {
	width   int64
	version int
	buckets map[int64]*bucket
	active  map[string]int // active event ID -> index into history
	history []versionedEvent
	audit   []CorrectionRecord
}

// NewStore creates a store with the given bucket width (in TS units).
func NewStore(bucketWidth int64) *Store {
	if bucketWidth <= 0 {
		panic("agg: bucket width must be positive")
	}
	return &Store{
		width:   bucketWidth,
		buckets: make(map[int64]*bucket),
		active:  make(map[string]int),
	}
}

// Version returns the current store version. Every state-changing operation
// increments it; snapshots remain queryable via QueryAt/Details.
func (s *Store) Version() int { return s.version }

// BucketWidth returns the fixed bucket width.
func (s *Store) BucketWidth() int64 { return s.width }

// Audit returns the correction/deletion audit log.
func (s *Store) Audit() []CorrectionRecord {
	out := make([]CorrectionRecord, len(s.audit))
	copy(out, s.audit)
	return out
}

// bucketIndex maps a timestamp to its bucket, with floor semantics for
// negative timestamps.
func (s *Store) bucketIndex(ts int64) int64 {
	q := ts / s.width
	if ts%s.width != 0 && ts < 0 {
		q--
	}
	return q
}

func (s *Store) addActive(e Event) {
	idx := s.bucketIndex(e.TS)
	b := s.buckets[idx]
	if b == nil {
		b = &bucket{}
		s.buckets[idx] = b
	}
	b.insert(e)
	s.active[e.ID] = len(s.history)
	s.history = append(s.history, versionedEvent{Event: e, addedAt: s.version})
}

// Ingest adds a batch of events. Events whose ID is already active (within
// the batch or from an earlier, overlapping batch) are skipped as
// duplicates, so re-delivering an overlapping batch is idempotent.
func (s *Store) Ingest(events []Event) IngestReport {
	s.version++
	rep := IngestReport{Version: s.version}
	seen := make(map[string]struct{}, len(events))
	for _, e := range events {
		if _, dup := seen[e.ID]; dup {
			rep.Duplicates++
			continue
		}
		seen[e.ID] = struct{}{}
		if _, dup := s.active[e.ID]; dup {
			rep.Duplicates++
			continue
		}
		s.addActive(e)
		rep.Added++
	}
	return rep
}

// Correct replaces all active events in [start, end) with the replacement
// events and records an audit entry. The operation is versioned: events
// removed here stay visible to queries at older versions.
func (s *Store) Correct(start, end int64, replacement []Event) CorrectionRecord {
	return s.correct(start, end, replacement, "correct")
}

// Delete removes all active events in [start, end). It is a correction with
// an empty replacement.
func (s *Store) Delete(start, end int64) CorrectionRecord {
	return s.correct(start, end, nil, "delete")
}

func (s *Store) correct(start, end int64, replacement []Event, kind string) CorrectionRecord {
	s.version++
	rec := CorrectionRecord{
		Version:     s.version,
		Kind:        kind,
		Start:       start,
		End:         end,
		DetailStart: start,
		DetailEnd:   end,
	}
	if end > start {
		first := s.bucketIndex(start)
		last := s.bucketIndex(end - 1)
		rec.DetailStart = max64(start, first*s.width)
		rec.DetailEnd = min64(end, (last+1)*s.width)
		for idx := first; idx <= last; idx++ {
			b := s.buckets[idx]
			if b == nil {
				continue
			}
			rec.Buckets = append(rec.Buckets, idx)
			lo := max64(start, idx*s.width)
			hi := min64(end, idx*s.width+s.width)
			// Copy out: doomed shares backing with b.events, and
			// b.remove below mutates that array.
			doomed := append([]Event(nil), eventsInRange(b.events, lo, hi)...)
			rec.Scanned += len(doomed)
			for _, e := range doomed {
				if !b.remove(e) {
					continue
				}
				hiIdx, ok := s.active[e.ID]
				if ok {
					s.history[hiIdx].removedAt = s.version
					delete(s.active, e.ID)
				}
				rec.Removed++
			}
		}
	}
	seen := make(map[string]struct{}, len(replacement))
	for _, e := range replacement {
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		if _, dup := s.active[e.ID]; dup {
			continue
		}
		s.addActive(e)
		rec.Added++
	}
	s.audit = append(s.audit, rec)
	return rec
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
