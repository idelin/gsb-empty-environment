package lease

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Gateway is a region gateway: it holds one long, coarse lease from the
// root coordinator and mints many short, fine-grained sub-leases to the
// worker nodes in its region. All edge churn (joins, drops, renewals)
// is absorbed here and never reaches the root.
type Gateway struct {
	RegionID string

	root     *RootCoordinator
	quota    int64         // quota to request from the root
	leaseTTL time.Duration // root lease duration (long, e.g. 1m)
	subTTL   time.Duration // sub-lease duration (short, e.g. 5s)

	mu        sync.Mutex
	rootLease *Lease
	subs      map[string]*Lease
	seq       int64

	requests int64 // atomic: sub-lease RPCs handled (edge load absorbed here)
	reaped   int64 // atomic: sub-leases reclaimed after holder dropped

	stopCh  chan struct{}
	wg      sync.WaitGroup
	started bool
}

// NewGateway creates a region gateway. quota is the slice of cluster
// quota this region asks the root for; leaseTTL is the (long) root
// lease duration; subTTL is the (short) worker sub-lease duration.
func NewGateway(regionID string, root *RootCoordinator, quota int64, leaseTTL, subTTL time.Duration) *Gateway {
	return &Gateway{
		RegionID: regionID,
		root:     root,
		quota:    quota,
		leaseTTL: leaseTTL,
		subTTL:   subTTL,
		subs:     make(map[string]*Lease),
		stopCh:   make(chan struct{}),
	}
}

// Requests reports how many sub-lease RPCs this gateway has handled.
func (g *Gateway) Requests() int64 { return atomic.LoadInt64(&g.requests) }

// Reaped reports how many sub-leases were reclaimed locally after
// their holders dropped without releasing (timeout path).
func (g *Gateway) Reaped() int64 { return atomic.LoadInt64(&g.reaped) }

// RootLease returns a copy of the current root lease, or nil.
func (g *Gateway) RootLease() *Lease {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rootLease == nil {
		return nil
	}
	cp := *g.rootLease
	return &cp
}

// UsedQuota returns the quota currently lent out to workers.
func (g *Gateway) UsedQuota() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var used int64
	for _, l := range g.subs {
		used += l.Quota
	}
	return used
}

// ActiveSubLeases returns the number of live sub-leases.
func (g *Gateway) ActiveSubLeases() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.subs)
}

// AcquireRootLease (re)acquires this region's lease from the root.
// Safe to call when the previous lease was lost or expired.
func (g *Gateway) AcquireRootLease() error {
	l, err := g.root.AcquireRegionLease(g.RegionID, g.quota, g.leaseTTL)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.rootLease = l
	g.mu.Unlock()
	return nil
}

// RequestSubLease mints a short sub-lease for a worker, bounded by the
// quota of the root lease this gateway currently holds.
func (g *Gateway) RequestSubLease(workerID string, amount int64) (*Lease, error) {
	atomic.AddInt64(&g.requests, 1)
	if amount <= 0 {
		return nil, ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	g.reapExpiredLocked(now)
	if g.rootLease == nil || g.rootLease.Expired(now) {
		return nil, ErrNoRootLease
	}
	var used int64
	for _, l := range g.subs {
		used += l.Quota
	}
	if used+amount > g.rootLease.Quota {
		return nil, ErrQuotaExhausted
	}
	// A sub-lease may never outlive the root lease backing it.
	expires := now.Add(g.subTTL)
	if expires.After(g.rootLease.ExpiresAt) {
		expires = g.rootLease.ExpiresAt
	}
	g.seq++
	l := &Lease{
		ID:        fmt.Sprintf("%s-sub-%d", g.RegionID, g.seq),
		HolderID:  workerID,
		Quota:     amount,
		IssuedAt:  now,
		ExpiresAt: expires,
	}
	g.subs[l.ID] = l
	cp := *l
	return &cp, nil
}

// RenewSubLease extends a live sub-lease by the gateway's sub-lease TTL.
func (g *Gateway) RenewSubLease(leaseID string) (*Lease, error) {
	atomic.AddInt64(&g.requests, 1)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	l, ok := g.subs[leaseID]
	if !ok {
		return nil, ErrLeaseNotFound
	}
	if l.Expired(now) {
		delete(g.subs, leaseID)
		return nil, ErrLeaseNotFound
	}
	expires := now.Add(g.subTTL)
	if g.rootLease != nil && expires.After(g.rootLease.ExpiresAt) {
		expires = g.rootLease.ExpiresAt
	}
	l.ExpiresAt = expires
	cp := *l
	return &cp, nil
}

// ReleaseSubLease returns a worker's quota to the region pool.
func (g *Gateway) ReleaseSubLease(leaseID string) {
	atomic.AddInt64(&g.requests, 1)
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.subs, leaseID)
}

// ReapExpired reclaims all timed-out sub-leases locally. This is the
// ONLY thing that happens when an edge worker drops: the root is never
// contacted. Returns the number of reclaimed leases.
func (g *Gateway) ReapExpired() int {
	g.mu.Lock()
	n := g.reapExpiredLocked(time.Now())
	g.mu.Unlock()
	if n > 0 {
		atomic.AddInt64(&g.reaped, int64(n))
	}
	return n
}

func (g *Gateway) reapExpiredLocked(now time.Time) int {
	n := 0
	for id, l := range g.subs {
		if l.Expired(now) {
			delete(g.subs, id)
			n++
		}
	}
	return n
}

// Start launches the gateway's two background duties, each a single
// goroutine per region (not per worker):
//  1. keep the root lease alive (renew well before expiry, re-acquire
//     if lost);
//  2. reap timed-out sub-leases locally.
func (g *Gateway) Start() error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return nil
	}
	g.started = true
	g.mu.Unlock()

	if g.RootLease() == nil {
		if err := g.AcquireRootLease(); err != nil {
			return err
		}
	}

	tick := g.subTTL / 2
	if r := g.leaseTTL / 3; r < tick {
		tick = r
	}
	if tick <= 0 {
		tick = time.Millisecond
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-g.stopCh:
				return
			case now := <-t.C:
				g.keepRootLeaseAlive(now)
				g.ReapExpired()
			}
		}
	}()
	return nil
}

// Stop shuts the background loop down.
func (g *Gateway) Stop() {
	g.mu.Lock()
	if !g.started {
		g.mu.Unlock()
		return
	}
	g.started = false
	close(g.stopCh)
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *Gateway) keepRootLeaseAlive(now time.Time) {
	g.mu.Lock()
	rl := g.rootLease
	g.mu.Unlock()

	if rl == nil || rl.Expired(now) {
		// Lease lost: re-acquire. Root quota from the expired lease is
		// reclaimed lazily by the root itself.
		_ = g.AcquireRootLease()
		return
	}
	if rl.TTL(now) < g.leaseTTL/2 {
		nl, err := g.root.RenewRegionLease(rl.ID, g.leaseTTL)
		if err != nil {
			_ = g.AcquireRootLease()
			return
		}
		g.mu.Lock()
		g.rootLease = nl
		g.mu.Unlock()
	}
}
