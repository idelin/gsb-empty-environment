package leaseread

import (
	"context"
	"time"
)

// Cluster wires a leader LeaseReadOptimizer to a set of simulated followers
// and runs background lease renewal, mimicking a deployed Raft group.
type Cluster struct {
	Leader    *LeaseReadOptimizer
	Store     *KVStore
	Followers []*Follower

	stopRenew chan struct{}
	renewDone chan struct{}
}

// NewCluster builds a leader with the given followers and immediately grants
// it an initial lease (as if it just won election with quorum acks).
func NewCluster(clock Clock, followerCount int, followerLatency, leaseDuration, degradeThreshold time.Duration) *Cluster {
	store := NewKVStore()
	lease := NewLease(1)
	lease.Renew(clock.Now().Add(leaseDuration))

	followers := make([]*Follower, followerCount)
	ids := make([]string, followerCount)
	for i := 0; i < followerCount; i++ {
		id := "follower-" + string(rune('a'+i))
		followers[i] = NewFollower(id, followerLatency)
		ids[i] = id
	}
	transport := NewInMemoryTransport(followers...)

	leader := NewLeaseReadOptimizer("leader", clock, lease, store, transport, ids, leaseDuration, degradeThreshold)
	return &Cluster{
		Leader:    leader,
		Store:     store,
		Followers: followers,
		stopRenew: make(chan struct{}),
		renewDone: make(chan struct{}),
	}
}

// StartRenewal launches the background loop that renews the leader lease
// every leaseDuration/2, exactly like a healthy Raft leader's heartbeats.
func (c *Cluster) StartRenewal(leaseDuration time.Duration) {
	go func() {
		defer close(c.renewDone)
		ticker := time.NewTicker(leaseDuration / 2)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopRenew:
				return
			case <-ticker.C:
				// Renewal failures are ignored here: the lease simply
				// approaches expiry and reads degrade automatically.
				_ = c.Leader.RenewLease(context.Background())
			}
		}
	}()
}

// StopRenewal halts background lease renewal (simulating a leader whose
// heartbeats are delayed, or preparing to test the degrade path).
func (c *Cluster) StopRenewal() {
	select {
	case <-c.stopRenew:
	default:
		close(c.stopRenew)
	}
	<-c.renewDone
}
