package leaserouter

import (
	"fmt"
	"time"
)

// Lease grants one node the exclusive right to serve a KeyRange.
// Leases form a lineage through Split: children record their Parent and
// inherit a bumped fencing Epoch so stale parent leases always lose.
type Lease struct {
	ID       int64
	Parent   int64 // ID of the lease this one was split from (0 = root grant)
	NodeID   string
	Range    KeyRange
	Epoch    uint64 // fencing token, bumped on every split
	IssuedAt time.Time
	TTL      time.Duration
}

// Expired reports whether the lease is no longer valid at now.
func (l *Lease) Expired(now time.Time) bool {
	return !now.Before(l.IssuedAt.Add(l.TTL))
}

func (l *Lease) String() string {
	parent := "none"
	if l.Parent != 0 {
		parent = fmt.Sprintf("#%d", l.Parent)
	}
	return fmt.Sprintf("lease#%d node=%s range=%s epoch=%d parent=%s",
		l.ID, l.NodeID, l.Range, l.Epoch, parent)
}
