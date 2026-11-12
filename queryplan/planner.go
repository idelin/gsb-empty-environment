package queryplan

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// AllFailedError is returned when every candidate terminates without a usable
// answer and the request context is still alive.
type AllFailedError struct {
	// Statuses holds the per-candidate terminal outcomes, in delegation order.
	Statuses []CandidateStatus
}

func (e *AllFailedError) Error() string {
	return "queryplan: all candidates failed"
}

// Planner owns the current delegation generation and the answer cache. It is
// safe for concurrent use: multiple Lookup calls may run across generations.
type Planner struct {
	upstream Upstream

	// candidateTimeout bounds each individual candidate attempt. Zero means
	// DefaultCandidateTimeout; negative means no per-candidate deadline beyond
	// whatever the Lookup context already carries.
	candidateTimeout time.Duration

	mu sync.RWMutex
	// delegation is the authoritative server set currently in force.
	delegation Delegation
	// cache stores one entry per cacheKey, tagged with the writing generation.
	cache map[string]cacheEntry

	// activePlans counts plans whose Lookup has not returned. It is the
	// deterministic counterpart of "no dangling waiters": once Lookup returns
	// every goroutine and channel receiver the plan owned has been drained.
	activePlans atomic.Int64
}

// Option customizes a Planner.
type Option func(*Planner)

// WithCandidateTimeout overrides DefaultCandidateTimeout. A negative duration
// disables the planner-owned per-candidate deadline.
func WithCandidateTimeout(d time.Duration) Option {
	return func(p *Planner) { p.candidateTimeout = d }
}

// NewPlanner creates a planner seeded with an initial delegation. Later
// ReplaceDelegation calls must use generation numbers strictly greater than
// the seed's.
func NewPlanner(upstream Upstream, seed Delegation, opts ...Option) *Planner {
	if upstream == nil {
		panic("queryplan: upstream is required")
	}
	p := &Planner{
		upstream:         upstream,
		delegation:       seed,
		cache:            make(map[string]cacheEntry),
		candidateTimeout: DefaultCandidateTimeout,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ReplaceDelegation installs a newer authoritative server set. The replacement
// generation must be strictly greater than the current one; equal or older
// generations are rejected so a stale referral cannot roll the clock back.
//
// Plans already in flight keep their pinned generation and continue to
// completion; they simply lose the ability to commit cache or referrals.
func (p *Planner) ReplaceDelegation(d Delegation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d.Generation <= p.delegation.Generation {
		return errors.New("queryplan: delegation generation is not newer")
	}
	p.delegation = d
	return nil
}

// CurrentGeneration reports the generation currently in force.
func (p *Planner) CurrentGeneration() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.delegation.Generation
}

// ActivePlans reports how many Lookup calls have not yet returned.
func (p *Planner) ActivePlans() int64 {
	return p.activePlans.Load()
}

// snapshot pins the delegation visible to a new plan.
func (p *Planner) snapshot() Delegation {
	p.mu.RLock()
	defer p.mu.RUnlock()
	candidates := make([]Candidate, len(p.delegation.Candidates))
	copy(candidates, p.delegation.Candidates)
	return Delegation{
		Generation: p.delegation.Generation,
		Candidates: candidates,
	}
}

// cachedAnswer returns a cache entry only when it was written by the
// generation the plan is pinned to. An entry from a newer delegation is never
// served to an older plan, and vice versa: each generation owns its cache.
func (p *Planner) cachedAnswer(key string, generation uint64) (cacheEntry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	entry, ok := p.cache[key]
	if !ok || entry.generation != generation {
		return cacheEntry{}, false
	}
	return entry, true
}

// commitAnswer stores a successful response and optionally installs a
// referral, but only while the response still matches the current generation.
// It returns true when the answer was cached.
func (p *Planner) commitAnswer(key string, generation uint64, resp *Response) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.delegation.Generation {
		// A newer delegation took over while the answer was in flight. The
		// answer may still complete its own request, but it must not touch
		// cache or referrals owned by the new generation.
		return false
	}
	p.cache[key] = cacheEntry{generation: generation, response: resp}
	if resp.NewDelegation != nil && resp.NewDelegation.Generation > p.delegation.Generation {
		p.delegation = *resp.NewDelegation
	}
	return true
}

// Lookup executes one query against the pinned delegation.
//
// The plan generation is fixed for the whole call: later delegation
// replacements neither retarget the candidates nor invalidate the returned
// answer, they only prevent that answer from updating the new cache.
func (p *Planner) Lookup(ctx context.Context, q Query) (*Result, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	delegation := p.snapshot()
	result := &Result{
		Query:      q,
		Generation: delegation.Generation,
		Candidates: make([]CandidateStatus, len(delegation.Candidates)),
	}

	if entry, ok := p.cachedAnswer(cacheKey(q), delegation.Generation); ok {
		result.Response = entry.response
		result.FromCache = true
		result.Elapsed = time.Since(start)
		return result, nil
	}

	if len(delegation.Candidates) == 0 {
		return nil, errors.New("queryplan: delegation has no candidates")
	}

	plan := p.startPlan(ctx, q, delegation, result, start)
	p.activePlans.Add(1)
	defer p.activePlans.Add(-1)
	return plan.run()
}
