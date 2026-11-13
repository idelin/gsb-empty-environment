// Package bmc implements a secured best master clock election (BMCA) with
// admission control for an IEEE 1588 boundary clock.
//
// An Announce message only enters the election after passing admission:
// source-port authentication, grandmaster authorization and a stepsRemoved
// range check. Admitted messages are ranked by the grandmaster properties,
// with per-path tie-breaking, and grandmasters that have not announced within
// the announcement timeout are evicted.
package bmc

import (
	"bytes"
	"cmp"
	"sort"
	"sync"
	"time"
)

// AnnounceTimeout is the period after which a grandmaster is removed from the
// candidate set when no Announce message has been received from its port.
const AnnounceTimeout = 4 * time.Second

// MaxStepsRemoved is the exclusive upper bound of an acceptable stepsRemoved
// value: an Announce with stepsRemoved >= MaxStepsRemoved is discarded.
const MaxStepsRemoved uint16 = 255

const defaultSweepInterval = 250 * time.Millisecond

// ClockIdentity is an IEEE 1588 grandmaster clock identity, conventionally
// eight bytes. Identities are compared lexically in byte order.
type ClockIdentity []byte

// PortIdentity identifies the source port of an Announce message.
type PortIdentity struct {
	ClockIdentity ClockIdentity
	PortNumber    uint16
}

// Announce carries the fields of an Announce message used by admission
// control and the best master clock election.
type Announce struct {
	GrandmasterPriority1               uint8
	GrandmasterClockClass              uint8
	GrandmasterClockAccuracy           uint8
	GrandmasterOffsetScaledLogVariance uint16
	GrandmasterPriority2               uint8
	GrandmasterIdentity                ClockIdentity
	StepsRemoved                       uint16
	SourcePort                         PortIdentity
}

// Authenticator reports whether the given source port is authenticated. It is
// invoked before every other admission check. A nil result rejects the
// message (fail closed).
type Authenticator func(source PortIdentity) bool

// RejectReason explains why an Announce message was refused admission.
type RejectReason int

const (
	// RejectNone marks an admitted message.
	RejectNone RejectReason = iota
	// RejectAuthenticationFailed means the source port did not authenticate.
	RejectAuthenticationFailed
	// RejectUnauthorizedGrandmaster means the grandmaster identity is not in
	// the caller-provided authorization set.
	RejectUnauthorizedGrandmaster
	// RejectStepsRemovedOutOfRange means stepsRemoved is greater than or equal
	// to MaxStepsRemoved.
	RejectStepsRemovedOutOfRange
)

// String returns a human-readable rejection reason.
func (r RejectReason) String() string {
	switch r {
	case RejectAuthenticationFailed:
		return "authentication failed"
	case RejectUnauthorizedGrandmaster:
		return "grandmaster identity not authorized"
	case RejectStepsRemovedOutOfRange:
		return "stepsRemoved out of range"
	default:
		return "admitted"
	}
}

// Candidate is an admitted Announce message together with the time it was last
// observed.
type Candidate struct {
	Announce Announce
	LastSeen time.Time
}

// Option customizes an Elector at construction time.
type Option func(*Elector)

// WithClock overrides the time source. It is mainly useful in tests.
func WithClock(now func() time.Time) Option {
	return func(e *Elector) {
		if now != nil {
			e.now = now
		}
	}
}

// WithAnnounceTimeout overrides the default AnnounceTimeout.
func WithAnnounceTimeout(timeout time.Duration) Option {
	return func(e *Elector) {
		if timeout > 0 {
			e.timeout = timeout
		}
	}
}

// Elector performs admission control and best master election. It is safe for
// concurrent use.
type Elector struct {
	mu           sync.RWMutex
	authorized   map[string]bool
	authenticate Authenticator
	entries      map[portKey]*Candidate
	now          func() time.Time
	timeout      time.Duration
}

type portKey struct {
	clock string
	port  uint16
}

// NewElector builds an Elector. authorizedGrandmasters is the set of
// grandmaster identities allowed to participate and authenticate verifies the
// source port of every message. A nil authenticator rejects every message.
func NewElector(authorizedGrandmasters []ClockIdentity, authenticate Authenticator, opts ...Option) *Elector {
	if authenticate == nil {
		authenticate = func(PortIdentity) bool { return false }
	}
	authorized := make(map[string]bool, len(authorizedGrandmasters))
	for _, identity := range authorizedGrandmasters {
		authorized[string(identity)] = true
	}
	e := &Elector{
		authorized:   authorized,
		authenticate: authenticate,
		entries:      make(map[portKey]*Candidate),
		now:          time.Now,
		timeout:      AnnounceTimeout,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// HandleAnnounce runs admission control on an Announce message and, when it
// passes, records it as a candidate keyed by its source port. Rejected
// messages never alter the candidate set.
func (e *Elector) HandleAnnounce(msg Announce) (admitted bool, reason RejectReason) {
	if !e.authenticate(msg.SourcePort) {
		return false, RejectAuthenticationFailed
	}
	if !e.authorized[string(msg.GrandmasterIdentity)] {
		return false, RejectUnauthorizedGrandmaster
	}
	if msg.StepsRemoved >= MaxStepsRemoved {
		return false, RejectStepsRemovedOutOfRange
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.entries[portIdentityKey(msg.SourcePort)] = &Candidate{
		Announce: cloneAnnounce(msg),
		LastSeen: e.now(),
	}
	return true, RejectNone
}

// Elect evicts stale grandmasters and returns the best remaining candidate, or
// nil when no candidate remains. The returned value is a copy that callers may
// modify freely.
func (e *Elector) Elect() *Candidate {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sweepLocked()
	var best *Candidate
	for _, candidate := range e.entries {
		if best == nil || compareCandidate(candidate, best) < 0 {
			best = candidate
		}
	}
	if best == nil {
		return nil
	}
	return cloneCandidate(best)
}

// Candidates evicts stale grandmasters and returns the best entry for each
// surviving grandmaster, ordered from best to worst. One grandmaster may be
// reachable through several source ports; only its best path is returned.
func (e *Elector) Candidates() []Candidate {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sweepLocked()
	bestByGrandmaster := make(map[string]*Candidate)
	for _, candidate := range e.entries {
		key := string(candidate.Announce.GrandmasterIdentity)
		incumbent := bestByGrandmaster[key]
		if incumbent == nil || compareCandidate(candidate, incumbent) < 0 {
			bestByGrandmaster[key] = candidate
		}
	}

	out := make([]Candidate, 0, len(bestByGrandmaster))
	for _, candidate := range bestByGrandmaster {
		out = append(out, *cloneCandidate(candidate))
	}
	sort.Slice(out, func(i, j int) bool {
		return compareCandidate(&out[i], &out[j]) < 0
	})
	return out
}

// Sweep removes every entry that has not announced within the timeout and
// returns the number of removed entries.
func (e *Elector) Sweep() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sweepLocked()
}

// StartSweeper periodically evicts stale entries in the background and returns
// a stop function that blocks until the background loop has exited. A
// non-positive interval uses a sensible default.
func (e *Elector) StartSweeper(interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = defaultSweepInterval
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.Sweep()
			case <-stopCh:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() { close(stopCh) })
		<-doneCh
	}
}

func (e *Elector) sweepLocked() int {
	deadline := e.now().Add(-e.timeout)
	removed := 0
	for key, candidate := range e.entries {
		if !candidate.LastSeen.After(deadline) {
			delete(e.entries, key)
			removed++
		}
	}
	return removed
}

// compareCandidate imposes the total election ordering. It returns a negative
// number when a ranks better than b, zero when they are equivalent and a
// positive number otherwise.
//
// The grandmaster vector is compared first (smaller values win), then the
// grandmaster identity bytes. For entries of the same grandmaster the path
// quality decides: smaller stepsRemoved, then the smaller source port
// identity.
func compareCandidate(a, b *Candidate) int {
	if c := cmp.Compare(a.Announce.GrandmasterPriority1, b.Announce.GrandmasterPriority1); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Announce.GrandmasterClockClass, b.Announce.GrandmasterClockClass); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Announce.GrandmasterClockAccuracy, b.Announce.GrandmasterClockAccuracy); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Announce.GrandmasterOffsetScaledLogVariance, b.Announce.GrandmasterOffsetScaledLogVariance); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Announce.GrandmasterPriority2, b.Announce.GrandmasterPriority2); c != 0 {
		return c
	}
	if c := bytes.Compare(a.Announce.GrandmasterIdentity, b.Announce.GrandmasterIdentity); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Announce.StepsRemoved, b.Announce.StepsRemoved); c != 0 {
		return c
	}
	return comparePortIdentity(a.Announce.SourcePort, b.Announce.SourcePort)
}

// comparePortIdentity orders port identities by their wire byte layout: the
// clock identity bytes first, then the port number.
func comparePortIdentity(a, b PortIdentity) int {
	if c := bytes.Compare(a.ClockIdentity, b.ClockIdentity); c != 0 {
		return c
	}
	return cmp.Compare(a.PortNumber, b.PortNumber)
}

func portIdentityKey(p PortIdentity) portKey {
	return portKey{clock: string(p.ClockIdentity), port: p.PortNumber}
}

func cloneCandidate(c *Candidate) *Candidate {
	cp := *c
	cp.Announce = cloneAnnounce(c.Announce)
	return &cp
}

func cloneAnnounce(msg Announce) Announce {
	msg.GrandmasterIdentity = cloneBytes(msg.GrandmasterIdentity)
	msg.SourcePort.ClockIdentity = cloneBytes(msg.SourcePort.ClockIdentity)
	return msg
}

func cloneBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}
