package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// TopicPartition identifies a single Kafka partition.
type TopicPartition struct {
	Topic     string
	Partition int32
}

func (tp TopicPartition) String() string {
	return fmt.Sprintf("%s-%d", tp.Topic, tp.Partition)
}

// Session-level errors: the whole incremental fetch request is rejected.
var (
	// ErrSessionNotFound is returned when the session ID is unknown or closed.
	ErrSessionNotFound = errors.New("fetch session id not found")
	// ErrInvalidEpoch is returned when the request epoch is not exactly the
	// next expected epoch (epoch jumps, replays, or stale requests).
	ErrInvalidEpoch = errors.New("invalid fetch session epoch")
	// ErrPrincipalMismatch is returned when the authenticated principal does
	// not match the principal that created the session (stolen session ID).
	ErrPrincipalMismatch = errors.New("fetch session principal mismatch")
)

// ErrNotAuthorized is returned per-partition when the principal is no longer
// authorized to fetch from that partition.
var ErrNotAuthorized = errors.New("not authorized to fetch partition")

// Authorizer re-checks whether a principal is still allowed to fetch a
// partition. It is consulted on every incremental request so that revoked
// partitions are dropped promptly.
type Authorizer interface {
	Authorized(principal string, tp TopicPartition) bool
}

// AuthorizerFunc adapts a function to the Authorizer interface.
type AuthorizerFunc func(principal string, tp TopicPartition) bool

func (f AuthorizerFunc) Authorized(principal string, tp TopicPartition) bool {
	return f(principal, tp)
}

// Session is an incremental fetch session. It is bound at creation time to
// the authenticated principal and the set of partitions that were authorized
// in the full fetch that opened it.
type Session struct {
	id        int64
	principal string
	epoch     int32
	allowed   map[TopicPartition]struct{}
}

// ID returns the session identifier clients send in incremental requests.
func (s *Session) ID() int64 { return s.id }

// Principal returns the authenticated principal the session is bound to.
func (s *Session) Principal() string { return s.principal }

// IncrementalRequest is an incremental fetch request handled by the proxy.
type IncrementalRequest struct {
	SessionID int64
	Epoch     int32
	Principal string
	// Partitions the client wants to fetch in this request. Each must belong
	// to the session's creation-time authorized set.
	Partitions []TopicPartition
}

// PartitionError is a per-partition failure in an otherwise valid request.
type PartitionError struct {
	Partition TopicPartition
	Err       error
}

// Table is the fetch session table consulted by the Fetch handler.
type Table struct {
	mu       sync.Mutex
	nextID   int64
	sessions map[int64]*Session
	auth     Authorizer
}

// NewTable creates a session table that re-validates partitions with auth.
func NewTable(auth Authorizer) *Table {
	return &Table{
		nextID:   1,
		sessions: make(map[int64]*Session),
		auth:     auth,
	}
}

// Open creates a session for principal after a full fetch, binding it to the
// partitions authorized at creation time. The first incremental request must
// use epoch 1.
func (t *Table) Open(principal string, partitions []TopicPartition) *Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := &Session{
		id:        t.nextID,
		principal: principal,
		allowed:   make(map[TopicPartition]struct{}, len(partitions)),
	}
	t.nextID++
	for _, tp := range partitions {
		s.allowed[tp] = struct{}{}
	}
	t.sessions[s.id] = s
	return s
}

// Close invalidates a session; further incremental requests fail with
// ErrSessionNotFound.
func (t *Table) Close(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, id)
}

// Fetch validates an incremental request against the session table.
//
// A session-level error (unknown ID, epoch mismatch, or different principal)
// rejects the whole request and leaves the session untouched. Otherwise the
// epoch advances and each requested partition is checked: partitions outside
// the creation-time authorized set, or revoked since, are dropped from the
// session and reported as per-partition authorization failures.
func (t *Table) Fetch(req IncrementalRequest) (fetched []TopicPartition, denied []PartitionError, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.sessions[req.SessionID]
	if !ok {
		return nil, nil, ErrSessionNotFound
	}
	if s.principal != req.Principal {
		return nil, nil, fmt.Errorf("%w: session owned by %q, got %q",
			ErrPrincipalMismatch, s.principal, req.Principal)
	}
	if req.Epoch != s.epoch+1 {
		return nil, nil, fmt.Errorf("%w: expected %d, got %d",
			ErrInvalidEpoch, s.epoch+1, req.Epoch)
	}
	s.epoch = req.Epoch

	for _, tp := range req.Partitions {
		if _, ok := s.allowed[tp]; !ok {
			denied = append(denied, PartitionError{Partition: tp, Err: ErrNotAuthorized})
			continue
		}
		if t.auth != nil && !t.auth.Authorized(s.principal, tp) {
			// Authorization revoked: evict from the session so later
			// incremental requests cannot fetch it either.
			delete(s.allowed, tp)
			denied = append(denied, PartitionError{Partition: tp, Err: ErrNotAuthorized})
			continue
		}
		fetched = append(fetched, tp)
	}
	return fetched, denied, nil
}
