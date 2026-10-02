// Package lease implements a hierarchical lease and multi-level proxy
// authorization coordinator for large-scale microservice clusters.
//
// Topology:
//
//	RootCoordinator  --(long, coarse leases, e.g. 1m)-->  RegionGateway
//	RegionGateway    --(short, fine sub-leases, e.g. 5s)--> WorkerNode
//
// The root only ever talks to a handful of region gateways, so its load
// stays flat no matter how many edge workers come and go. A worker that
// drops offline is reclaimed locally by its region gateway once the
// sub-lease times out; the root is never involved.
package lease

import "time"

// Lease is a time-bounded grant of quota to a holder.
type Lease struct {
	ID        string
	HolderID  string
	Quota     int64
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Expired reports whether the lease has expired at the given instant.
func (l *Lease) Expired(now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// TTL returns the remaining lifetime of the lease at the given instant.
func (l *Lease) TTL(now time.Time) time.Duration {
	d := l.ExpiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
