package leaseread

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultDegradeThreshold is the acceptance-criteria boundary: when the
// remaining lease drops below 100ms the leader stops trusting the local
// lease and falls back to a full quorum heartbeat confirmation per read.
const DefaultDegradeThreshold = 100 * time.Millisecond

// Metrics exposes the counters that prove the optimization works.
type Metrics struct {
	localReads    int64 // reads served locally with zero network traffic
	degradedReads int64 // reads that required quorum heartbeat confirmation
	networkRounds int64 // network round trips triggered by reads
	renewals      int64 // successful background lease renewals
}

// Snapshot returns a consistent copy of the metrics.
func (m *Metrics) Snapshot() (local, degraded, network, renewals int64) {
	return atomic.LoadInt64(&m.localReads),
		atomic.LoadInt64(&m.degradedReads),
		atomic.LoadInt64(&m.networkRounds),
		atomic.LoadInt64(&m.renewals)
}

// LeaseReadOptimizer serves strongly consistent (linearizable) reads on the
// leader. Inside the lease's absolute safe window it reads the local state
// machine directly with zero network overhead; near lease expiry it degrades
// to a quorum heartbeat confirmation (read-index style) before reading.
type LeaseReadOptimizer struct {
	nodeID           string
	clock            Clock
	lease            *Lease
	store            *KVStore
	transport        *CountingTransport
	followers        []string
	degradeThreshold time.Duration
	leaseDuration    time.Duration

	Metrics Metrics
}

func NewLeaseReadOptimizer(
	nodeID string,
	clock Clock,
	lease *Lease,
	store *KVStore,
	transport Transport,
	followers []string,
	leaseDuration time.Duration,
	degradeThreshold time.Duration,
) *LeaseReadOptimizer {
	return &LeaseReadOptimizer{
		nodeID:           nodeID,
		clock:            clock,
		lease:            lease,
		store:            store,
		transport:        NewCountingTransport(transport),
		followers:        followers,
		leaseDuration:    leaseDuration,
		degradeThreshold: degradeThreshold,
	}
}

// Read returns the newest committed value for key with strict
// linearizability. Fast path: if the remaining lease is at or above the
// degrade threshold, the read is served locally with zero network round
// trips. Slow path: confirm a quorum of followers via heartbeat, renew the
// lease, then read locally.
func (o *LeaseReadOptimizer) Read(ctx context.Context, key string) ([]byte, error) {
	now := o.clock.Now()
	if o.lease.Remaining(now) >= o.degradeThreshold {
		// Absolute safe window: no other leader can have committed a newer
		// write, so the local applied state is the newest committed state.
		atomic.AddInt64(&o.Metrics.localReads, 1)
		return o.store.Get(key)
	}

	// Lease is about to expire: degrade to full quorum heartbeat
	// confirmation for this read.
	atomic.AddInt64(&o.Metrics.degradedReads, 1)
	rounds, err := o.confirmQuorum(ctx)
	atomic.AddInt64(&o.Metrics.networkRounds, rounds)
	if err != nil {
		return nil, err
	}
	return o.store.Get(key)
}

// confirmQuorum heartbeats every follower in parallel and requires a
// majority (leader included) to ack. On success the lease is renewed for a
// fresh leaseDuration measured from the local clock. It returns the number
// of network round trips used.
func (o *LeaseReadOptimizer) confirmQuorum(ctx context.Context) (int64, error) {
	term := o.lease.Term()
	commitIndex := o.store.AppliedIndex()
	quorum := len(o.followers)/2 + 1 // followers needed incl. leader vote

	before := o.transport.Rounds()
	var wg sync.WaitGroup
	acks := int32(1) // leader votes for itself
	for _, id := range o.followers {
		wg.Add(1)
		go func(followerID string) {
			defer wg.Done()
			if err := o.transport.Heartbeat(ctx, followerID, term, commitIndex); err == nil {
				atomic.AddInt32(&acks, 1)
			}
		}(id)
	}
	wg.Wait()
	rounds := o.transport.Rounds() - before

	if int(atomic.LoadInt32(&acks)) < quorum {
		return rounds, ErrNoQuorum
	}
	o.lease.Renew(o.clock.Now().Add(o.leaseDuration))
	return rounds, nil
}

// RenewLease is the background lease maintenance the leader performs while
// it is healthy: one quorum heartbeat round per leaseDuration/2.
func (o *LeaseReadOptimizer) RenewLease(ctx context.Context) error {
	if _, err := o.confirmQuorum(ctx); err != nil {
		return err
	}
	atomic.AddInt64(&o.Metrics.renewals, 1)
	return nil
}

// NetworkRounds returns the total network round trips issued by this node.
func (o *LeaseReadOptimizer) NetworkRounds() int64 {
	return o.transport.Rounds()
}

// LeaseRemaining exposes the current lease remainder (for demos/tests).
func (o *LeaseReadOptimizer) LeaseRemaining() time.Duration {
	return o.lease.Remaining(o.clock.Now())
}
