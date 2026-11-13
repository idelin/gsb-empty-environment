package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrInvalidName is returned for malformed presentation-format names.
	ErrInvalidName = errors.New("netsec: invalid name")
	// ErrOutOfZone is returned when a name is not at or below the cache zone.
	ErrOutOfZone = errors.New("netsec: name is outside zone")
	// ErrUnverified is returned when the caller did not supply a verification
	// receipt for an NSEC record.
	ErrUnverified = errors.New("netsec: NSEC record has no verification receipt")
	// ErrReceiptMismatch is returned when a receipt is not bound to exactly the
	// record fields that were validated.
	ErrReceiptMismatch = errors.New("netsec: verification receipt does not match record")
	// ErrExpired is returned when a record is already expired at insertion.
	ErrExpired = errors.New("netsec: NSEC record is expired")
	// ErrStaleSerial is returned when a record belongs to an older zone serial.
	ErrStaleSerial = errors.New("netsec: NSEC record belongs to an older zone serial")
)

// Clock supplies the current time.  Tests and callers can replace the default
// clock through WithClock.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

type config struct {
	clock Clock
}

// Option configures a Cache.
type Option func(*config) error

// WithClock sets the cache clock.
func WithClock(clock Clock) Option {
	return func(c *config) error {
		if clock == nil {
			return errors.New("netsec: nil clock")
		}
		c.clock = clock
		return nil
	}
}

// WithClockFunc sets the cache clock from a function.
func WithClockFunc(now func() time.Time) Option {
	return func(c *config) error {
		if now == nil {
			return errors.New("netsec: nil clock function")
		}
		c.clock = ClockFunc(now)
		return nil
	}
}

// Receipt is a caller-produced verification receipt.  It must bind the zone,
// owner, next name, type bitmap, signature expiry, and zone serial that were
// covered by the validation that produced the receipt.
type Receipt struct {
	ID               string
	Zone             string
	Owner            string
	Next             string
	Types            TypeBitmap
	SignatureExpires time.Time
	ZoneSerial       uint32
}

// NSECRecord is a verified denial-of-existence record accepted by the cache.
// TTL is the original TTL in seconds, not a countdown value.
type NSECRecord struct {
	Owner            string
	Next             string
	Types            TypeBitmap
	TTL              uint32
	SignatureExpires time.Time
	ReceiptID        string
	ZoneSerial       uint32
}

type cachedNSEC struct {
	record  NSECRecord
	owner   Name
	next    Name
	expires time.Time
}

// Status describes the strongest denial answer supported by the cache.
type Status uint8

const (
	// NeedUpstream means the cache cannot safely answer the query.
	NeedUpstream Status = iota
	// NXDomain means the query name and its applicable wildcard were proven
	// absent with current, same-serial NSEC records.
	NXDomain
	// NoData means the query name exists and the requested type is not in the
	// NSEC bitmap at that owner (and no CNAME blocks the answer).
	NoData
)

// Clear aliases for callers that prefer status-prefixed names.
const (
	StatusNeedUpstream = NeedUpstream
	StatusNXDomain     = NXDomain
	StatusNoData       = NoData
)

// Answer is the result of a cache lookup.  Proof contains only the NSEC
// records necessary for the answer, in canonical owner order and without
// duplicates.  ClosestEncloser and Wildcard are filled for an NXDOMAIN answer.
type Answer struct {
	Status          Status
	Proof           []NSECRecord
	ClosestEncloser string
	Wildcard        string
}

// Cache stores verified NSEC records for one fixed zone and one current zone
// serial.  It is safe for concurrent use.
type Cache struct {
	zone Name

	mu        sync.RWMutex
	clock     Clock
	hasSerial bool
	serial    uint32
	records   map[Name]cachedNSEC
}

// NewCache creates an NSEC cache for zone.
func NewCache(zone string, opts ...Option) (*Cache, error) {
	z, err := ParseName(zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	cfg := config{clock: ClockFunc(time.Now)}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	return &Cache{
		zone:    z,
		clock:   cfg.clock,
		records: make(map[Name]cachedNSEC),
	}, nil
}

// New is an alias for NewCache.
func New(zone string, opts ...Option) (*Cache, error) { return NewCache(zone, opts...) }

// Zone returns the canonical cache zone.
func (c *Cache) Zone() string { return c.zone.String() }

// ZoneName returns the canonical cache zone as a Name.
func (c *Cache) ZoneName() Name { return c.zone }

// Serial returns the current zone serial and whether any record has been
// accepted.
func (c *Cache) Serial() (uint32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serial, c.hasSerial
}

// Len returns the number of currently valid records.
func (c *Cache) Len() int {
	now := c.clock.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, e := range c.records {
		if now.Before(e.expires) {
			n++
		}
	}
	return n
}

// Add validates and inserts a caller-verified NSEC record.  A record from a
// newer zone serial atomically discards all older records.  A record from an
// older serial is rejected.  Re-adding an owner in the current serial replaces
// that owner's previous NSEC.
func (c *Cache) Add(record NSECRecord, receipt Receipt) error {
	now := c.clock.Now()
	entry, err := c.validate(record, receipt, now)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasSerial {
		switch serialCompare(record.ZoneSerial, c.serial) {
		case -1:
			return ErrStaleSerial
		case 1:
			c.records = make(map[Name]cachedNSEC)
			c.serial = record.ZoneSerial
		}
	} else {
		c.hasSerial = true
		c.serial = record.ZoneSerial
	}

	c.records[entry.owner] = entry
	return nil
}

// Insert is an alias for Add.
func (c *Cache) Insert(record NSECRecord, receipt Receipt) error {
	return c.Add(record, receipt)
}

// Update is an alias for Add.
func (c *Cache) Update(record NSECRecord, receipt Receipt) error {
	return c.Add(record, receipt)
}

// PurgeExpired removes records that are no longer valid.
func (c *Cache) PurgeExpired() {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for owner, e := range c.records {
		if !now.Before(e.expires) {
			delete(c.records, owner)
		}
	}
}

func (c *Cache) validate(record NSECRecord, receipt Receipt, now time.Time) (cachedNSEC, error) {
	if record.ReceiptID == "" || receipt.ID == "" {
		return cachedNSEC{}, ErrUnverified
	}
	if record.ReceiptID != receipt.ID {
		return cachedNSEC{}, fmt.Errorf("%w: receipt ID", ErrReceiptMismatch)
	}

	receiptZone, err := ParseName(receipt.Zone)
	if err != nil {
		return cachedNSEC{}, fmt.Errorf("%w: receipt zone: %v", ErrReceiptMismatch, err)
	}
	if receiptZone.Compare(c.zone) != 0 {
		return cachedNSEC{}, fmt.Errorf("%w: receipt zone", ErrReceiptMismatch)
	}

	owner, err := ParseName(record.Owner)
	if err != nil {
		return cachedNSEC{}, fmt.Errorf("%w: owner: %v", ErrInvalidName, err)
	}
	next, err := ParseName(record.Next)
	if err != nil {
		return cachedNSEC{}, fmt.Errorf("%w: next: %v", ErrInvalidName, err)
	}
	if !owner.IsSubdomainOf(c.zone) {
		return cachedNSEC{}, fmt.Errorf("%w: owner %s", ErrOutOfZone, owner)
	}
	if !next.IsSubdomainOf(c.zone) {
		return cachedNSEC{}, fmt.Errorf("%w: next %s", ErrOutOfZone, next)
	}

	receiptOwner, err := ParseName(receipt.Owner)
	if err != nil || receiptOwner.Compare(owner) != 0 {
		return cachedNSEC{}, fmt.Errorf("%w: receipt owner", ErrReceiptMismatch)
	}
	receiptNext, err := ParseName(receipt.Next)
	if err != nil || receiptNext.Compare(next) != 0 {
		return cachedNSEC{}, fmt.Errorf("%w: receipt next", ErrReceiptMismatch)
	}

	types, err := record.Types.Normalize()
	if err != nil {
		return cachedNSEC{}, err
	}
	receiptTypes, err := receipt.Types.Normalize()
	if err != nil {
		return cachedNSEC{}, fmt.Errorf("%w: receipt types: %v", ErrReceiptMismatch, err)
	}
	if !types.Equal(receiptTypes) {
		return cachedNSEC{}, fmt.Errorf("%w: receipt types", ErrReceiptMismatch)
	}
	if !record.SignatureExpires.Equal(receipt.SignatureExpires) {
		return cachedNSEC{}, fmt.Errorf("%w: signature expiry", ErrReceiptMismatch)
	}
	if record.ZoneSerial != receipt.ZoneSerial {
		return cachedNSEC{}, fmt.Errorf("%w: zone serial", ErrReceiptMismatch)
	}

	ttlExpiry := now.Add(time.Duration(record.TTL) * time.Second)
	expires := ttlExpiry
	if record.SignatureExpires.Before(expires) {
		expires = record.SignatureExpires
	}
	if !now.Before(expires) {
		return cachedNSEC{}, ErrExpired
	}

	normalized := NSECRecord{
		Owner:            owner.String(),
		Next:             next.String(),
		Types:            types.Clone(),
		TTL:              record.TTL,
		SignatureExpires: record.SignatureExpires,
		ReceiptID:        record.ReceiptID,
		ZoneSerial:       record.ZoneSerial,
	}
	return cachedNSEC{
		record:  normalized,
		owner:   owner,
		next:    next,
		expires: expires,
	}, nil
}

// Query looks up qname and qtype.  It never reports NXDOMAIN unless it has a
// strict covering NSEC for qname and a strict covering NSEC for the applicable
// wildcard, with an existing closest encloser determined from current cache
// evidence.  Otherwise it returns NeedUpstream.
func (c *Cache) Query(qname string, qtype uint16) (Answer, error) {
	q, err := ParseName(qname)
	if err != nil {
		return Answer{}, fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	if !q.IsSubdomainOf(c.zone) {
		return Answer{}, fmt.Errorf("%w: %s", ErrOutOfZone, q)
	}

	now := c.clock.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()

	valid := make([]cachedNSEC, 0, len(c.records))
	all := make([]cachedNSEC, 0, len(c.records))
	for _, e := range c.records {
		all = append(all, e)
		if now.Before(e.expires) {
			valid = append(valid, e)
		}
	}

	if exact, ok := c.records[q]; ok {
		if !now.Before(exact.expires) {
			return Answer{Status: NeedUpstream}, nil
		}
		if qtype == TypeANY {
			return Answer{Status: NeedUpstream}, nil
		}
		if !exact.record.Types.Has(qtype) {
			if qtype != TypeCNAME && exact.record.Types.Has(TypeCNAME) {
				return Answer{Status: NeedUpstream}, nil
			}
			return Answer{
				Status: NoData,
				Proof:  []NSECRecord{cloneRecord(exact.record)},
			}, nil
		}
		return Answer{Status: NeedUpstream}, nil
	}

	// A name can be present as an empty non-terminal even though it has no NSEC
	// owner.  An NSEC that covers the name and whose next name is a strict
	// descendant proves that this name is an empty non-terminal and therefore
	// has no RRsets of any type.
	if existsIn(valid, c.zone, q) {
		if proof, ok := emptyNonTerminalProof(valid, c.zone, q); ok {
			return Answer{
				Status: NoData,
				Proof:  []NSECRecord{cloneRecord(proof.record)},
			}, nil
		}
		return Answer{Status: NeedUpstream}, nil
	}
	// Expired evidence is not usable for a proof, but treating a possibly
	// present name as absent would be unsafe.  Defer rather than guess.
	if existsIn(all, c.zone, q) {
		return Answer{Status: NeedUpstream}, nil
	}

	qProof, ok := coveringRecord(valid, q)
	if !ok {
		return Answer{Status: NeedUpstream}, nil
	}
	// An expired record is not proof, but it can still indicate that the cache
	// lacks current evidence for one of q's ancestors.  In that case a guessed
	// lower closest encloser could select the wrong wildcard, so defer.
	if hasExpiredAncestor(all, now, c.zone, q) {
		return Answer{Status: NeedUpstream}, nil
	}

	ce := c.closestEncloser(valid, q)
	wild := nameAt(ce, "*")
	if existsIn(valid, c.zone, wild) || existsIn(all, c.zone, wild) {
		return Answer{Status: NeedUpstream}, nil
	}
	if covers(qProof.owner, qProof.next, wild) {
		return Answer{
			Status:          NXDomain,
			Proof:           minimalProof([]NSECRecord{qProof.record}),
			ClosestEncloser: ce.String(),
			Wildcard:        wild.String(),
		}, nil
	}
	wProof, ok := coveringRecord(valid, wild)
	if !ok {
		return Answer{Status: NeedUpstream}, nil
	}

	proof := []NSECRecord{qProof.record, wProof.record}
	return Answer{
		Status:          NXDomain,
		Proof:           minimalProof(proof),
		ClosestEncloser: ce.String(),
		Wildcard:        wild.String(),
	}, nil
}

// Lookup is an alias for Query.
func (c *Cache) Lookup(qname string, qtype uint16) (Answer, error) {
	return c.Query(qname, qtype)
}

func (c *Cache) closestEncloser(valid []cachedNSEC, q Name) Name {
	cur, ok := q.Parent()
	if !ok {
		return c.zone
	}
	for {
		if cur == c.zone || existsIn(valid, c.zone, cur) {
			return cur
		}
		parent, ok := cur.Parent()
		if !ok || parent == cur {
			return c.zone
		}
		cur = parent
	}
}

func existsIn(records []cachedNSEC, zone, name Name) bool {
	if name == zone {
		return true
	}
	for _, e := range records {
		if name == e.owner || name == e.next {
			return true
		}
		if name.IsAncestorOf(e.owner) || name.IsAncestorOf(e.next) {
			return true
		}
	}
	return false
}

func hasExpiredAncestor(records []cachedNSEC, now time.Time, zone, q Name) bool {
	for _, e := range records {
		if now.Before(e.expires) {
			continue
		}
		if e.owner != zone && q.IsSubdomainOf(e.owner) {
			return true
		}
		if e.next != zone && q.IsSubdomainOf(e.next) {
			return true
		}
	}
	return false
}

func emptyNonTerminalProof(records []cachedNSEC, zone, q Name) (cachedNSEC, bool) {
	if q == zone {
		return cachedNSEC{}, false
	}
	var candidates []cachedNSEC
	for _, e := range records {
		if covers(e.owner, e.next, q) && q.IsAncestorOf(e.next) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return cachedNSEC{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if c := candidates[i].owner.Compare(candidates[j].owner); c != 0 {
			return c < 0
		}
		return candidates[i].record.ReceiptID < candidates[j].record.ReceiptID
	})
	return candidates[0], true
}

func coveringRecord(records []cachedNSEC, q Name) (cachedNSEC, bool) {
	var candidates []cachedNSEC
	for _, e := range records {
		if covers(e.owner, e.next, q) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return cachedNSEC{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if c := candidates[i].owner.Compare(candidates[j].owner); c != 0 {
			return c < 0
		}
		if c := candidates[i].next.Compare(candidates[j].next); c != 0 {
			return c < 0
		}
		return candidates[i].record.ReceiptID < candidates[j].record.ReceiptID
	})
	return candidates[0], true
}

func covers(owner, next, q Name) bool {
	if q == owner {
		return false
	}
	if owner == next {
		// A one-name zone: the NSEC covers the entire zone ring except owner.
		return true
	}
	cmp := owner.Compare(next)
	if cmp < 0 {
		return owner.Compare(q) < 0 && q.Compare(next) < 0
	}
	return owner.Compare(q) < 0 || q.Compare(next) < 0
}

func minimalProof(records []NSECRecord) []NSECRecord {
	if len(records) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(records))
	out := make([]NSECRecord, 0, len(records))
	for _, r := range records {
		key := r.Owner + "\x00" + r.Next + "\x00" + r.ReceiptID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cloneRecord(r))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := ParseName(out[i].Owner)
		b, _ := ParseName(out[j].Owner)
		if c := a.Compare(b); c != 0 {
			return c < 0
		}
		return out[i].ReceiptID < out[j].ReceiptID
	})
	return out
}

func cloneRecord(r NSECRecord) NSECRecord {
	r.Types = r.Types.Clone()
	return r
}

func serialCompare(a, b uint32) int {
	d := int32(a - b)
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	default:
		return 0
	}
}
