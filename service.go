package delegation

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Service is the in-memory ticket authority for a single process. It owns
// the HMAC key used to sign tokens and the bookkeeping for delegation
// trees, revocations and one-time claim state.
//
// All mutating operations and all stateful checks hold the same mutex, so
// concurrent calls (for example a revocation racing with a claim) are
// linearized in a single, consistent order.
type Service struct {
	key   []byte
	now   func() time.Time
	mu    sync.Mutex
	nodes map[string]*node
}

// node is the internal bookkeeping record for one ticket.
type node struct {
	ticket   Ticket
	revoked  bool
	consumed bool
	jobID    string
	children []string
}

// Option customizes a Service at construction time.
type Option func(*Service)

// WithClock overrides the time source used for expiry checks. It is mainly
// useful in tests that need to move time deterministically.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.now = clock
		}
	}
}

// NewService creates a ticket authority. The key is the HMAC secret shared
// by nothing outside the process: tokens handed to workers never contain
// it. Keys shorter than 32 bytes are rejected.
func NewService(key []byte, opts ...Option) (*Service, error) {
	if len(key) < 32 {
		return nil, ErrWeakKey
	}
	service := &Service{
		key:   append([]byte(nil), key...),
		now:   time.Now,
		nodes: make(map[string]*node),
	}
	for _, opt := range opts {
		opt(service)
	}
	return service, nil
}

// Issue creates a root ticket for the given scope and returns its signed
// token. The scope is canonicalized (sorted and deduplicated).
func (s *Service) Issue(scope Scope) (string, error) {
	if err := scope.validate(); err != nil {
		return "", err
	}
	ticket := Ticket{ID: newID(), Scope: scope}

	s.mu.Lock()
	s.nodes[ticket.ID] = &node{ticket: ticket}
	s.mu.Unlock()

	return signToken(s.key, ticket)
}

// Delegate derives a new ticket from parentToken. The requested scope must
// narrow the parent scope: same tenant, no later deadline, and object and
// action sets that are subsets of the parent sets.
//
// A ticket that is expired, revoked (including by an ancestor), or already
// consumed cannot be delegated further.
func (s *Service) Delegate(parentToken string, scope Scope) (string, error) {
	parent, err := parseToken(s.key, parentToken)
	if err != nil {
		return "", err
	}
	if err := scope.validate(); err != nil {
		return "", err
	}
	if !scope.narrows(parent.Scope) {
		return "", ErrScopeWidened
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.liveLocked(parent.ID, parent.ExpiresAt); err != nil {
		return "", err
	}
	child := Ticket{
		ID:       newID(),
		ParentID: parent.ID,
		Scope:    scope,
	}
	s.nodes[parent.ID].children = append(s.nodes[parent.ID].children, child.ID)
	s.nodes[child.ID] = &node{ticket: child}
	return signToken(s.key, child)
}

// Revoke marks the ticket and its whole delegation subtree as revoked.
// Revocation is idempotent: revoking an unknown or already revoked ticket
// is an error only when the ticket ID never existed.
func (s *Service) Revoke(token string) error {
	ticket, err := parseToken(s.key, token)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := s.nodes[ticket.ID]
	if !ok {
		return ErrTicketNotFound
	}
	revokeSubtree(target, s.nodes)
	return nil
}

// Validate checks that the token is well-formed and currently usable
// without consuming it. A successful Validate leaves the ticket claimable;
// failed validation never changes service state and therefore never
// consumes the ticket.
func (s *Service) Validate(token string) error {
	ticket, err := parseToken(s.key, token)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.liveLocked(ticket.ID, ticket.ExpiresAt)
}

// Claim atomically validates the token and grants the one-time right to
// execute one job for the requested tenant/object/action. At most one
// concurrent Claim on the same ticket can succeed: every subsequent Claim
// returns ErrTicketConsumed.
//
// Every failing precondition (bad signature, expiry, revocation, scope
// mismatch) is evaluated before the ticket is marked consumed, so a failed
// claim never consumes the ticket and the same token can be retried once
// the caller fixes the request.
func (s *Service) Claim(token, tenantID, object, action, jobID string) (Ticket, error) {
	ticket, err := parseToken(s.key, token)
	if err != nil {
		return Ticket{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.nodes[ticket.ID]
	if !ok {
		return Ticket{}, ErrTicketNotFound
	}
	now := s.now().Unix()
	if ticket.ExpiresAt <= now {
		return Ticket{}, ErrTicketExpired
	}
	if record.revoked || s.ancestorRevokedLocked(record) {
		return Ticket{}, ErrTicketRevoked
	}
	if record.consumed {
		return Ticket{}, ErrTicketConsumed
	}
	if !ticket.permits(tenantID, object, action, now) {
		return Ticket{}, ErrScopeDenied
	}

	record.consumed = true
	record.jobID = jobID
	return ticket, nil
}

// liveLocked verifies that the ticket is known, unexpired and not revoked
// (including revocation inherited from an ancestor). The caller must hold
// s.mu.
func (s *Service) liveLocked(id string, expiresAt int64) error {
	record, ok := s.nodes[id]
	if !ok {
		return ErrTicketNotFound
	}
	if expiresAt <= s.now().Unix() {
		return ErrTicketExpired
	}
	if record.revoked {
		return ErrTicketRevoked
	}
	if s.ancestorRevokedLocked(record) {
		return ErrTicketRevoked
	}
	if record.consumed {
		return ErrTicketConsumed
	}
	return nil
}

// ancestorRevokedLocked walks the parent chain. Revocation marks a whole
// subtree eagerly, but the parent walk also covers tickets whose records
// were linked before that revocation happened. The caller must hold s.mu.
func (s *Service) ancestorRevokedLocked(record *node) bool {
	parentID := record.ticket.ParentID
	for parentID != "" {
		parent, ok := s.nodes[parentID]
		if !ok {
			return true
		}
		if parent.revoked {
			return true
		}
		parentID = parent.ticket.ParentID
	}
	return false
}

// revokeSubtree marks record and every reachable descendant as revoked.
func revokeSubtree(record *node, nodes map[string]*node) {
	record.revoked = true
	for _, childID := range record.children {
		if child, ok := nodes[childID]; ok && !child.revoked {
			revokeSubtree(child, nodes)
		}
	}
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}
