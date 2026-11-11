package netsec

import (
	"errors"
	"sync"
)

// AmplificationFactor is the maximum ratio of bytes sent to bytes received
// on an unvalidated path.
const AmplificationFactor = 3

// ErrBudgetExhausted is returned by Send when the path has no send budget
// left for even a single byte.
var ErrBudgetExhausted = errors.New("netsec: amplification budget exhausted")

// Sender transmits a datagram to a remote address and reports how many
// bytes were actually written. It may write fewer bytes than len(b).
type Sender interface {
	SendTo(b []byte, addr string) (int, error)
}

// SenderFunc adapts a plain function to the Sender interface.
type SenderFunc func(b []byte, addr string) (int, error)

// SendTo implements Sender.
func (f SenderFunc) SendTo(b []byte, addr string) (int, error) { return f(b, addr) }

// Limiter hands out per-address send admission paths. Every distinct
// address gets an independent budget: when a client shows up from a new
// address, accounting starts from scratch and cannot reuse or interfere
// with the budget of any other address.
type Limiter struct {
	mu    sync.Mutex
	paths map[string]*Path
}

// NewLimiter returns a ready-to-use Limiter.
func NewLimiter() *Limiter {
	return &Limiter{paths: make(map[string]*Path)}
}

// Path returns the admission path for addr, creating an independent one on
// first use. Repeated calls with the same address return the same path, so
// all workers share a single budget per address.
func (l *Limiter) Path(addr string) *Path {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.paths[addr]
	if !ok {
		p = &Path{addr: addr}
		l.paths[addr] = p
	}
	return p
}

// Path is the send admission state for one remote address. All methods are
// safe for concurrent use by multiple worker goroutines.
type Path struct {
	addr string

	mu        sync.Mutex
	received  int64
	sent      int64
	reserved  int64
	validated bool
}

// Addr returns the remote address this path accounts for.
func (p *Path) Addr() string { return p.addr }

// AddReceived credits n bytes received from the remote address. Every
// receive event counts, including duplicate and retransmitted datagrams.
func (p *Path) AddReceived(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.received += int64(n)
	p.mu.Unlock()
}

// Validate marks the address as validated. The amplification limit stops
// applying immediately; in-flight reservations keep their accounting.
func (p *Path) Validate() {
	p.mu.Lock()
	p.validated = true
	p.mu.Unlock()
}

// Validated reports whether the address has been validated.
func (p *Path) Validated() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validated
}

// allowance is the number of bytes that may still be reserved.
// Callers must hold p.mu.
func (p *Path) allowance() int64 {
	a := AmplificationFactor*p.received - p.sent - p.reserved
	if a < 0 {
		return 0
	}
	return a
}

// Reserve atomically reserves up to n bytes of send budget and returns the
// granted amount, which is smaller than n (possibly zero) when the
// remaining budget does not cover the full request. The grant is deducted
// from the shared budget before any byte is sent, so concurrent workers
// can never overdraw it. Every granted byte must later be settled with
// exactly one of Commit (byte actually sent) or Release (byte not sent).
func (p *Path) Reserve(n int) int {
	if n <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	grant := int64(n)
	if !p.validated {
		if a := p.allowance(); grant > a {
			grant = a
		}
	}
	p.reserved += grant
	return int(grant)
}

// Commit records n previously reserved bytes as actually sent.
func (p *Path) Commit(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.reserved -= int64(n)
	p.sent += int64(n)
	p.mu.Unlock()
}

// Release returns n previously reserved bytes that were not sent to the
// shared budget.
func (p *Path) Release(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.reserved -= int64(n)
	p.mu.Unlock()
}

// Send admits b on the path and hands the granted prefix to s. The
// reservation happens atomically before s runs. Only the bytes s reports
// as written are charged against the budget; the unsent remainder of the
// reservation, which is everything on a failed send, is released back, so
// failed or partial sends never leak budget.
func (p *Path) Send(s Sender, b []byte) (int, error) {
	grant := p.Reserve(len(b))
	if grant == 0 {
		return 0, ErrBudgetExhausted
	}
	n, err := s.SendTo(b[:grant], p.addr)
	if n < 0 {
		n = 0
	}
	if n > grant {
		n = grant
	}
	p.Commit(n)
	p.Release(grant - n)
	return n, err
}

// Stats is a consistent snapshot of a path's accounting.
type Stats struct {
	Received  int64
	Sent      int64
	Reserved  int64
	Validated bool
}

// Stats returns an atomic snapshot of the path's counters.
func (p *Path) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Received:  p.received,
		Sent:      p.sent,
		Reserved:  p.reserved,
		Validated: p.validated,
	}
}
