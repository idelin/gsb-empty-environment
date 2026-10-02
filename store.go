package main

const entryOverhead = 16 // fixed metadata bytes accounted per entry

// Entry is a single CRDT record. Tombstones retain the value bytes until
// the garbage collector physically reclaims them.
type Entry struct {
	Key       string
	Value     []byte
	Tombstone bool
	Dot       Dot
}

func (e Entry) size() int64 {
	return int64(len(e.Key) + len(e.Value) + entryOverhead)
}

// Store is a last-writer-wins CRDT map with tombstone deletes.
type Store struct {
	nodeID  string
	clock   VectorClock
	entries map[string]Entry
	mem     int64
}

func NewStore(nodeID string) *Store {
	return &Store{
		nodeID:  nodeID,
		clock:   VectorClock{},
		entries: map[string]Entry{},
	}
}

func (s *Store) tick() uint64 {
	s.clock[s.nodeID]++
	return s.clock[s.nodeID]
}

// Put writes a live entry and returns its dot.
func (s *Store) Put(key string, value []byte) Entry {
	e := Entry{Key: key, Value: value, Dot: Dot{Node: s.nodeID, Clock: s.tick()}}
	s.apply(e)
	return e
}

// Delete turns the key into a tombstone (value retained until GC).
func (s *Store) Delete(key string) Entry {
	prev := s.entries[key]
	e := Entry{
		Key:       key,
		Value:     prev.Value,
		Tombstone: true,
		Dot:       Dot{Node: s.nodeID, Clock: s.tick()},
	}
	s.apply(e)
	return e
}

// apply merges an entry using last-writer-wins; tombstones win ties.
func (s *Store) apply(e Entry) {
	prev, ok := s.entries[e.Key]
	if ok && !wins(e, prev) {
		return
	}
	if ok {
		s.mem -= prev.size()
	}
	s.entries[e.Key] = e
	s.mem += e.size()
}

func wins(a, b Entry) bool {
	if a.Dot.Clock != b.Dot.Clock {
		return a.Dot.Clock > b.Dot.Clock
	}
	if a.Tombstone != b.Tombstone {
		return a.Tombstone
	}
	return a.Dot.Node > b.Dot.Node
}

// Get returns the entry for a key.
func (s *Store) Get(key string) (Entry, bool) {
	e, ok := s.entries[key]
	return e, ok
}

// Entries returns a snapshot of all entries.
func (s *Store) Entries() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	return out
}

// Clock returns a copy of the store's vector clock.
func (s *Store) Clock() VectorClock { return s.clock.Copy() }

// MergeClock folds a remote vector clock into the local one.
func (s *Store) MergeClock(vc VectorClock) { s.clock.Merge(vc) }

// Memory returns the accounted bytes held by the store.
func (s *Store) Memory() int64 { return s.mem }

// GC physically removes tombstones that are strictly older than threshold
// and dominated by the causal stability frontier (i.e. every node in the
// cluster is guaranteed to have already observed the delete).
func (s *Store) GC(frontier VectorClock, threshold uint64) (reclaimed int, freedBytes int64) {
	for k, e := range s.entries {
		if !e.Tombstone || e.Dot.Clock >= threshold {
			continue
		}
		if !frontier.Dominates(e.Dot) {
			continue
		}
		delete(s.entries, k)
		s.mem -= e.size()
		reclaimed++
		freedBytes += e.size()
	}
	return reclaimed, freedBytes
}
