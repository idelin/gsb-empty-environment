// Package leasecoord implements a hierarchical lease and multi-level proxy
// authorization coordinator for large-scale microservice clusters.
//
// A root coordinator issues long-lived (1 minute) coarse-grained leases to a
// handful of region gateways. Each gateway fans out short-lived (5 second)
// fine-grained sub-leases to the edge workers in its region. Edge workers
// never talk to the root, so central coordination pressure stays nearly
// constant regardless of cluster size.
package leasecoord

import (
	"fmt"
	"sync"
	"time"
)

// Lease is a time-bounded quota grant.
type Lease struct {
	ID        string
	HolderID  string
	Quota     int64
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Expired reports whether the lease is no longer valid at the given time.
func (l Lease) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

// RootCoordinator issues long-lived coarse-grained leases to region
// gateways. It never communicates with edge workers directly.
type RootCoordinator struct {
	mu         sync.Mutex
	clock      Clock
	totalQuota int64
	ttl        time.Duration
	leases     map[string]*Lease // keyed by gateway ID
	msgCount   int64             // lease RPCs served; root load metric
	seq        int64
}

// NewRootCoordinator creates a root coordinator with the given total quota
// and coarse lease TTL (1 minute in production). A nil clock uses wall time.
func NewRootCoordinator(totalQuota int64, ttl time.Duration, clock Clock) *RootCoordinator {
	if clock == nil {
		clock = realClock{}
	}
	return &RootCoordinator{
		clock:      clock,
		totalQuota: totalQuota,
		ttl:        ttl,
		leases:     make(map[string]*Lease),
	}
}

// RequestLease serves a gateway's acquire/renew request for coarse quota.
// This is the only entry point that adds load to the root coordinator.
func (r *RootCoordinator) RequestLease(gatewayID string, quota int64) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgCount++
	r.sweepLocked()

	now := r.clock.Now()
	if l, ok := r.leases[gatewayID]; ok {
		if delta := quota - l.Quota; delta > 0 && r.availableLocked() < delta {
			return Lease{}, fmt.Errorf("root: insufficient quota for gateway %q", gatewayID)
		}
		l.Quota = quota
		l.ExpiresAt = now.Add(r.ttl)
		return *l, nil
	}
	if r.availableLocked() < quota {
		return Lease{}, fmt.Errorf("root: insufficient quota for gateway %q", gatewayID)
	}
	r.seq++
	l := &Lease{
		ID:        fmt.Sprintf("root-lease-%d", r.seq),
		HolderID:  gatewayID,
		Quota:     quota,
		IssuedAt:  now,
		ExpiresAt: now.Add(r.ttl),
	}
	r.leases[gatewayID] = l
	return *l, nil
}

// Release lets a gateway return its coarse lease early.
func (r *RootCoordinator) Release(gatewayID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.leases, gatewayID)
}

// MessageCount reports how many lease RPCs the root has served. It is the
// proxy metric for root coordinator CPU load.
func (r *RootCoordinator) MessageCount() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.msgCount
}

// OutstandingQuota returns the quota currently leased out to gateways.
func (r *RootCoordinator) OutstandingQuota() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	var sum int64
	for _, l := range r.leases {
		sum += l.Quota
	}
	return sum
}

func (r *RootCoordinator) availableLocked() int64 {
	var used int64
	for _, l := range r.leases {
		used += l.Quota
	}
	return r.totalQuota - used
}

func (r *RootCoordinator) sweepLocked() {
	now := r.clock.Now()
	for id, l := range r.leases {
		if l.Expired(now) {
			delete(r.leases, id)
		}
	}
}
