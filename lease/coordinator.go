package lease

import (
	"fmt"
	"sync"
	"time"
)

type leaseState struct {
	Lease
	frozen           bool
	pendingCandidate string
	pendingTerm      uint64
}

// Coordinator is the coordination center that owns lease state.
// All methods are safe for concurrent use.
type Coordinator struct {
	mu     sync.Mutex
	clock  Clock
	leases map[string]*leaseState
}

// NewCoordinator creates a Coordinator. A nil clock uses wall time.
func NewCoordinator(clock Clock) *Coordinator {
	if clock == nil {
		clock = realClock{}
	}
	return &Coordinator{clock: clock, leases: make(map[string]*leaseState)}
}

// Acquire grants the lease to holderID for ttl. It fails while another
// holder's lease is still live.
func (c *Coordinator) Acquire(leaseID, holderID string, ttl time.Duration) (*Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	var term uint64 = 1
	if st, ok := c.leases[leaseID]; ok {
		if now.Before(st.ExpiresAt) {
			return nil, fmt.Errorf("%w: %q holds %q", ErrLeaseHeld, st.HolderID, leaseID)
		}
		term = st.Term + 1
	}
	st := &leaseState{Lease: Lease{
		ID:         leaseID,
		HolderID:   holderID,
		Term:       term,
		AcquiredAt: now,
		ExpiresAt:  now.Add(ttl),
		Context:    map[string]string{},
	}}
	c.leases[leaseID] = st
	return st.snapshot(), nil
}

// Renew dynamically extends the lease; ttl may differ on every call.
func (c *Coordinator) Renew(leaseID, holderID string, term uint64, ttl time.Duration) (*Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.checkLocked(leaseID, holderID, term)
	if err != nil {
		return nil, err
	}
	if st.frozen {
		return nil, ErrLeaseFrozen
	}
	st.ExpiresAt = c.clock.Now().Add(ttl)
	st.Renewals++
	return st.snapshot(), nil
}

// PrepareHandover freezes the lease and issues a HandoverPackage naming the
// candidate. While frozen, renewals and writes are rejected so no state can
// change underneath the incoming holder.
func (c *Coordinator) PrepareHandover(leaseID, holderID string, term uint64, candidateID string) (*HandoverPackage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.checkLocked(leaseID, holderID, term)
	if err != nil {
		return nil, err
	}
	if st.frozen {
		return nil, ErrLeaseFrozen
	}
	if candidateID == holderID {
		return nil, fmt.Errorf("%w: candidate equals current holder", ErrInvalidCandidate)
	}
	now := c.clock.Now()
	st.frozen = true
	st.pendingCandidate = candidateID
	st.pendingTerm = st.Term + 1
	return &HandoverPackage{
		LeaseID:      leaseID,
		FromHolder:   holderID,
		ToCandidate:  candidateID,
		Term:         st.pendingTerm,
		IssuedAt:     now,
		ExpiresAt:    st.ExpiresAt,
		RemainingTTL: st.ExpiresAt.Sub(now),
		Context:      cloneContext(st.Context),
	}, nil
}

// CompleteHandover atomically moves ownership to the candidate. The
// candidate inherits the remaining TTL and lease context; the fencing token
// advances so the old holder's credential becomes invalid immediately.
func (c *Coordinator) CompleteHandover(pkg *HandoverPackage, candidateID string) (*Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.leases[pkg.LeaseID]
	if !ok || !st.frozen {
		return nil, ErrNoHandover
	}
	if st.pendingCandidate != candidateID || pkg.ToCandidate != candidateID {
		return nil, fmt.Errorf("%w: %q is not the designated candidate", ErrInvalidCandidate, candidateID)
	}
	if pkg.Term != st.pendingTerm {
		return nil, fmt.Errorf("%w: package term %d, pending %d", ErrInvalidTerm, pkg.Term, st.pendingTerm)
	}
	if !c.clock.Now().Before(st.ExpiresAt) {
		return nil, ErrLeaseExpired
	}
	st.HolderID = candidateID
	st.Term = pkg.Term
	st.frozen = false
	st.pendingCandidate = ""
	st.pendingTerm = 0
	st.Context = cloneContext(pkg.Context)
	return st.snapshot(), nil
}

// AbortHandover cancels a pending handover (e.g. candidate unreachable) and
// returns control to the original holder.
func (c *Coordinator) AbortHandover(leaseID, holderID string, term uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.checkLocked(leaseID, holderID, term)
	if err != nil {
		return err
	}
	st.frozen = false
	st.pendingCandidate = ""
	st.pendingTerm = 0
	return nil
}

// Release voluntarily gives up the lease without naming a successor.
func (c *Coordinator) Release(leaseID, holderID string, term uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.checkLocked(leaseID, holderID, term); err != nil {
		return err
	}
	delete(c.leases, leaseID)
	return nil
}

// Validate authorizes a business write: holder, fencing token, expiry and
// freeze state must all be current.
func (c *Coordinator) Validate(leaseID, holderID string, term uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.checkLocked(leaseID, holderID, term)
	if err != nil {
		return err
	}
	if st.frozen {
		return ErrLeaseFrozen
	}
	return nil
}

// PutContext stores business context on the lease; it is inherited across
// handovers so the successor resumes exactly where the predecessor stopped.
func (c *Coordinator) PutContext(leaseID, holderID string, term uint64, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.checkLocked(leaseID, holderID, term)
	if err != nil {
		return err
	}
	if st.frozen {
		return ErrLeaseFrozen
	}
	st.Context[key] = value
	return nil
}

// Holder reports the current holder and whether the lease is live and
// serving (not frozen).
func (c *Coordinator) Holder(leaseID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.leases[leaseID]
	if !ok || st.frozen || !c.clock.Now().Before(st.ExpiresAt) {
		return "", false
	}
	return st.HolderID, true
}

// checkLocked validates fencing token, holder identity and expiry. The term
// is checked first so that a stale credential is always reported as a
// fencing-token violation, even after ownership has moved on.
func (c *Coordinator) checkLocked(leaseID, holderID string, term uint64) (*leaseState, error) {
	st, ok := c.leases[leaseID]
	if !ok {
		return nil, ErrNotHolder
	}
	if st.Term != term {
		return nil, fmt.Errorf("%w: have %d, current %d", ErrInvalidTerm, term, st.Term)
	}
	if st.HolderID != holderID {
		return nil, fmt.Errorf("%w: held by %q", ErrNotHolder, st.HolderID)
	}
	if !c.clock.Now().Before(st.ExpiresAt) {
		return nil, ErrLeaseExpired
	}
	return st, nil
}
