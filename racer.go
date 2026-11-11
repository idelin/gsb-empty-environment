package netsec

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrAllFailed is returned when every policy-approved candidate failed to connect.
	ErrAllFailed = errors.New("netsec: all candidates failed")
	// ErrNoUsableAddress is returned when no address passed policy validation.
	ErrNoUsableAddress = errors.New("netsec: no address passed policy validation")
	// ErrPolicyRejected marks a per-address result rejected by tenant policy.
	ErrPolicyRejected = errors.New("netsec: address rejected by tenant policy")
	// ErrPolicyRevoked is returned when the round's policy version was revoked.
	ErrPolicyRevoked = errors.New("netsec: policy version revoked")
)

// Conn is a verified outbound connection handed to the caller.
type Conn interface {
	Close() error
}

// Dialer dials a single target address.
type Dialer interface {
	Dial(ctx context.Context, addr string) (Conn, error)
}

// DialFunc adapts a function to Dialer.
type DialFunc func(ctx context.Context, addr string) (Conn, error)

// Dial implements Dialer.
func (f DialFunc) Dial(ctx context.Context, addr string) (Conn, error) { return f(ctx, addr) }

// Policy decides whether addr may be dialed under a tenant policy version.
type Policy interface {
	Allows(version, addr string) bool
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(version, addr string) bool

// Allows implements Policy.
func (f PolicyFunc) Allows(version, addr string) bool { return f(version, addr) }

// Round pins one resolution round: a fixed address set, the tenant policy
// version they were validated against, and a deadline for the whole round.
type Round struct {
	Addrs         []string
	PolicyVersion string
	Deadline      time.Time
}

// AddressResult reports the outcome for a single address of a round.
type AddressResult struct {
	Addr   string
	Dialed bool
	Err    error
}

// Racer runs connection races for an outbound host.
type Racer struct {
	dialer Dialer
	policy Policy

	mu      sync.Mutex
	revoked map[string]struct{}
	active  map[*round]struct{}
}

// NewRacer builds a Racer that dials with d and validates with p.
func NewRacer(d Dialer, p Policy) *Racer {
	return &Racer{
		dialer:  d,
		policy:  p,
		revoked: make(map[string]struct{}),
		active:  make(map[*round]struct{}),
	}
}

// Revoke marks a tenant policy version as revoked. Active rounds pinned to
// that version are canceled; late successes from them are closed, never
// delivered. Future rounds on the version fail fast.
func (r *Racer) Revoke(version string) {
	r.mu.Lock()
	r.revoked[version] = struct{}{}
	var rounds []*round
	for rd := range r.active {
		if rd.version == version {
			rounds = append(rounds, rd)
		}
	}
	r.mu.Unlock()
	for _, rd := range rounds {
		rd.cancel(ErrPolicyRevoked)
	}
}

// Connect runs one round: it validates cfg.Addrs against the pinned policy
// version, dials the survivors in parallel, and returns at most one verified
// connection. Late successes — including callbacks from canceled rounds — are
// closed immediately. If every candidate fails, Connect returns the
// per-address results together with ErrAllFailed.
func (r *Racer) Connect(ctx context.Context, cfg Round) (Conn, []AddressResult, error) {
	results := make([]AddressResult, len(cfg.Addrs))

	r.mu.Lock()
	_, revoked := r.revoked[cfg.PolicyVersion]
	r.mu.Unlock()

	var candidates []int
	for i, addr := range cfg.Addrs {
		results[i].Addr = addr
		switch {
		case revoked:
			results[i].Err = ErrPolicyRevoked
		case !r.policy.Allows(cfg.PolicyVersion, addr):
			results[i].Err = ErrPolicyRejected
		default:
			results[i].Dialed = true
			candidates = append(candidates, i)
		}
	}
	if revoked {
		return nil, results, ErrPolicyRevoked
	}
	if len(candidates) == 0 {
		return nil, results, ErrNoUsableAddress
	}

	var dialCtx context.Context
	var cancel context.CancelFunc
	if cfg.Deadline.IsZero() {
		dialCtx, cancel = context.WithCancel(ctx)
	} else {
		dialCtx, cancel = context.WithDeadline(ctx, cfg.Deadline)
	}

	rd := &round{
		version:   cfg.PolicyVersion,
		stopDial:  cancel,
		remaining: len(candidates),
		results:   results,
		done:      make(chan struct{}),
	}

	r.mu.Lock()
	r.active[rd] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.active, rd)
		r.mu.Unlock()
		cancel()
	}()

	for _, idx := range candidates {
		go func() {
			c, err := r.dialer.Dial(dialCtx, cfg.Addrs[idx])
			rd.finish(idx, c, err)
		}()
	}

	select {
	case <-rd.done:
	case <-dialCtx.Done():
		rd.cancel(dialCtx.Err())
		<-rd.done
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()
	switch rd.state {
	case stateWon:
		return rd.winner, nil, nil
	case stateFailed:
		return nil, rd.results, ErrAllFailed
	default:
		return nil, rd.results, rd.termErr
	}
}

type roundState int

const (
	statePending roundState = iota
	stateWon
	stateFailed
	stateCanceled
)

// round serializes cancel against win through a single mutex: whichever
// transition leaves statePending first defines the round's outcome.
type round struct {
	version string

	mu        sync.Mutex
	state     roundState
	stopDial  context.CancelFunc
	remaining int
	results   []AddressResult
	winner    Conn
	termErr   error
	done      chan struct{}
	doneOnce  sync.Once
}

func (rd *round) finish(idx int, c Conn, err error) {
	if err == nil && c == nil {
		err = errors.New("netsec: dialer returned nil connection")
	}
	rd.mu.Lock()
	if rd.state != statePending {
		// Late callback from an already-decided round: never deliver.
		rd.mu.Unlock()
		if c != nil {
			c.Close()
		}
		return
	}
	if err == nil {
		rd.state = stateWon
		rd.winner = c
		rd.stopDial()
		rd.mu.Unlock()
		rd.signal()
		return
	}
	rd.results[idx].Err = err
	rd.remaining--
	if rd.remaining == 0 {
		rd.state = stateFailed
		rd.mu.Unlock()
		rd.signal()
		return
	}
	rd.mu.Unlock()
}

func (rd *round) cancel(err error) {
	rd.mu.Lock()
	if rd.state != statePending {
		rd.mu.Unlock()
		return
	}
	rd.state = stateCanceled
	rd.termErr = err
	rd.stopDial()
	rd.mu.Unlock()
	rd.signal()
}

func (rd *round) signal() {
	rd.doneOnce.Do(func() { close(rd.done) })
}
