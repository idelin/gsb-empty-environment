package leasecoord

import (
	"fmt"
	"sync"
	"time"
)

// RegionGateway holds one coarse lease from the root coordinator and fans
// out short-lived fine-grained sub-leases to the edge workers in its
// region. It is the proxy authorization layer that shields the root from
// per-worker churn.
type RegionGateway struct {
	mu        sync.Mutex
	id        string
	root      *RootCoordinator
	clock     Clock
	subTTL    time.Duration
	chunkSize int64 // coarse quota fetched from the root per top-up
	coarse    Lease
	hasCoarse bool
	subs      map[string]*Lease // keyed by worker ID
	seq       int64
}

// NewRegionGateway creates a gateway attached to the given root. subTTL is
// the sub-lease lifetime (5 seconds in production); chunkSize is the coarse
// quota block requested from the root on each top-up. A nil clock uses wall
// time.
func NewRegionGateway(id string, root *RootCoordinator, subTTL time.Duration, chunkSize int64, clock Clock) *RegionGateway {
	if clock == nil {
		clock = realClock{}
	}
	return &RegionGateway{
		id:        id,
		root:      root,
		clock:     clock,
		subTTL:    subTTL,
		chunkSize: chunkSize,
		subs:      make(map[string]*Lease),
	}
}

// RequestSubLease serves a worker's acquire/renew request. The request is
// handled purely locally unless the coarse lease needs a top-up from the
// root.
func (g *RegionGateway) RequestSubLease(workerID string, quota int64) (Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked()

	if l, ok := g.subs[workerID]; ok {
		if delta := quota - l.Quota; delta > 0 {
			if err := g.ensureCoarseLocked(delta); err != nil {
				return Lease{}, err
			}
		}
		l.Quota = quota
		l.ExpiresAt = g.clock.Now().Add(g.subTTL)
		return *l, nil
	}
	if err := g.ensureCoarseLocked(quota); err != nil {
		return Lease{}, err
	}
	g.seq++
	now := g.clock.Now()
	l := &Lease{
		ID:        fmt.Sprintf("%s-sub-%d", g.id, g.seq),
		HolderID:  workerID,
		Quota:     quota,
		IssuedAt:  now,
		ExpiresAt: now.Add(g.subTTL),
	}
	g.subs[workerID] = l
	return *l, nil
}

// ReleaseSubLease lets a worker return its sub-lease early.
func (g *RegionGateway) ReleaseSubLease(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.subs, workerID)
}

// Sweep expires dead workers' sub-leases and reclaims their quota locally.
// The root coordinator is never involved.
func (g *RegionGateway) Sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked()
}

// OutstandingQuota returns the quota currently leased to local workers.
func (g *RegionGateway) OutstandingQuota() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked()
	return g.outstandingLocked()
}

// SubLeaseCount returns the number of live sub-leases in this region.
func (g *RegionGateway) SubLeaseCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked()
	return len(g.subs)
}

func (g *RegionGateway) outstandingLocked() int64 {
	var sum int64
	for _, l := range g.subs {
		sum += l.Quota
	}
	return sum
}

func (g *RegionGateway) coarseValidLocked() bool {
	return g.hasCoarse && !g.coarse.Expired(g.clock.Now())
}

// ensureCoarseLocked guarantees at least need free quota under the coarse
// lease, topping up from the root in chunkSize blocks when necessary.
func (g *RegionGateway) ensureCoarseLocked(need int64) error {
	if g.coarseValidLocked() && g.coarse.Quota-g.outstandingLocked() >= need {
		return nil
	}
	want := g.outstandingLocked() + need
	if rem := want % g.chunkSize; rem != 0 {
		want += g.chunkSize - rem
	}
	l, err := g.root.RequestLease(g.id, want)
	if err != nil {
		return err
	}
	g.coarse = l
	g.hasCoarse = true
	return nil
}

func (g *RegionGateway) sweepLocked() {
	now := g.clock.Now()
	for id, l := range g.subs {
		if l.Expired(now) {
			delete(g.subs, id)
		}
	}
}
