package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrRevoking is returned by Register while a revocation transaction is
	// in flight: the fence is up and no new mappings may be created.
	ErrRevoking = errors.New("netsec: revocation in progress, new mappings blocked")
	// ErrDuplicateMapping is returned when registering an already active id.
	ErrDuplicateMapping = errors.New("netsec: mapping id already registered")
	// ErrMappingQuarantined is returned when registering an id whose last
	// invalidation failed; the mapping stays isolated and must not be reused.
	ErrMappingQuarantined = errors.New("netsec: mapping id quarantined after failed invalidation")
	// ErrRevocationInFlight is returned when a second Revoke overlaps the first.
	ErrRevocationInFlight = errors.New("netsec: revocation transaction already in flight")
	// ErrIncompleteRevocation reports that one or more mappings failed
	// invalidation; those ids remain quarantined and unreadable-by-policy.
	ErrIncompleteRevocation = errors.New("netsec: revocation incomplete, failed mappings quarantined")
)

// Confirmation is the page driver's completion report for one invalidation
// request. Generation must echo the value passed to PageDriver.Invalidate so
// stale reports for a reused mapping id are recognised and dropped.
type Confirmation struct {
	MappingID  uint64
	Generation uint64
	// Err is nil when the mapping is guaranteed to be no longer readable.
	Err error
}

// PageDriver is implemented by the lower-level page driver. Invalidate is
// asynchronous: the driver must eventually report the outcome through
// Coordinator.Confirm, in any order.
type PageDriver interface {
	Invalidate(mappingID uint64, generation uint64) error
}

// RevokeResult summarises a completed revocation transaction.
type RevokeResult struct {
	// Revoked lists mapping ids confirmed unreadable, sorted ascending.
	Revoked []uint64
	// Failed lists mapping ids whose invalidation failed; they remain
	// quarantined and cannot be registered again.
	Failed []uint64
}

type mappingState struct {
	generation  uint64
	quarantined bool
}

type transaction struct {
	pending map[uint64]uint64 // mapping id -> generation awaiting confirmation
	revoked []uint64
	failed  []uint64
	done    chan struct{}
}

// Coordinator owns the mapping registry and runs revocation transactions.
type Coordinator struct {
	mu       sync.Mutex
	driver   PageDriver
	mappings map[uint64]*mappingState
	nextGen  uint64
	fenced   bool
	txn      *transaction
}

// NewCoordinator returns a Coordinator that issues invalidations to driver.
func NewCoordinator(driver PageDriver) *Coordinator {
	return &Coordinator{
		driver:   driver,
		mappings: make(map[uint64]*mappingState),
	}
}

// Register admits a new shared-page mapping for a tenant. It fails while a
// revocation fence is up, while the id is already active, or when the id is
// quarantined after a failed invalidation.
func (c *Coordinator) Register(mappingID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fenced {
		return ErrRevoking
	}
	if st, ok := c.mappings[mappingID]; ok {
		if st.quarantined {
			return ErrMappingQuarantined
		}
		return ErrDuplicateMapping
	}
	c.nextGen++
	c.mappings[mappingID] = &mappingState{generation: c.nextGen}
	return nil
}

// Release removes a mapping that is no longer needed outside of revocation.
func (c *Coordinator) Release(mappingID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fenced {
		return
	}
	if st, ok := c.mappings[mappingID]; ok && !st.quarantined {
		delete(c.mappings, mappingID)
	}
}

// Revoke runs a revocation transaction: it fences off new mappings, requests
// invalidation of every registered mapping, and blocks until every one of
// them has been confirmed by the driver. Only then does it report success.
// Mappings whose invalidation failed stay quarantined: they remain registered
// as isolated and their ids can never be registered again.
func (c *Coordinator) Revoke(ctx context.Context) (RevokeResult, error) {
	c.mu.Lock()
	if c.txn != nil {
		c.mu.Unlock()
		return RevokeResult{}, ErrRevocationInFlight
	}
	c.fenced = true
	txn := &transaction{
		pending: make(map[uint64]uint64, len(c.mappings)),
		done:    make(chan struct{}),
	}
	for id, st := range c.mappings {
		if !st.quarantined {
			txn.pending[id] = st.generation
		}
	}
	c.txn = txn
	snapshot := make([]Confirmation, 0, len(txn.pending))
	for id, gen := range txn.pending {
		snapshot = append(snapshot, Confirmation{MappingID: id, Generation: gen})
	}
	c.mu.Unlock()

	for _, req := range snapshot {
		if err := c.driver.Invalidate(req.MappingID, req.Generation); err != nil {
			req.Err = err
			c.Confirm(req)
		}
	}

	select {
	case <-txn.done:
	case <-ctx.Done():
		// The fence stays up and the transaction keeps tracking late
		// confirmations; a cancelled Revoke never lifts isolation early.
		return RevokeResult{}, ctx.Err()
	}

	c.mu.Lock()
	res := RevokeResult{Revoked: txn.revoked, Failed: txn.failed}
	for _, id := range txn.revoked {
		delete(c.mappings, id)
	}
	for _, id := range txn.failed {
		if st, ok := c.mappings[id]; ok {
			st.quarantined = true
		}
	}
	c.txn = nil
	c.fenced = false
	c.mu.Unlock()

	sort.Slice(res.Revoked, func(i, j int) bool { return res.Revoked[i] < res.Revoked[j] })
	sort.Slice(res.Failed, func(i, j int) bool { return res.Failed[i] < res.Failed[j] })
	if len(res.Failed) > 0 {
		return res, fmt.Errorf("%w: ids %v", ErrIncompleteRevocation, res.Failed)
	}
	return res, nil
}

// Confirm consumes a driver confirmation. Reports for unknown ids, for a
// finished transaction, or with a stale generation (id reused after a new
// registration) are ignored.
func (c *Coordinator) Confirm(cf Confirmation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.txn == nil {
		return
	}
	gen, ok := c.txn.pending[cf.MappingID]
	if !ok || gen != cf.Generation {
		return
	}
	delete(c.txn.pending, cf.MappingID)
	if cf.Err != nil {
		c.txn.failed = append(c.txn.failed, cf.MappingID)
	} else {
		c.txn.revoked = append(c.txn.revoked, cf.MappingID)
	}
	if len(c.txn.pending) == 0 {
		close(c.txn.done)
	}
}

// Quarantined reports whether the id is isolated after a failed invalidation.
func (c *Coordinator) Quarantined(mappingID uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.mappings[mappingID]
	return ok && st.quarantined
}

// Active reports whether the id is a live registered mapping.
func (c *Coordinator) Active(mappingID uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.mappings[mappingID]
	return ok && !st.quarantined
}
