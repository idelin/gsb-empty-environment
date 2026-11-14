package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"sort"
	"sync"
	"time"
)

// GatewayConfig configures a multi-connection edge gateway. The gateway owns a
// single CID tombstone ledger and generator so values cannot collide across
// connections while in quarantine.
type GatewayConfig struct {
	Clock             Clock
	Generator         CIDGenerator
	MaxPacketLifetime time.Duration
	ConfirmationGrace time.Duration
	MinCIDLength      int
	MaxCIDLength      int
	MasterSecret      []byte
	Ledger            *CIDLedger
	TokenLedger       *TokenLedger
	RecoveryKey       []byte
	// JournalFactory creates a per-connection journal. Each connection has an
	// independent hash chain because gateway-level transactions are not
	// defined by QUIC.
	JournalFactory func(connectionID string) (Journal, error)
}

// GatewayConnection is an open connection plus its lifecycle manager.
type GatewayConnection struct {
	ID      string
	Route   RoutePartition
	Manager *Manager
}

// Gateway is a registry of per-connection managers sharing one CID ledger.
type Gateway struct {
	mu          sync.Mutex
	clock       Clock
	generator   CIDGenerator
	ledger      *CIDLedger
	tokenLedger *TokenLedger
	cfg         GatewayConfig
	nextID      uint64
	conns       map[string]*GatewayConnection
}

type lockedCIDGenerator struct {
	mu sync.Mutex
	g  CIDGenerator
}

func (l *lockedCIDGenerator) NextCID() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.g.NextCID()
}

// NewGateway creates a gateway. If no generator is supplied, a shared random
// generator is created. MasterSecret should be persisted for crash recovery.
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock{}
	}
	generator := cfg.Generator
	if generator == nil {
		var err error
		generator, err = NewRandomCIDGenerator(rand.Reader, 16, nil)
		if err != nil {
			return nil, err
		}
	}
	generator = &lockedCIDGenerator{g: generator}
	ledger := cfg.Ledger
	if ledger == nil {
		ledger = NewCIDLedger()
	}
	tokenLedger := cfg.TokenLedger
	if tokenLedger == nil {
		tokenLedger = NewTokenLedger()
	}
	cfg.Clock = clock
	cfg.Generator = generator
	cfg.Ledger = ledger
	cfg.TokenLedger = tokenLedger
	return &Gateway{
		clock:       clock,
		generator:   generator,
		ledger:      ledger,
		tokenLedger: tokenLedger,
		cfg:         cfg,
		conns:       make(map[string]*GatewayConnection),
	}, nil
}

// OpenConnection creates an auto-named connection in a routing partition.
func (g *Gateway) OpenConnection(route RoutePartition) (*GatewayConnection, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	return g.openLocked(fmt.Sprintf("conn-%d", g.nextID), route)
}

// OpenNamedConnection creates a deterministic connection identifier, which is
// useful for stacks that map connection state to an external session ID.
func (g *Gateway) OpenNamedConnection(id string, route RoutePartition) (*GatewayConnection, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty connection ID", ErrInvalidConfig)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.openLocked(id, route)
}

func (g *Gateway) openLocked(id string, route RoutePartition) (*GatewayConnection, error) {
	if _, exists := g.conns[id]; exists {
		return nil, fmt.Errorf("netsec: connection %q already exists", id)
	}
	var journal Journal
	if g.cfg.JournalFactory != nil {
		j, err := g.cfg.JournalFactory(id)
		if err != nil {
			return nil, err
		}
		journal = j
	}
	m, err := NewManager(Config{
		Clock:             g.clock,
		Generator:         g.generator,
		Route:             route,
		MaxPacketLifetime: g.cfg.MaxPacketLifetime,
		ConfirmationGrace: g.cfg.ConfirmationGrace,
		MinCIDLength:      g.cfg.MinCIDLength,
		MaxCIDLength:      g.cfg.MaxCIDLength,
		MasterSecret:      g.cfg.MasterSecret,
		Ledger:            g.ledger,
		TokenLedger:       g.tokenLedger,
		Journal:           journal,
		RecoveryKey:       g.cfg.RecoveryKey,
		Label:             id,
	})
	if err != nil {
		return nil, err
	}
	conn := &GatewayConnection{ID: id, Route: route, Manager: m}
	g.conns[id] = conn
	return conn, nil
}

// Connection looks up an open connection.
func (g *Gateway) Connection(id string) (*GatewayConnection, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.conns[id]
	return c, ok
}

// CloseConnection closes one connection but keeps its quarantined tokens in
// the registry until they expire, so late old-token replays still resolve to
// the old connection rather than any newer one.
func (g *Gateway) CloseConnection(id, reason string) (CloseResult, error) {
	g.mu.Lock()
	c, ok := g.conns[id]
	g.mu.Unlock()
	if !ok {
		return CloseResult{}, fmt.Errorf("netsec: connection %q not found", id)
	}
	return c.Manager.Close(reason)
}

// GatewayResetDecision combines the per-connection decision with the gateway
// connection that owned the matched token.
type GatewayResetDecision struct {
	ResetDecision
	ConnectionID string
	Candidates   int
}

// DetectReset checks every live token in the gateway. Exactly one matching
// CID is required; a cross-connection collision is ambiguous and closes
// nothing. Tokens are also scoped to the route partition in ResetOptions.
func (g *Gateway) DetectReset(packet []byte, opts ResetOptions) GatewayResetDecision {
	decision := GatewayResetDecision{ResetDecision: ResetDecision{Route: opts.Route}}
	if len(packet) < MinStatelessResetPacketSize {
		decision.Reason = ResetTooShort
		return decision
	}
	if packet[0]&0x40 == 0 {
		decision.Reason = ResetBadFixedBit
		return decision
	}
	if packet[0]&0x80 != 0 {
		decision.Reason = ResetLongHeader
		return decision
	}
	if opts.Probe != nil {
		if opts.Probe(packet) {
			decision.Reason = ResetDecryptable
			return decision
		}
	} else if !opts.CallerVerifiedUndecryptable {
		decision.Reason = ResetNoProbe
		return decision
	}

	var tail ResetToken
	copy(tail[:], packet[len(packet)-StatelessResetTokenSize:])
	g.mu.Lock()
	conns := make([]*GatewayConnection, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })

	var owners []*GatewayConnection
	for _, c := range conns {
		n, err := c.Manager.resetCandidateCount(tail, packet[len(packet)-StatelessResetTokenSize:], opts.Route)
		if err != nil {
			decision.Reason = ResetLogFailure
			decision.Err = err
			return decision
		}
		if n > 0 {
			owners = append(owners, c)
			if n > 1 {
				// Collision inside one manager is already ambiguous.
				decision.Reason = ResetAmbiguous
				decision.Candidates = n
				w, werr := c.Manager.recordResetAmbiguity(opts.Route, "multiple live CIDs share the trailing token")
				if werr != nil {
					decision.Err = werr
					decision.Reason = ResetLogFailure
					return decision
				}
				decision.Witness = &w
				return decision
			}
		}
	}
	decision.Candidates = len(owners)
	if len(owners) == 0 {
		decision.Reason = ResetNoToken
		return decision
	}
	if len(owners) > 1 {
		decision.Reason = ResetAmbiguous
		w, werr := owners[0].Manager.recordResetAmbiguity(opts.Route, "token matches multiple gateway connections")
		if werr != nil {
			decision.Err = werr
			decision.Reason = ResetLogFailure
			return decision
		}
		decision.Witness = &w
		return decision
	}
	owner := owners[0]
	sub := owner.Manager.DetectReset(packet, opts)
	decision.ResetDecision = sub
	decision.ConnectionID = owner.ID
	return decision
}

// resetCandidateCount revalidates a token under the manager lock without
// closing. It is used by the gateway to detect cross-connection collisions
// before any state transition is attempted.
func (m *Manager) resetCandidateCount(token ResetToken, tail []byte, route RoutePartition) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return 0, err
	}
	count := 0
	for _, rec := range m.tokens[token] {
		if rec.state == CIDExpired || rec.state == CIDNone {
			continue
		}
		if subtle.ConstantTimeCompare(rec.token[:], tail) == 1 && rec.route == route {
			count++
		}
	}
	return count, nil
}

func (m *Manager) recordResetAmbiguity(route RoutePartition, reason string) (AuditWitness, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWitnessLocked(AuditResetAmbiguous, nil, CIDNone, CIDNone, reason)
	w.Route = route
	w = m.resealWitnessLocked(w)
	if err := m.commitLocked(w, nil); err != nil {
		return AuditWitness{}, err
	}
	return w, nil
}
