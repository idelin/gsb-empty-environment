package netsec

import (
	"bytes"
	"sort"
	"sync"
)

// userWindow identifies the scope of the global cross-group cap.
type userWindow struct {
	userID string
	window int64
}

// groupKey identifies the scope of the local per-group cap.
type groupKey struct {
	scope userWindow
	group string
}

// entry is the internal state held for one accepted event.
type entry struct {
	record Record
	digest [digestSize]byte
}

// Selector accumulates records for collection hosts. It keeps the full set of
// accepted (unique) events plus withdrawal tombstones and applies trimming
// deterministically at read time. Keeping every accepted event instead of
// eagerly trimming is what makes independently collected shards mergeable:
// merging shards and then trimming yields exactly the same answer as trimming
// the complete collection.
//
// A Selector is safe for concurrent use.
type Selector struct {
	mu sync.Mutex

	key       []byte
	localCap  int
	globalCap int
	entries   map[string]entry
	withdrawn map[string]struct{}
	// tombstoneOnly holds withdrawal requests for events this shard has not
	// seen. They must survive merging so a withdraw delivered to the "wrong"
	// shard still retracts the event once the shards are combined.
	tombstoneOnly map[string]struct{}
	groups        map[groupKey]map[string]entry
	userWindow    map[userWindow]map[string]entry
}

// NewSelector builds a Selector. key is the HMAC key used for ranking digests,
// localCap is the per (user, window, group) limit K and globalCap is the
// per (user, window) total limit across groups.
func NewSelector(key []byte, localCap, globalCap int) (*Selector, error) {
	if len(key) == 0 {
		return nil, ErrInvalidConfig
	}
	if localCap <= 0 || globalCap <= 0 {
		return nil, ErrInvalidConfig
	}

	copiedKey := make([]byte, len(key))
	copy(copiedKey, key)

	return &Selector{
		key:           copiedKey,
		localCap:      localCap,
		globalCap:     globalCap,
		entries:       make(map[string]entry),
		withdrawn:     make(map[string]struct{}),
		tombstoneOnly: make(map[string]struct{}),
		groups:        make(map[groupKey]map[string]entry),
		userWindow:    make(map[userWindow]map[string]entry),
	}, nil
}

// Ingest accepts one record. It is idempotent: re-delivery of an EventID with
// identical content is a no-op, while the same EventID carrying different
// content is rejected with ErrConflict and the previously accepted record is
// left untouched. A withdrawn EventID stays withdrawn even if a matching copy
// is delivered later, modelling retraction that wins over retransmissions.
func (s *Selector) Ingest(r Record) error {
	if r.EventID == "" {
		return ErrInvalidConfig
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.entries[r.EventID]; ok {
		if !recordsEqual(existing.record, r) {
			return ErrConflict
		}
		return nil
	}

	e := entry{record: r, digest: KeyedDigest(s.key, r)}
	s.entries[r.EventID] = e

	// A withdrawal that arrived before the event itself wins immediately once
	// the event shows up.
	if _, pending := s.tombstoneOnly[r.EventID]; pending {
		delete(s.tombstoneOnly, r.EventID)
		s.withdrawn[r.EventID] = struct{}{}
	}

	gk := groupKey{scope: userWindow{userID: r.UserID, window: r.Window}, group: r.Group}
	groupMembers, ok := s.groups[gk]
	if !ok {
		groupMembers = make(map[string]entry)
		s.groups[gk] = groupMembers
	}
	groupMembers[r.EventID] = e

	uw := gk.scope
	uwMembers, ok := s.userWindow[uw]
	if !ok {
		uwMembers = make(map[string]entry)
		s.userWindow[uw] = uwMembers
	}
	uwMembers[r.EventID] = e

	return nil
}

// Withdraw retracts an event. It returns true when an active event was
// tombstoned by this call and false when the event was unknown or already
// withdrawn. Even when the event is unknown the request is remembered as an
// orphan tombstone and survives merging, so a withdrawal routed to one shard
// still retracts the matching event held by another shard. A tombstone never
// lets a later retransmission resurrect the event.
func (s *Selector) Withdraw(eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.withdrawn[eventID]; ok {
		return false
	}
	if _, ok := s.entries[eventID]; !ok {
		s.tombstoneOnly[eventID] = struct{}{}
		return false
	}

	s.withdrawn[eventID] = struct{}{}
	return true
}

// IsWithdrawn reports whether eventID currently has a withdrawal tombstone,
// including an orphan tombstone left by a withdrawal that preceded the event.
func (s *Selector) IsWithdrawn(eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.withdrawn[eventID]; ok {
		return true
	}
	_, ok := s.tombstoneOnly[eventID]
	return ok
}

// Digest exposes the ranking digest for one accepted EventID. The boolean is
// false when the event is unknown. It lets hosts verify ordering independently.
func (s *Selector) Digest(eventID string) ([digestSize]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[eventID]
	return e.digest, ok
}

// Len returns the number of accepted unique events, including withdrawn ones.
func (s *Selector) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Trim applies the two-level cap to the accumulated state and returns the
// retained records together with every excluded record and its reason.
//
// Within each (user, window, group) the K records with the smallest keyed
// digests survive (ReasonLocalCap for the rest). Survivors then compete across
// all groups of their (user, window) pair for the global cap; losers get
// ReasonGlobalCap. Withdrawn events are reported separately and never occupy a
// cap slot.
func (s *Selector) Trim() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return trimLocked(s.entries, s.withdrawn, s.groups, s.userWindow, s.localCap, s.globalCap)
}

// Clone returns a deep, independent copy of the selector.
func (s *Selector) Clone() *Selector {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := &Selector{
		key:           append([]byte(nil), s.key...),
		localCap:      s.localCap,
		globalCap:     s.globalCap,
		entries:       make(map[string]entry, len(s.entries)),
		withdrawn:     make(map[string]struct{}, len(s.withdrawn)),
		tombstoneOnly: make(map[string]struct{}, len(s.tombstoneOnly)),
		groups:        make(map[groupKey]map[string]entry, len(s.groups)),
		userWindow:    make(map[userWindow]map[string]entry, len(s.userWindow)),
	}

	for id, e := range s.entries {
		c.entries[id] = e
	}
	for id := range s.withdrawn {
		c.withdrawn[id] = struct{}{}
	}
	for id := range s.tombstoneOnly {
		c.tombstoneOnly[id] = struct{}{}
	}
	for gk, members := range s.groups {
		copied := make(map[string]entry, len(members))
		for id, e := range members {
			copied[id] = e
		}
		c.groups[gk] = copied
	}
	for uw, members := range s.userWindow {
		copied := make(map[string]entry, len(members))
		for id, e := range members {
			copied[id] = e
		}
		c.userWindow[uw] = copied
	}

	return c
}

// TrimRecords is the stateless form of trimming: it ranks the supplied records
// with key and applies localCap (K per user/window/group) and globalCap (total
// per user/window). It is convenient for one-shot callers and serves as the
// reference implementation that shard merging must match.
func TrimRecords(key []byte, records []Record, localCap, globalCap int) (Result, error) {
	if len(key) == 0 || localCap <= 0 || globalCap <= 0 {
		return Result{}, ErrInvalidConfig
	}

	entries := make(map[string]entry, len(records))
	groups := make(map[groupKey]map[string]entry)
	userWindows := make(map[userWindow]map[string]entry)

	for _, r := range records {
		if r.EventID == "" {
			return Result{}, ErrInvalidConfig
		}
		if existing, ok := entries[r.EventID]; ok {
			if !recordsEqual(existing.record, r) {
				return Result{}, ErrConflict
			}
			continue
		}

		e := entry{record: r, digest: KeyedDigest(key, r)}
		entries[r.EventID] = e

		gk := groupKey{scope: userWindow{userID: r.UserID, window: r.Window}, group: r.Group}
		members, ok := groups[gk]
		if !ok {
			members = make(map[string]entry)
			groups[gk] = members
		}
		members[r.EventID] = e

		uwMembers, ok := userWindows[gk.scope]
		if !ok {
			uwMembers = make(map[string]entry)
			userWindows[gk.scope] = uwMembers
		}
		uwMembers[r.EventID] = e
	}

	return trimLocked(entries, map[string]struct{}{}, groups, userWindows, localCap, globalCap), nil
}

// trimLocked contains the deterministic two-level trimming algorithm. It never
// mutates its inputs.
func trimLocked(
	entries map[string]entry,
	withdrawn map[string]struct{},
	groups map[groupKey]map[string]entry,
	userWindows map[userWindow]map[string]entry,
	localCap, globalCap int,
) Result {
	activeByGroup := make(map[groupKey][]entry)
	activeByUW := make(map[userWindow][]entry)
	tombstones := make([]entry, 0, len(withdrawn))

	// Withdrawn events are tombstones: they never compete for cap slots.
	for id, e := range entries {
		if _, gone := withdrawn[id]; gone {
			tombstones = append(tombstones, e)
			continue
		}
		gk := groupKey{
			scope: userWindow{userID: e.record.UserID, window: e.record.Window},
			group: e.record.Group,
		}
		activeByGroup[gk] = append(activeByGroup[gk], e)
		activeByUW[gk.scope] = append(activeByUW[gk.scope], e)
	}

	localSurvivors := make(map[string]entry)
	dropped := make([]DroppedRecord, 0)

	// Local cap: among active events keep the K smallest digests per group.
	for _, groupEntries := range activeByGroup {
		sortEntriesInPlace(groupEntries)
		for i, e := range groupEntries {
			if i < localCap {
				localSurvivors[e.record.EventID] = e
				continue
			}
			dropped = append(dropped, DroppedRecord{Record: e.record, Reason: ReasonLocalCap})
		}
	}

	// Global cap: local survivors across all groups of one (user, window)
	// compete against each other for the total budget.
	kept := make([]entry, 0)
	for _, uwEntries := range activeByUW {
		survivors := make([]entry, 0, len(uwEntries))
		for _, e := range uwEntries {
			if _, ok := localSurvivors[e.record.EventID]; ok {
				survivors = append(survivors, e)
			}
		}
		sortEntriesInPlace(survivors)
		for i, e := range survivors {
			if i < globalCap {
				kept = append(kept, e)
				continue
			}
			dropped = append(dropped, DroppedRecord{Record: e.record, Reason: ReasonGlobalCap})
		}
	}

	sortEntriesInPlace(kept)
	sort.Slice(tombstones, func(i, j int) bool {
		return tombstones[i].record.EventID < tombstones[j].record.EventID
	})
	sort.Slice(dropped, func(i, j int) bool {
		return dropped[i].Record.EventID < dropped[j].Record.EventID
	})

	result := Result{
		Kept:      make([]Record, 0, len(kept)),
		Dropped:   dropped,
		Withdrawn: make([]Record, 0, len(tombstones)),
	}
	for _, e := range kept {
		result.Kept = append(result.Kept, e.record)
	}
	for _, e := range tombstones {
		result.Withdrawn = append(result.Withdrawn, e.record)
	}

	return result
}

func sortEntriesInPlace(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		if cmp := bytes.Compare(entries[i].digest[:], entries[j].digest[:]); cmp != 0 {
			return cmp < 0
		}
		return entries[i].record.EventID < entries[j].record.EventID
	})
}

// recordsEqual compares every content field. Paylods are compared byte-wise;
// nil and empty payloads are treated as equal.
func recordsEqual(a, b Record) bool {
	return a.EventID == b.EventID &&
		a.UserID == b.UserID &&
		a.Window == b.Window &&
		a.Group == b.Group &&
		bytes.Equal(a.Payload, b.Payload)
}
