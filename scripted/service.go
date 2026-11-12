// Package scripted provides an in-memory, scriptable directory service for
// testing the netsec lease layer. It implements netsec.Conn while giving
// tests precise control over bind outcomes, search terminal timing and
// late/injected protocol messages.
package scripted

import (
	"context"
	"errors"
	"sync"
	"time"

	"example.org/netsec/q0422"
)

// ErrInvalidCredentials mirrors a directory invalid-credentials response.
var ErrInvalidCredentials = errors.New("scripted: invalid credentials")

// ErrConnClosed is returned by Recv once a connection is closed and its
// inbox has drained.
var ErrConnClosed = errors.New("scripted: connection closed")

// BindRule scripts the server-side behavior of one bind identity.
type BindRule struct {
	Password string
	Tenant   string
	// Err forces the bind response to fail.
	Err error
	// WhoAmI overrides the principal reported by the WhoAmI extended
	// operation. Empty means the server reports the bound username.
	WhoAmI string
	// WhoAmIErr forces identity confirmation to fail.
	WhoAmIErr error
}

// SearchPlan scripts the entries returned for searches under a tenant.
type SearchPlan struct {
	Tenant string
	// Entries are streamed, one envelope each, before the terminal result.
	Entries []*netsec.Entry
	// HoldTerminal leaves the search result-done message parked until the
	// test calls Conn.ReleaseTerminal, simulating an un-terminated query.
	HoldTerminal bool
}

// BindRecord is one observed bind attempt.
type BindRecord struct {
	ConnID   int
	Username string
	Password string
	Tenant   string
	At       time.Time
}

// Service hands out scripted connections sharing scripted behavior.
type Service struct {
	mu sync.Mutex

	rules map[string]BindRule
	plans map[string]SearchPlan

	dials   int
	conns   []*Conn
	records []BindRecord
}

// NewService creates an empty scripted service.
func NewService() *Service {
	return &Service{
		rules: make(map[string]BindRule),
		plans: make(map[string]SearchPlan),
	}
}

// AddBindRule registers the behavior for one bind username.
func (s *Service) AddBindRule(username string, rule BindRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[username] = rule
}

// SetSearchPlan registers (or replaces) the search plan for a tenant.
func (s *Service) SetSearchPlan(plan SearchPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.Tenant] = plan
}

// Dialer returns a netsec.Dialer backed by this service.
func (s *Service) Dialer() netsec.Dialer {
	return func(ctx context.Context) (netsec.Conn, error) {
		return s.Dial(), nil
	}
}

// Dial opens a fresh scripted connection.
func (s *Service) Dial() *Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dials++
	c := &Conn{
		service:   s,
		id:        s.dials,
		inbox:     make(chan *Envelope, 256),
		nextOpID:  1,
		terminal:  make(map[int]chan struct{}),
		closeOnce: make(chan struct{}),
	}
	s.conns = append(s.conns, c)
	return c
}

// Dials reports how many connections the service has handed out.
func (s *Service) Dials() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// Conns returns all connections ever opened.
func (s *Service) Conns() []*Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Conn, len(s.conns))
	copy(out, s.conns)
	return out
}

// BindRecords returns a copy of observed bind attempts.
func (s *Service) BindRecords() []BindRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BindRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *Service) snapshotRule(username string) (BindRule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rules[username]
	return r, ok
}

func (s *Service) snapshotPlan(tenant string) SearchPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plans[tenant]
}

func (s *Service) record(r BindRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}
