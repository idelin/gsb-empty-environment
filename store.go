package netsec

import (
	"context"
	"fmt"
	"sync"
)

// RelationTuple is an ACL fact. A write with Present=false revokes the
// relation for the subject on the object.
type RelationTuple struct {
	Object   string
	Relation string
	Subject  string
	Present  bool
}

type tupleKey struct {
	object   string
	relation string
	subject  string
}

// entryKind distinguishes ACL writes from resource-content writes in the
// leader's append-only log.
type entryKind int

const (
	entryACL entryKind = iota
	entryContent
)

// LogEntry is one committed write, stamped with the zookie timestamp at which
// it took effect. Replicas apply entries in timestamp order.
type LogEntry struct {
	Timestamp uint64
	Kind      entryKind
	Object    string
	Tuple     RelationTuple
}

// CheckRequest is a permission check. Token is an optional at_least_as_fresh
// wire zookie: when non-empty, the serving node must have applied a snapshot
// at least that fresh before answering.
type CheckRequest struct {
	Object   string
	Relation string
	Subject  string
	Token    string
}

// CheckResult is the answer to a CheckRequest.
type CheckResult struct {
	Allowed  bool
	ServedAt Zookie
}

// Store is the authoritative, writable leader. It hands out monotonically
// increasing zookies, keeps an append-only log for replicas, and tracks the
// freshness floor of every resource.
type Store struct {
	secret []byte

	mu        sync.Mutex
	clock     uint64
	log       []LogEntry
	tuples    map[tupleKey]bool
	aclFloor  map[string]uint64
	contentTS map[string]uint64
}

// NewStore creates a leader store authenticated with secret. Replicas must be
// configured with the same secret so that tokens validate identically.
func NewStore(secret []byte) *Store {
	return &Store{
		secret:    append([]byte(nil), secret...),
		tuples:    make(map[tupleKey]bool),
		aclFloor:  make(map[string]uint64),
		contentTS: make(map[string]uint64),
	}
}

// nextZookieLocked advances the logical clock and returns the fresh token.
// Callers must hold s.mu. Timestamps are strictly monotonic across every
// write, regardless of write type.
func (s *Store) nextZookieLocked() Zookie {
	s.clock++
	return mintZookie(s.secret, s.clock)
}

// WriteTuple applies an ACL write (grant or revocation) and returns the
// zookie at which it is visible. The resource's ACL freshness floor is
// advanced to that zookie so no later check can be served from an older ACL.
func (s *Store) WriteTuple(t RelationTuple) (Zookie, error) {
	if t.Object == "" || t.Relation == "" || t.Subject == "" {
		return Zookie{}, fmt.Errorf("netsec: object, relation and subject are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	zk := s.nextZookieLocked()
	key := tupleKey{object: t.Object, relation: t.Relation, subject: t.Subject}
	if t.Present {
		s.tuples[key] = true
	} else {
		delete(s.tuples, key)
	}
	s.aclFloor[t.Object] = zk.Timestamp()
	s.log = append(s.log, LogEntry{
		Timestamp: zk.Timestamp(),
		Kind:      entryACL,
		Object:    t.Object,
		Tuple:     t,
	})
	return zk, nil
}

// SaveContent records a resource-content mutation. The returned zookie is
// stored as the resource's content freshness floor: every subsequent check of
// the resource must read a snapshot at least this fresh, preventing a stale
// replica from evaluating new content against an old ACL view.
func (s *Store) SaveContent(object string) (Zookie, error) {
	if object == "" {
		return Zookie{}, fmt.Errorf("netsec: object is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	zk := s.nextZookieLocked()
	s.contentTS[object] = zk.Timestamp()
	s.log = append(s.log, LogEntry{
		Timestamp: zk.Timestamp(),
		Kind:      entryContent,
		Object:    object,
	})
	return zk, nil
}

// Latest returns the leader's newest zookie.
func (s *Store) Latest() Zookie {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mintZookie(s.secret, s.clock)
}

// EntriesSince returns the committed log entries with timestamps greater than
// after. This is the replication feed consumed by replicas.
func (s *Store) EntriesSince(after uint64) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]LogEntry, 0, len(s.log))
	for _, entry := range s.log {
		if entry.Timestamp > after {
			out = append(out, entry)
		}
	}
	return out
}

// ResourceFloor returns the resource's authoritative freshness floor: the
// maximum of its latest ACL write and its latest content write. Replicas
// consult the leader for this value because a lagging replica may not yet
// know the newest floor.
func (s *Store) ResourceFloor(object string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	floor := s.aclFloor[object]
	if content := s.contentTS[object]; content > floor {
		floor = content
	}
	return floor
}

// HasTuple reports whether the relation is currently granted at the latest
// leader state.
func (s *Store) HasTuple(object, relation, subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tuples[tupleKey{object: object, relation: relation, subject: subject}]
}

// Check serves an authoritative leader check. An at_least_as_fresh token is
// validated and rejected if malformed or ahead of the leader's latest
// timestamp; otherwise the current state already satisfies any such bound.
func (s *Store) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	required, err := s.requiredFreshness(req)
	if err != nil {
		return CheckResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if required > s.clock {
		return CheckResult{}, fmt.Errorf("%w: %d > leader %d",
			ErrFutureZookie, required, s.clock)
	}

	allowed := s.tuples[tupleKey{object: req.Object, relation: req.Relation, subject: req.Subject}]
	return CheckResult{
		Allowed:  allowed,
		ServedAt: mintZookie(s.secret, s.clock),
	}, nil
}

// requiredFreshness parses and bounds an at_least_as_fresh token. It returns
// the explicit requested timestamp, or zero when the request carries no
// token. Malformed and future (relative to the leader) tokens are rejected.
func (s *Store) requiredFreshness(req CheckRequest) (uint64, error) {
	if req.Token == "" {
		return 0, nil
	}

	zk, err := ParseZookie(s.secret, req.Token)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	latest := s.clock
	s.mu.Unlock()

	if zk.Timestamp() > latest {
		return 0, fmt.Errorf("%w: %d > leader %d",
			ErrFutureZookie, zk.Timestamp(), latest)
	}
	return zk.Timestamp(), nil
}
