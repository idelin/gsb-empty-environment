package forkwatch

import (
	"sort"
	"sync"
)

// Monitor collects verified session messages for one shard and derives fork
// evidence from their hash-chain relationships.
//
// A Monitor is safe for concurrent use. Statuses and conflicts are derived
// from the stored records on every read rather than cached, so importing
// records in any order always converges on the same result.
type Monitor struct {
	mu       sync.RWMutex
	verifier Verifier
	shard    string
	records  map[string]Record
}

// NewMonitor creates a Monitor for the named shard using verifier for all
// signature checks.
func NewMonitor(shard string, verifier Verifier) *Monitor {
	return &Monitor{
		verifier: verifier,
		shard:    shard,
		records:  make(map[string]Record),
	}
}

// Shard returns the shard name supplied at construction.
func (m *Monitor) Shard() string {
	return m.shard
}

// Observe verifies and stores a signed message.
//
// A message that fails signature verification is rejected with
// ErrInvalidSignature and is not stored. A message without a content digest
// is rejected with ErrInvalidDigest. Re-observing a message whose content
// digest is already known is a no-op, so repeated delivery of identical
// snapshots is idempotent.
func (m *Monitor) Observe(msg SignedMessage) error {
	if len(msg.ContentDigest) == 0 {
		return ErrInvalidDigest
	}
	if err := m.verifier.Verify(msg.Payload, msg.Signature); err != nil {
		return ErrInvalidSignature
	}

	rec := Record{
		Generation:    msg.Generation,
		Sequence:      msg.Sequence,
		ParentDigest:  cloneDigest(msg.ParentDigest),
		ContentDigest: cloneDigest(msg.ContentDigest),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.ContentDigest.String()] = rec
	return nil
}

// Status reports the derived status of the record with the given content
// digest. Unknown digests report StatusUnknown.
func (m *Monitor) Status(contentDigest Digest) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.records[contentDigest.String()]
	if !ok {
		return StatusUnknown
	}
	conflicts := evaluateConflicts(m.records)
	if conflictingDigests(conflicts)[rec.ContentDigest.String()] {
		return StatusConflict
	}
	if isRoot(rec) {
		return StatusLinked
	}
	if _, ok := m.records[rec.ParentDigest.String()]; ok {
		return StatusLinked
	}
	return StatusPending
}

// Records returns all stored records, sorted by generation, sequence and
// content digest. Each record's current derived status accompanies it.
func (m *Monitor) Records() []RecordStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conflicts := evaluateConflicts(m.records)
	conflicting := conflictingDigests(conflicts)
	records := sortedRecords(m.records)
	out := make([]RecordStatus, 0, len(records))
	for _, rec := range records {
		status := StatusPending
		switch {
		case conflicting[rec.ContentDigest.String()]:
			status = StatusConflict
		case isRoot(rec) || containsRecord(m.records, rec.ParentDigest):
			status = StatusLinked
		}
		out = append(out, RecordStatus{Record: rec, Status: status})
	}
	return out
}

// Pending returns the records whose parent has not been observed yet and are
// not part of a fork. A pending record becomes linked once its parent is
// observed, or conflicting once a sibling is observed.
func (m *Monitor) Pending() []Record {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conflicts := evaluateConflicts(m.records)
	conflicting := conflictingDigests(conflicts)
	var out []Record
	for _, rec := range sortedRecords(m.records) {
		if conflicting[rec.ContentDigest.String()] || isRoot(rec) {
			continue
		}
		if !containsRecord(m.records, rec.ParentDigest) {
			out = append(out, rec)
		}
	}
	return out
}

// Conflicts returns all detected forks, in a deterministic order.
func (m *Monitor) Conflicts() []Conflict {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sortedConflicts(evaluateConflicts(m.records))
}

// HasConflict reports whether any fork evidence has been observed.
func (m *Monitor) HasConflict() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(evaluateConflicts(m.records)) > 0
}

// Snapshot exports the shard state for transport or persistence. Statuses are
// intentionally not exported: they are derived from records and are
// reconstructed after a merge or reload.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records := sortedRecords(m.records)
	return Snapshot{
		Shard:   m.shard,
		Records: cloneRecords(records),
	}
}

// MergeSnapshot imports records produced by another shard (or a reloaded
// snapshot of this shard).
//
// The merge is a union keyed by content digest, so it is idempotent and
// independent of merge order. Imported records have already passed signature
// verification at their originating shard; snapshots are a trust boundary
// internal to the service. Records with no content digest are skipped.
func (m *Monitor) MergeSnapshot(snap Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mergeLocked(m.records, snap.Records)
	return nil
}

// MergeSnapshots merges several snapshots in one call. It behaves identically
// to repeated [Monitor.MergeSnapshot] calls regardless of snapshot order.
func (m *Monitor) MergeSnapshots(snaps ...Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, snap := range snaps {
		mergeLocked(m.records, snap.Records)
	}
	return nil
}

// MergeSnapshots is a pure helper that merges any number of shard snapshots
// without needing a [Monitor]. Equal inputs in any order produce an equal
// output, and merging an output back into itself changes nothing.
func MergeSnapshots(snaps ...Snapshot) Snapshot {
	merged := make(map[string]Record)
	for _, snap := range snaps {
		mergeLocked(merged, snap.Records)
	}
	return Snapshot{Records: cloneRecords(sortedRecords(merged))}
}

// RecordStatus pairs a stored record with its derived status.
type RecordStatus struct {
	Record Record
	Status Status
}

func mergeLocked(target map[string]Record, records []Record) {
	for _, rec := range records {
		if len(rec.ContentDigest) == 0 {
			continue
		}
		key := rec.ContentDigest.String()
		if _, exists := target[key]; exists {
			continue
		}
		target[key] = Record{
			Generation:    rec.Generation,
			Sequence:      rec.Sequence,
			ParentDigest:  cloneDigest(rec.ParentDigest),
			ContentDigest: cloneDigest(rec.ContentDigest),
		}
	}
}

// forkKey identifies one session position: every message claiming the same
// generation, sequence number and parent digest competes for that position.
// Distinct content digests under the same fork key are fork evidence.
type forkKey struct {
	generation uint64
	sequence   uint64
	parent     string
}

func evaluateConflicts(records map[string]Record) map[forkKey]Conflict {
	// Group competing messages by session position. Root records group under
	// an empty parent key.
	groups := make(map[forkKey]map[string]Record)
	for _, rec := range records {
		key := forkKey{
			generation: rec.Generation,
			sequence:   rec.Sequence,
			parent:     rec.ParentDigest.String(),
		}
		group, ok := groups[key]
		if !ok {
			group = make(map[string]Record)
			groups[key] = group
		}
		group[rec.ContentDigest.String()] = rec
	}

	conflicts := make(map[forkKey]Conflict)
	for key, group := range groups {
		if len(group) < 2 {
			continue
		}

		digests := make([]Digest, 0, len(group))
		var parentDigest Digest
		for _, rec := range group {
			parentDigest = rec.ParentDigest
			digests = append(digests, cloneDigest(rec.ContentDigest))
		}
		sort.Slice(digests, func(i, j int) bool {
			return digests[i].String() < digests[j].String()
		})

		conflicts[key] = Conflict{
			Generation:     key.generation,
			Sequence:       key.sequence,
			ParentDigest:   cloneDigest(parentDigest),
			ContentDigests: digests,
		}
	}
	return conflicts
}

// conflictingDigests indexes every content digest participating in a fork so
// status derivation stays linear in the number of stored records.
func conflictingDigests(conflicts map[forkKey]Conflict) map[string]bool {
	out := make(map[string]bool)
	for _, conflict := range conflicts {
		for _, digest := range conflict.ContentDigests {
			out[digest.String()] = true
		}
	}
	return out
}

func isRoot(rec Record) bool {
	return len(rec.ParentDigest) == 0
}

func containsRecord(records map[string]Record, digest Digest) bool {
	_, ok := records[digest.String()]
	return ok
}

func sortedRecords(records map[string]Record) []Record {
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generation != out[j].Generation {
			return out[i].Generation < out[j].Generation
		}
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ContentDigest.String() < out[j].ContentDigest.String()
	})
	return out
}

func sortedConflicts(conflicts map[forkKey]Conflict) []Conflict {
	out := make([]Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		out = append(out, conflict)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generation != out[j].Generation {
			return out[i].Generation < out[j].Generation
		}
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ParentDigest.String() < out[j].ParentDigest.String()
	})
	return out
}

func cloneDigest(d Digest) Digest {
	if len(d) == 0 {
		return nil
	}
	cp := make(Digest, len(d))
	copy(cp, d)
	return cp
}

func cloneRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for i, rec := range records {
		out[i] = Record{
			Generation:    rec.Generation,
			Sequence:      rec.Sequence,
			ParentDigest:  cloneDigest(rec.ParentDigest),
			ContentDigest: cloneDigest(rec.ContentDigest),
		}
	}
	return out
}
