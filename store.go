package telemetry

import "sort"

// storedEvent is the versioned, append-only record of one event instance.
// Removed == 0 means the event is live. Historical ("as of version v")
// queries replay the log and keep snapshots of older versions usable.
type storedEvent struct {
	Event
	Added   int
	Removed int
}

// AuditEntry records what one mutation touched: the buckets that were
// recomputed and the detail (raw event) range scanned while doing so.
type AuditEntry struct {
	Version     int
	Op          string // "ingest" | "correct" | "delete"
	Start       int64  // affected time range (op range or batch span)
	End         int64
	Buckets     []int64 // bucket indexes recomputed
	DetailLo    int64   // min event TS scanned during recompute
	DetailHi    int64   // max event TS scanned during recompute
	DetailCount int     // live events scanned during recompute
}

// Store keeps the versioned event log plus incrementally maintained
// per-bucket summaries for the current version.
type Store struct {
	width     int64
	version   int
	log       []*storedEvent
	live      map[string]*storedEvent
	byBucket  map[int64]map[string]*storedEvent
	summaries map[int64]*BucketSummary
	audit     []AuditEntry
}

func NewStore(bucketWidth int64) *Store {
	if bucketWidth <= 0 {
		panic("bucket width must be positive")
	}
	return &Store{
		width:     bucketWidth,
		live:      map[string]*storedEvent{},
		byBucket:  map[int64]map[string]*storedEvent{},
		summaries: map[int64]*BucketSummary{},
	}
}

func (s *Store) Version() int { return s.version }

func (s *Store) BucketWidth() int64 { return s.width }

// bucketOf maps a timestamp to its bucket index using floor division, so
// an event exactly on a boundary belongs to the bucket it opens and
// negative timestamps still bucket correctly.
func (s *Store) bucketOf(ts int64) int64 {
	b := ts / s.width
	if ts < 0 && ts%s.width != 0 {
		b--
	}
	return b
}

// Ingest applies a batch of events. Duplicates (same ID already live, or
// repeated inside the batch) are skipped, which makes overlapping batch
// redelivery safe. Returns (added, duplicates).
func (s *Store) Ingest(batch []Event) (added, dups int) {
	s.version++
	affected := map[int64]bool{}
	seen := map[string]bool{}
	var lo, hi int64
	for _, e := range batch {
		if seen[e.ID] {
			dups++
			continue
		}
		seen[e.ID] = true
		if _, ok := s.live[e.ID]; ok {
			dups++
			continue
		}
		s.addLive(e, affected)
		added++
		if added == 1 || e.TS < lo {
			lo = e.TS
		}
		if added == 1 || e.TS > hi {
			hi = e.TS
		}
	}
	s.recompute("ingest", affected, lo, hi)
	return added, dups
}

// Correct is a versioned interval correction: every live event in
// [start, end) is retracted and replaced by the given events (only those
// falling inside the range are applied). The old state remains queryable
// via QueryAsOf with an earlier version.
func (s *Store) Correct(start, end int64, events []Event) {
	s.version++
	affected := s.retract(start, end)
	seen := map[string]bool{}
	for _, e := range events {
		if e.TS < start || e.TS >= end || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		if _, ok := s.live[e.ID]; ok {
			continue
		}
		s.addLive(e, affected)
	}
	s.recompute("correct", affected, start, end)
}

// Delete is a versioned interval deletion: every live event in
// [start, end) is retracted. If the retracted set held the bucket peak,
// the summary is rebuilt from the survivors, so the next-largest value
// surfaces instead of the peak silently lingering or only the sum moving.
func (s *Store) Delete(start, end int64) {
	s.version++
	affected := s.retract(start, end)
	s.recompute("delete", affected, start, end)
}

func (s *Store) addLive(e Event, affected map[int64]bool) {
	se := &storedEvent{Event: e, Added: s.version}
	s.log = append(s.log, se)
	s.live[e.ID] = se
	b := s.bucketOf(e.TS)
	set := s.byBucket[b]
	if set == nil {
		set = map[string]*storedEvent{}
		s.byBucket[b] = set
	}
	set[e.ID] = se
	affected[b] = true
}

func (s *Store) retract(start, end int64) map[int64]bool {
	affected := map[int64]bool{}
	for id, se := range s.live {
		if se.TS >= start && se.TS < end {
			se.Removed = s.version
			delete(s.live, id)
			b := s.bucketOf(se.TS)
			delete(s.byBucket[b], id)
			affected[b] = true
		}
	}
	return affected
}

// recompute rebuilds the summaries of all touched buckets from the live
// detail set and appends an audit entry describing the access.
func (s *Store) recompute(op string, affected map[int64]bool, lo, hi int64) {
	buckets := make([]int64, 0, len(affected))
	for b := range affected {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })

	entry := AuditEntry{Version: s.version, Op: op, Start: lo, End: hi, Buckets: buckets}
	for _, b := range buckets {
		set := s.byBucket[b]
		if len(set) == 0 {
			delete(s.summaries, b)
			delete(s.byBucket, b)
			continue
		}
		sum := &BucketSummary{}
		for _, se := range sortedEvents(set) {
			sum.add(se.Event)
			if entry.DetailCount == 0 || se.TS < entry.DetailLo {
				entry.DetailLo = se.TS
			}
			if entry.DetailCount == 0 || se.TS > entry.DetailHi {
				entry.DetailHi = se.TS
			}
			entry.DetailCount++
		}
		s.summaries[b] = sum
	}
	s.audit = append(s.audit, entry)
}

// sortedEvents flattens a live-event set in (TS, ID) order so that float
// sums and evidence selection are reproducible regardless of map order.
func sortedEvents(set map[string]*storedEvent) []*storedEvent {
	out := make([]*storedEvent, 0, len(set))
	for _, se := range set {
		out = append(out, se)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Audit returns the recorded access log, one entry per mutation.
func (s *Store) Audit() []AuditEntry {
	out := make([]AuditEntry, len(s.audit))
	copy(out, s.audit)
	return out
}

// eventsAsOf replays the log and returns the events live at version v.
func (s *Store) eventsAsOf(v int) []Event {
	var out []Event
	for _, se := range s.log {
		if se.Added <= v && (se.Removed == 0 || se.Removed > v) {
			out = append(out, se.Event)
		}
	}
	return out
}
