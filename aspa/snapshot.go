package aspa

import (
	"sync/atomic"
	"time"
)

// Snapshot is an immutable, consistently published view of the
// customer-to-provider authorizations in one RPKI snapshot. Objects
// that were revoked (absent from the new publication) or expired
// before the snapshot was built never appear in it, so verifications
// pinned to a snapshot never mix stale objects with new results.
type Snapshot struct {
	Serial    uint64
	CreatedAt time.Time

	providers map[ASN]map[ASN]struct{}
	records   map[ASN]*ASPA
}

// NewSnapshot builds an immutable snapshot from the given ASPA
// records. Records whose NotAfter is non-zero and not after now are
// treated as expired and excluded. Later records for the same
// customer replace earlier ones.
func NewSnapshot(serial uint64, records []ASPA, now time.Time) *Snapshot {
	s := &Snapshot{
		Serial:    serial,
		CreatedAt: now,
		providers: make(map[ASN]map[ASN]struct{}, len(records)),
		records:   make(map[ASN]*ASPA, len(records)),
	}
	for i := range records {
		rec := records[i]
		if !rec.NotAfter.IsZero() && !now.Before(rec.NotAfter) {
			continue
		}
		set := make(map[ASN]struct{}, len(rec.Providers))
		for _, p := range rec.Providers {
			set[p] = struct{}{}
		}
		s.providers[rec.Customer] = set
		cp := rec
		s.records[rec.Customer] = &cp
	}
	return s
}

// HasRecord reports whether the snapshot holds an ASPA record for
// customer. Absence means the customer has published no (unexpired)
// ASPA and verification must yield Unknown for pairs needing it.
func (s *Snapshot) HasRecord(customer ASN) bool {
	_, ok := s.records[customer]
	return ok
}

// Record returns the ASPA record for customer, or nil.
func (s *Snapshot) Record(customer ASN) *ASPA {
	return s.records[customer]
}

// Authorized reports whether customer's ASPA record lists provider.
func (s *Snapshot) Authorized(customer, provider ASN) bool {
	set, ok := s.providers[customer]
	if !ok {
		return false
	}
	_, ok = set[provider]
	return ok
}

// Store holds the currently published snapshot. Publishing is an
// atomic pointer swap: readers either see the whole old snapshot or
// the whole new one, never a mixture.
type Store struct {
	cur atomic.Pointer[Snapshot]
}

// NewStore returns a Store publishing snap (which may be nil).
func NewStore(snap *Snapshot) *Store {
	s := &Store{}
	s.cur.Store(snap)
	return s
}

// Publish atomically replaces the current snapshot.
func (s *Store) Publish(snap *Snapshot) {
	s.cur.Store(snap)
}

// Current returns the currently published snapshot, or nil if none
// has been published yet.
func (s *Store) Current() *Snapshot {
	return s.cur.Load()
}
