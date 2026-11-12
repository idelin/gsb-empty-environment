package lsadb

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Outcome identifies the admission decision Database.Admit reached.
type Outcome int

const (
	// OutcomeAuthRejected: the neighbor packet failed the cryptographic
	// authentication sequence-number check; no LSA was examined.
	OutcomeAuthRejected Outcome = iota

	// OutcomeInvalid: the LSA failed checksum, length or sequence-number
	// validation.
	OutcomeInvalid

	// OutcomeAccept: the LSA was newer than any database instance and has
	// been installed; it must be flooded and acknowledged.
	OutcomeAccept

	// OutcomeDuplicate: the same LSA instance is already in the database;
	// it is acknowledged but not flooded again.
	OutcomeDuplicate

	// OutcomeOlder: a newer instance is already in the database; the newer
	// instance is sent back to the neighbor instead of acknowledging.
	OutcomeOlder

	// OutcomeOrphanMaxAge: a MaxAge LSA with no matching database instance
	// is acknowledged and discarded without creating an entry.
	OutcomeOrphanMaxAge

	// OutcomeFlush: a MaxAge LSA matched a database instance, which has
	// been removed; the MaxAge LSA is flooded and acknowledged.
	OutcomeFlush

	// OutcomeSelfDefense: a self-originated LSA arrived with a sequence
	// number not older than the local instance. The forged LSA was refused
	// and a strictly newer local instance was re-originated and flooded.
	OutcomeSelfDefense

	// OutcomeSelfDefenseThrottled: as OutcomeSelfDefense, but the rate
	// limiter suppressed another re-origination; the database is unchanged
	// and RetryAfter reports when the next defense is allowed.
	OutcomeSelfDefenseThrottled

	// OutcomeSelfOlder: a self-originated LSA arrived whose sequence
	// number is older than the local instance; the local instance is
	// flooded back.
	OutcomeSelfOlder
)

// String returns the stable name of an outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeAuthRejected:
		return "auth-rejected"
	case OutcomeInvalid:
		return "invalid"
	case OutcomeAccept:
		return "accept"
	case OutcomeDuplicate:
		return "duplicate"
	case OutcomeOlder:
		return "older"
	case OutcomeOrphanMaxAge:
		return "orphan-maxage"
	case OutcomeFlush:
		return "flush"
	case OutcomeSelfDefense:
		return "self-defense"
	case OutcomeSelfDefenseThrottled:
		return "self-defense-throttled"
	case OutcomeSelfOlder:
		return "self-older"
	default:
		return fmt.Sprintf("outcome-%d", int(o))
	}
}

// Decision tells the routing process what to do with an admitted LSA. Slices
// must not be modified by the caller.
type Decision struct {
	// Outcome is the result of the admission check.
	Outcome Outcome

	// Key identifies the LSA that was examined.
	Key Key

	// Received is the LSA parsed from the neighbor packet.
	Received *LSA

	// Instance is the database instance after the decision: the freshly
	// installed LSA for Accept, the surviving instance for
	// Duplicate/Older, the re-originated LSA for SelfDefense, or nil for
	// removals and orphaned MaxAge LSAs.
	Instance *LSA

	// Replace reports whether the database entry for Key was installed or
	// replaced.
	Replace bool

	// Remove reports whether the database entry for Key was deleted.
	Remove bool

	// Ack lists LSAs that must be acknowledged to the sending neighbor.
	Ack []*LSA

	// Flood lists LSAs that must be flooded out of the router's other
	// interfaces.
	Flood []*LSA

	// Reoriginated is the freshly re-originated local LSA when Outcome is
	// OutcomeSelfDefense.
	Reoriginated *LSA

	// DefenseDelayed reports that a self-defense re-origination was
	// suppressed by the rate limiter.
	DefenseDelayed bool

	// RetryAfter, set when DefenseDelayed is true, is the time remaining
	// until another re-origination is permitted.
	RetryAfter time.Duration

	// Err carries the rejection cause for OutcomeAuthRejected and
	// OutcomeInvalid, or a re-origination failure.
	Err error
}

// Stats are cumulative admission counters.
type Stats struct {
	Received         atomic.Uint64
	AuthRejected     atomic.Uint64
	InvalidRejected  atomic.Uint64
	Accepted         atomic.Uint64
	Duplicates       atomic.Uint64
	Older            atomic.Uint64
	OrphanMaxAge     atomic.Uint64
	Flushed          atomic.Uint64
	SelfReoriginated atomic.Uint64
	SelfThrottled    atomic.Uint64
	SelfOlder        atomic.Uint64
}

// Snapshot returns the counters keyed by their metric names.
func (s *Stats) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"received":          s.Received.Load(),
		"auth_rejected":     s.AuthRejected.Load(),
		"invalid_rejected":  s.InvalidRejected.Load(),
		"accepted":          s.Accepted.Load(),
		"duplicates":        s.Duplicates.Load(),
		"older":             s.Older.Load(),
		"orphan_maxage":     s.OrphanMaxAge.Load(),
		"flushed":           s.Flushed.Load(),
		"self_reoriginated": s.SelfReoriginated.Load(),
		"self_throttled":    s.SelfThrottled.Load(),
		"self_older":        s.SelfOlder.Load(),
	}
}

// Clock allows tests to control time.
type Clock func() time.Time

// ReoriginateFunc produces a fresh local LSA in response to a forged
// self-originated LSA. prev is the current database instance (nil when none
// exists), incoming is the forged LSA refused from the wire and newSeq is the
// sequence number the fresh instance must carry.
type ReoriginateFunc func(prev, incoming *LSA, newSeq int32) (*LSA, error)

// DefenseConfig configures the self-originated-LSA counter-action.
type DefenseConfig struct {
	// ReoriginateInterval is the minimum time between two
	// re-originations of the same LSA key. Zero defaults to one second.
	ReoriginateInterval time.Duration

	// Reoriginate builds the replacement LSA. Nil selects
	// DefaultReoriginate.
	Reoriginate ReoriginateFunc

	// Clock is the time source. Nil selects time.Now.
	Clock Clock
}

// Database is the link-state database admission gate. It is safe for
// concurrent use.
type Database struct {
	local RouterID
	auth  *AuthTracker

	mu          sync.Mutex
	lsas        map[Key]*LSA
	lastDefense map[Key]time.Time

	cfg   DefenseConfig
	stats Stats
}

// NewDatabase creates an empty database for router local.
func NewDatabase(local RouterID, cfg DefenseConfig) *Database {
	if cfg.ReoriginateInterval <= 0 {
		cfg.ReoriginateInterval = time.Second
	}
	if cfg.Reoriginate == nil {
		cfg.Reoriginate = DefaultReoriginate
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Database{
		local:       local,
		lsas:        make(map[Key]*LSA),
		lastDefense: make(map[Key]time.Time),
		cfg:         cfg,
	}
}

// WithAuth attaches an authentication sequence tracker and returns the
// database so calls can be chained.
func (db *Database) WithAuth(tracker *AuthTracker) *Database {
	db.auth = tracker
	return db
}

// LocalRouter returns the router ID the database defends.
func (db *Database) LocalRouter() RouterID {
	return db.local
}

// Put installs or replaces an LSA directly. It is used for locally
// originated LSAs and for tests; it bypasses the admission checks.
func (db *Database) Put(l *LSA) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.lsas[l.Key()] = l
}

// Lookup returns the current instance of key and whether it exists.
func (db *Database) Lookup(key Key) (*LSA, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.lsas[key]
	return l, ok
}

// Len returns the number of LSAs currently in the database.
func (db *Database) Len() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.lsas)
}

// Snapshot returns a copy of the current database instances.
func (db *Database) Snapshot() []*LSA {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]*LSA, 0, len(db.lsas))
	for _, l := range db.lsas {
		out = append(out, l)
	}
	return out
}

// Stats returns the cumulative admission counters.
func (db *Database) Stats() *Stats {
	return &db.stats
}

// Admit runs the RFC 2328 section 13 ingress processing for one LSA received
// from neighbor. authSeq is the cryptographic authentication sequence number
// carried by the neighbor packet; it is only consulted when an AuthTracker
// has been attached.
func (db *Database) Admit(neighbor RouterID, authSeq uint64, received *LSA) Decision {
	// RFC 2328 appendix D.1 / RFC 5709: the authentication sequence number
	// is checked before anything else in the packet.
	if db.auth != nil && !db.auth.Check(neighbor, authSeq) {
		db.stats.AuthRejected.Add(1)
		return Decision{
			Outcome: OutcomeAuthRejected,
			Err: fmt.Errorf("%w: neighbor %s authentication sequence %d",
				ErrAuthSequence, neighbor, authSeq),
		}
	}

	if err := validateLSA(received); err != nil {
		db.stats.InvalidRejected.Add(1)
		return Decision{
			Outcome:  OutcomeInvalid,
			Received: received,
			Err:      err,
		}
	}
	db.stats.Received.Add(1)

	key := received.Key()
	db.mu.Lock()
	defer db.mu.Unlock()
	existing := db.lsas[key]

	// RFC 2328 section 13 step 3: MaxAge handling comes first.
	if received.IsMaxAge() {
		ack := []*LSA{received}
		if existing == nil {
			// No instance to flush: acknowledge and discard, never install.
			db.stats.OrphanMaxAge.Add(1)
			return Decision{
				Outcome:  OutcomeOrphanMaxAge,
				Key:      key,
				Received: received,
				Ack:      ack,
			}
		}
		delete(db.lsas, key)
		db.stats.Flushed.Add(1)
		return Decision{
			Outcome:  OutcomeFlush,
			Key:      key,
			Received: received,
			Instance: existing,
			Remove:   true,
			Ack:      ack,
			Flood:    ack,
		}
	}

	// RFC 2328 section 13.4: a self-originated LSA arriving from the
	// network is never accepted; counter-originate a newer instance.
	if received.AdvertisingRouter == db.local {
		if existing == nil || CompareSequence(received.SeqNum, existing.SeqNum) >= 0 {
			return db.defend(key, received, existing)
		}
		db.stats.SelfOlder.Add(1)
		return Decision{
			Outcome:  OutcomeSelfOlder,
			Key:      key,
			Received: received,
			Instance: existing,
			Flood:    []*LSA{existing},
		}
	}

	// Normal RFC 2328 section 13 step 5 flooding for foreign LSAs.
	ack := []*LSA{received}
	if existing == nil {
		db.lsas[key] = received
		db.stats.Accepted.Add(1)
		return Decision{
			Outcome:  OutcomeAccept,
			Key:      key,
			Received: received,
			Instance: received,
			Replace:  true,
			Ack:      ack,
			Flood:    ack,
		}
	}

	switch compareLSA(received, existing) {
	case 1:
		db.lsas[key] = received
		db.stats.Accepted.Add(1)
		return Decision{
			Outcome:  OutcomeAccept,
			Key:      key,
			Received: received,
			Instance: received,
			Replace:  true,
			Ack:      ack,
			Flood:    ack,
		}
	case 0:
		db.stats.Duplicates.Add(1)
		return Decision{
			Outcome:  OutcomeDuplicate,
			Key:      key,
			Received: received,
			Instance: existing,
			Ack:      ack,
		}
	default:
		db.stats.Older.Add(1)
		return Decision{
			Outcome:  OutcomeOlder,
			Key:      key,
			Received: received,
			Instance: existing,
			Flood:    []*LSA{existing},
		}
	}
}

// defend implements the rate-limited counter-origination of a self-originated
// LSA. The caller holds db.mu.
func (db *Database) defend(key Key, incoming, existing *LSA) Decision {
	now := db.cfg.Clock()
	if last, ok := db.lastDefense[key]; ok && now.Sub(last) < db.cfg.ReoriginateInterval {
		db.stats.SelfThrottled.Add(1)
		return Decision{
			Outcome:        OutcomeSelfDefenseThrottled,
			Key:            key,
			Received:       incoming,
			Instance:       existing,
			Ack:            []*LSA{incoming},
			DefenseDelayed: true,
			RetryAfter:     db.cfg.ReoriginateInterval - now.Sub(last),
		}
	}

	// Beat both the local instance and the forged instance so the
	// re-originated LSA is strictly newer in the circular sequence space.
	base := incoming.SeqNum
	if existing != nil && CompareSequence(existing.SeqNum, base) > 0 {
		base = existing.SeqNum
	}
	newSeq, mustFlush := NextSequence(base)

	fresh, err := db.cfg.Reoriginate(existing, incoming, newSeq)
	if err != nil {
		return Decision{
			Outcome:  OutcomeSelfDefense,
			Key:      key,
			Received: incoming,
			Instance: existing,
			Err:      fmt.Errorf("%w: %v", ErrReoriginate, err),
		}
	}

	db.lastDefense[key] = now
	db.stats.SelfReoriginated.Add(1)

	decision := Decision{
		Outcome:      OutcomeSelfDefense,
		Key:          key,
		Received:     incoming,
		Instance:     fresh,
		Reoriginated: fresh,
		Replace:      true,
		Ack:          []*LSA{incoming},
	}

	// RFC 2328 section 12.1.4: when the sequence number space wraps the old
	// instance must be MaxAge-flushed before the fresh initial instance is
	// flooded, so the flush reaches the area first.
	if mustFlush && existing != nil {
		flush := maxAgeCopy(existing)
		decision.Flood = []*LSA{flush, fresh}
	} else {
		decision.Flood = []*LSA{fresh}
	}

	db.lsas[key] = fresh
	return decision
}

// compareLSA applies the RFC 2328 section 13 step 5 instance comparison.
// It returns 1 when incoming is newer than current, -1 when it is older, and
// 0 when the two are the same LSA instance.
func compareLSA(incoming, current *LSA) int {
	switch CompareSequence(incoming.SeqNum, current.SeqNum) {
	case 1:
		return 1
	case -1:
		return -1
	}
	// Equal sequence number: the larger checksum is more recent.
	if incoming.Checksum != current.Checksum {
		if incoming.Checksum > current.Checksum {
			return 1
		}
		return -1
	}
	// Equal sequence and checksum: the smaller LS age is more recent.
	if incoming.LSAGE < current.LSAGE {
		return 1
	}
	if incoming.LSAGE > current.LSAGE {
		return -1
	}
	return 0
}

// validateLSA enforces the header invariants an admitted LSA must satisfy.
func validateLSA(l *LSA) error {
	if l == nil {
		return fmt.Errorf("%w: nil LSA", ErrMalformedLSA)
	}
	if l.Length < HeaderLength {
		return fmt.Errorf("%w: length %d below header size", ErrMalformedLSA, l.Length)
	}
	if int(l.Length) != HeaderLength+len(l.Body) {
		return fmt.Errorf("%w: length %d does not match %d header and body bytes",
			ErrMalformedLSA, l.Length, HeaderLength+len(l.Body))
	}
	if l.SeqNum == reservedSequenceNumber {
		return fmt.Errorf("%w: LSA from %s", ErrReservedSequence, l.AdvertisingRouter)
	}
	if l.raw != nil && !verifyChecksum(l.raw) {
		return fmt.Errorf("%w: LSA from %s type %d",
			ErrChecksum, l.AdvertisingRouter, l.LSType)
	}
	return nil
}
