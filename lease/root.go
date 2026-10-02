package lease

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrQuotaExhausted is returned when the root (or a gateway) has no
	// quota left to hand out.
	ErrQuotaExhausted = errors.New("lease: quota exhausted")
	// ErrLeaseNotFound is returned when renewing or releasing an unknown
	// (or already expired and reaped) lease.
	ErrLeaseNotFound = errors.New("lease: lease not found")
	// ErrNoRootLease is returned by a gateway that currently holds no
	// valid root lease and therefore cannot mint sub-leases.
	ErrNoRootLease = errors.New("lease: gateway holds no valid root lease")
	// ErrInvalidArgument is returned for non-positive quota or TTL.
	ErrInvalidArgument = errors.New("lease: quota and ttl must be positive")
)

// RootCoordinator is the central authority. It only maintains lease
// traffic with region gateways (a handful of peers), never with the
// edge workers, so its CPU load stays essentially flat as the edge
// population grows.
//
// It deliberately runs NO background goroutine: expiry is enforced
// lazily on the rare occasions the root is touched.
type RootCoordinator struct {
	mu         sync.Mutex
	totalQuota int64
	leases     map[string]*Lease
	seq        int64

	requests int64 // atomic: total RPCs handled (load metric)
}

// NewRootCoordinator creates a root coordinator guarding totalQuota
// units of cluster-wide quota.
func NewRootCoordinator(totalQuota int64) *RootCoordinator {
	return &RootCoordinator{
		totalQuota: totalQuota,
		leases:     make(map[string]*Lease),
	}
}

// Requests reports how many RPCs the root has handled so far. This is
// the metric used to demonstrate that root load is independent of the
// number of edge workers.
func (r *RootCoordinator) Requests() int64 { return atomic.LoadInt64(&r.requests) }

// TotalQuota returns the cluster-wide quota the root guards.
func (r *RootCoordinator) TotalQuota() int64 { return r.totalQuota }

// UsedQuota returns the quota currently leased out to regions.
func (r *RootCoordinator) UsedQuota() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapExpiredLocked(time.Now())
	return r.usedLocked()
}

// ActiveLeases returns the number of live region leases.
func (r *RootCoordinator) ActiveLeases() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapExpiredLocked(time.Now())
	return len(r.leases)
}

// AcquireRegionLease grants quota to a region gateway for ttl.
func (r *RootCoordinator) AcquireRegionLease(regionID string, quota int64, ttl time.Duration) (*Lease, error) {
	atomic.AddInt64(&r.requests, 1)
	if quota <= 0 || ttl <= 0 {
		return nil, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.reapExpiredLocked(now)
	if r.usedLocked()+quota > r.totalQuota {
		return nil, ErrQuotaExhausted
	}
	r.seq++
	l := &Lease{
		ID:        fmt.Sprintf("root-lease-%d", r.seq),
		HolderID:  regionID,
		Quota:     quota,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	r.leases[l.ID] = l
	cp := *l
	return &cp, nil
}

// RenewRegionLease extends an existing region lease by ttl from now.
func (r *RootCoordinator) RenewRegionLease(leaseID string, ttl time.Duration) (*Lease, error) {
	atomic.AddInt64(&r.requests, 1)
	if ttl <= 0 {
		return nil, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.reapExpiredLocked(now)
	l, ok := r.leases[leaseID]
	if !ok {
		return nil, ErrLeaseNotFound
	}
	l.ExpiresAt = now.Add(ttl)
	cp := *l
	return &cp, nil
}

// ReleaseRegionLease returns a region lease's quota to the pool.
func (r *RootCoordinator) ReleaseRegionLease(leaseID string) {
	atomic.AddInt64(&r.requests, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.leases, leaseID)
}

// reapExpiredLocked removes expired leases. Called lazily, only when
// the root is already doing work, keeping idle CPU at zero.
func (r *RootCoordinator) reapExpiredLocked(now time.Time) {
	for id, l := range r.leases {
		if l.Expired(now) {
			delete(r.leases, id)
		}
	}
}

func (r *RootCoordinator) usedLocked() int64 {
	var used int64
	for _, l := range r.leases {
		used += l.Quota
	}
	return used
}
