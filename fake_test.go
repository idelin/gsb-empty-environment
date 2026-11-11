package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Symbol kinds used by the fake protocol.
const (
	symTicket SymID = iota + 1
	symNonce
	symSession
)

// Message kinds used by the fake protocol.
const (
	kindConnect   = "connect"
	kindIssue     = "issue"
	kindAuth      = "auth"
	kindHandshake = "handshake"
	kindNoise     = "noise"
)

// Fake-server failure classes. They deliberately differ from a timeout class.
var (
	errFakeHandshake = errors.New("handshake regression: bad final message")
	errFakeNonMono   = errors.New("non-monotone: handshake reached without required auth")
	errFakeOrphan    = errors.New("orphan consume: symbol unknown to this session")
	errFakeReuse     = errors.New("stale state: ticket from a previous session reused")
)

// fakeConfig decides when a fresh fake server fails a replay.
type fakeConfig struct {
	// nonMonotoneAt is the message count threshold below which an otherwise
	// complete handshake returns the non-monotone class instead.
	nonMonotoneAt int

	// timeoutAt forces a deadline style failure once the trace is this short.
	timeoutAt int
}

// fakeHarness hands out isolated in-memory server sessions.
type fakeHarness struct {
	cfg fakeConfig

	mu       sync.Mutex
	nextGen  uint64
	sessions map[uint64]*fakeSession
}

func newFakeHarness(cfg fakeConfig) *fakeHarness {
	return &fakeHarness{cfg: cfg, sessions: map[uint64]*fakeSession{}}
}

func (h *fakeHarness) NewSession() (Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextGen++
	s := &fakeSession{harness: h, gen: h.nextGen}
	h.sessions[s.gen] = s
	return s, nil
}

func (h *fakeHarness) generationCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

// fakeSession is a brand new server state: ticket/nonce/session tables start
// empty for every session, so any cross-session symbol reuse is detectable.
type fakeSession struct {
	harness *fakeHarness
	gen     uint64
	closed  bool
	runs    int

	tickets  map[SymID]uint64 // symbol -> issuing generation
	nonces   map[SymID]bool
	sessions map[SymID]bool
}

func (s *fakeSession) Generation() uint64 { return s.gen }

func (s *fakeSession) Run(ctx context.Context, msgs []Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.runs++
	s.tickets = map[SymID]uint64{}
	s.nonces = map[SymID]bool{}
	s.sessions = map[SymID]bool{}

	var sawHandshake bool

	for _, msg := range msgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, sym := range msg.Produces {
			switch sym {
			case symTicket:
				s.tickets[sym] = s.gen
			case symNonce:
				s.nonces[sym] = true
			case symSession:
				s.sessions[sym] = true
			}
		}
		for _, sym := range msg.Consumes {
			if !s.known(sym) {
				return fmt.Errorf("%w: %s consumes %d on session %d",
					errFakeOrphan, msg.Kind, sym, s.gen)
			}
			if gen, ok := s.tickets[sym]; ok && gen != s.gen {
				return fmt.Errorf("%w: ticket gen %d on session %d",
					errFakeReuse, gen, s.gen)
			}
		}

		switch msg.Kind {
		case kindHandshake:
			sawHandshake = true
		}
	}

	if s.harness.cfg.timeoutAt > 0 && len(msgs) <= s.harness.cfg.timeoutAt {
		return TimeoutError
	}

	if sawHandshake {
		switch {
		case s.harness.cfg.nonMonotoneAt > 0 && len(msgs) < s.harness.cfg.nonMonotoneAt:
			return errFakeNonMono
		}
		return errFakeHandshake
	}
	return nil
}

func (s *fakeSession) known(sym SymID) bool {
	switch sym {
	case symTicket:
		_, ok := s.tickets[sym]
		return ok
	case symNonce:
		return s.nonces[sym]
	case symSession:
		return s.sessions[sym]
	default:
		return false
	}
}

func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

// classify is the caller-provided failure classification for the fake
// protocol, including explicit timeout recognition.
func classify(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, TimeoutError):
		return "TIMEOUT", true
	default:
		return err.Error(), false
	}
}

// rewriteTicketRepair drops a dangling ticket consumption when the harness
// permits re-issuing one, and rejects the candidate otherwise.
func rewriteTicketRepair(msg Message, dangling []SymID) (Message, bool) {
	for _, sym := range dangling {
		if sym == symTicket {
			return DropConsuming(msg, dangling)
		}
	}
	return Message{}, false
}

// fullTrace builds a noisy multi-connection trace around one failing
// handshake. The ticket is produced on c1 and consumed on c2, exercising
// cross-connection producer/consumer edges; message 8 consumes the ticket
// twice and the two connect messages produce the same nonce symbol, giving
// duplicate consumes and duplicate producers.
func fullTrace() []Message {
	return []Message{
		{ID: 0, Conn: "c1", Kind: kindConnect, Produces: []SymID{symNonce}},
		{ID: 1, Conn: "c1", Kind: kindIssue, Consumes: []SymID{symNonce}, Produces: []SymID{symTicket}},
		{ID: 2, Conn: "c1", Kind: kindNoise, Consumes: []SymID{symNonce, symNonce}},
		{ID: 3, Conn: "c1", Kind: kindIssue, Consumes: []SymID{symNonce}, Produces: []SymID{symTicket}},
		{ID: 4, Conn: "c2", Kind: kindConnect, Produces: []SymID{symSession}},
		{ID: 5, Conn: "c2", Kind: kindNoise, Consumes: []SymID{symSession}},
		{ID: 6, Conn: "c2", Kind: kindAuth, Consumes: []SymID{symTicket}},
		{ID: 7, Conn: "c2", Kind: kindNoise, Consumes: []SymID{symTicket, symTicket}},
		{ID: 8, Conn: "c2", Kind: kindAuth, Consumes: []SymID{symTicket}},
		{ID: 9, Conn: "c2", Kind: kindHandshake, Consumes: []SymID{symTicket}},
	}
}
