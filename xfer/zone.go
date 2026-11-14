package xfer

import (
	"sort"
	"sync"
)

// Delta describes one committed zone revision for IXFR journals.
type Delta struct {
	FromSerial uint32
	ToSerial   uint32
	Removed    []Record
	Added      []Record
}

// snapshot is an immutable, self-consistent view of one zone version.
type snapshot struct {
	serial  uint32
	soa     Record
	records []Record // excludes the leading SOA
}

type zoneState struct {
	cur     *snapshot
	journal []Delta // ordered by ToSerial, oldest first
}

// Store holds zone versions. Every update installs a new immutable snapshot
// atomically, so a transfer in progress either keeps its pinned version or
// aborts; it never observes a half-applied change.
type Store struct {
	mu           sync.RWMutex
	zones        map[string]*zoneState
	journalDepth int
}

// NewStore creates an empty zone store keeping up to journalDepth deltas per
// zone for IXFR. Older deltas are discarded and affected clients fall back to
// a full transfer.
func NewStore(journalDepth int) *Store {
	if journalDepth < 1 {
		journalDepth = 1
	}
	return &Store{zones: make(map[string]*zoneState), journalDepth: journalDepth}
}

// Load installs the first version of a zone. records must start with the SOA.
func (s *Store) Load(zone string, serial uint32, records []Record) error {
	if len(records) == 0 || records[0].Type != "SOA" {
		return denyf("zone %q: first record must be SOA", zone)
	}
	snap := &snapshot{serial: serial, soa: records[0], records: cloneRecords(records[1:])}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones[zone] = &zoneState{cur: snap}
	return nil
}

// Update atomically replaces a zone with a new version and records the delta
// in the IXFR journal. The new serial must differ from the current one.
func (s *Store) Update(zone string, serial uint32, records []Record, delta Delta) error {
	if len(records) == 0 || records[0].Type != "SOA" {
		return denyf("zone %q: first record must be SOA", zone)
	}
	snap := &snapshot{serial: serial, soa: records[0], records: cloneRecords(records[1:])}
	s.mu.Lock()
	defer s.mu.Unlock()
	zs, ok := s.zones[zone]
	if !ok {
		return denyf("unknown zone %q", zone)
	}
	if zs.cur.serial == serial {
		return denyf("zone %q: serial %d already current", zone, serial)
	}
	delta.FromSerial = zs.cur.serial
	delta.ToSerial = serial
	zs.journal = append(zs.journal, delta)
	if len(zs.journal) > s.journalDepth {
		zs.journal = zs.journal[len(zs.journal)-s.journalDepth:]
	}
	zs.cur = snap
	return nil
}

// pin returns the current snapshot of a zone. The snapshot is immutable and
// safe to use without holding the lock.
func (s *Store) pin(zone string) (*snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, false
	}
	return zs.cur, true
}

// currentSerial reports the live serial of a zone, used to detect changes
// while a transfer streams.
func (s *Store) currentSerial(zone string) (uint32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return 0, false
	}
	return zs.cur.serial, true
}

// deltas returns the journal entries needed to bring a client at fromSerial
// up to the current serial, or false if the journal no longer reaches back
// far enough and the client must fall back to AXFR.
func (s *Store) deltas(zone string, fromSerial uint32) ([]Delta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, false
	}
	if fromSerial == zs.cur.serial {
		return nil, true
	}
	idx := sort.Search(len(zs.journal), func(i int) bool {
		return zs.journal[i].FromSerial == fromSerial
	})
	if idx == len(zs.journal) {
		return nil, false
	}
	out := make([]Delta, len(zs.journal)-idx)
	copy(out, zs.journal[idx:])
	return out, true
}

func cloneRecords(in []Record) []Record {
	out := make([]Record, len(in))
	copy(out, in)
	return out
}
