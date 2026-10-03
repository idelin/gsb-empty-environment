package leaseread

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

const concurrentReaders = 10000

// newTestOptimizer builds a leader with a manual clock, no background
// renewal, and zero-latency followers, so tests are fully deterministic.
func newTestOptimizer(leaseRemaining time.Duration) (*LeaseReadOptimizer, *ManualClock, *KVStore, []*Follower) {
	clock := NewManualClock(time.Now())
	store := NewKVStore()
	lease := NewLease(1)
	lease.Renew(clock.Now().Add(leaseRemaining))

	followers := []*Follower{NewFollower("f1", 0), NewFollower("f2", 0)}
	transport := NewInMemoryTransport(followers...)
	opt := NewLeaseReadOptimizer(
		"leader", clock, lease, store, transport,
		[]string{"f1", "f2"},
		1*time.Second, DefaultDegradeThreshold,
	)
	return opt, clock, store, followers
}

// Acceptance criterion 1+2: 10k concurrent reads inside the lease's absolute
// safe window are served locally with zero network round trips.
func TestConcurrentLocalReadsZeroNetwork(t *testing.T) {
	opt, _, store, _ := newTestOptimizer(10 * time.Second)
	store.Put(1, "hot-key", []byte("v1"))

	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, concurrentReaders)
	for i := 0; i < concurrentReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := opt.Read(context.Background(), "hot-key")
			if err != nil {
				errs <- err
				return
			}
			if string(v) != "v1" {
				errs <- fmt.Errorf("got %q, want v1", v)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	local, degraded, network, _ := opt.Metrics.Snapshot()
	if local != concurrentReaders {
		t.Fatalf("local reads = %d, want %d", local, concurrentReaders)
	}
	if degraded != 0 {
		t.Fatalf("degraded reads = %d, want 0", degraded)
	}
	if rounds := opt.NetworkRounds(); rounds != 0 {
		t.Fatalf("network rounds = %d, want 0 (zero network overhead)", rounds)
	}
	if network != 0 {
		t.Fatalf("read-triggered network rounds = %d, want 0", network)
	}
	t.Logf("10000 concurrent lease reads in %v (%.0f reads/sec), 0 network rounds",
		elapsed, float64(concurrentReaders)/elapsed.Seconds())
}

// Acceptance criterion 3: once the remaining lease drops below 100ms, reads
// automatically degrade to full quorum heartbeat confirmation.
func TestDegradeBelowThreshold(t *testing.T) {
	opt, clock, store, _ := newTestOptimizer(150 * time.Millisecond)
	store.Put(1, "k", []byte("v"))

	// Inside the safe window: local read, no network.
	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if rounds := opt.NetworkRounds(); rounds != 0 {
		t.Fatalf("rounds inside safe window = %d, want 0", rounds)
	}

	// Advance so only 60ms of lease remains (< 100ms threshold).
	clock.Advance(90 * time.Millisecond)
	if rem := opt.LeaseRemaining(); rem >= DefaultDegradeThreshold {
		t.Fatalf("remaining = %v, want < 100ms", rem)
	}

	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	local, degraded, network, _ := opt.Metrics.Snapshot()
	if local != 1 || degraded != 1 {
		t.Fatalf("local=%d degraded=%d, want 1/1", local, degraded)
	}
	if network == 0 || opt.NetworkRounds() == 0 {
		t.Fatal("degraded read must perform quorum heartbeat network rounds")
	}

	// The degraded read also renewed the lease: next read is local again.
	before := opt.NetworkRounds()
	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if got := opt.NetworkRounds() - before; got != 0 {
		t.Fatalf("rounds after renewal = %d, want 0", got)
	}
}

// Boundary check: exactly 100ms remaining is still the fast path; 99ms is not.
func TestDegradeThresholdBoundary(t *testing.T) {
	opt, clock, store, _ := newTestOptimizer(DefaultDegradeThreshold)
	store.Put(1, "k", []byte("v"))

	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if local, degraded, _, _ := opt.Metrics.Snapshot(); local != 1 || degraded != 0 {
		t.Fatalf("at exactly 100ms remaining: local=%d degraded=%d, want 1/0", local, degraded)
	}

	clock.Advance(1 * time.Millisecond) // now 99ms remain
	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if local, degraded, _, _ := opt.Metrics.Snapshot(); local != 1 || degraded != 1 {
		t.Fatalf("at 99ms remaining: local=%d degraded=%d, want 1/1", local, degraded)
	}
}

// Linearizability guard: without a quorum the leader must refuse to serve
// the read rather than risk returning stale data.
func TestNoQuorumRefusesRead(t *testing.T) {
	opt, clock, store, followers := newTestOptimizer(50 * time.Millisecond)
	store.Put(1, "k", []byte("v"))
	_ = clock

	// Partition both followers: no quorum possible.
	followers[0].SetDown(true)
	followers[1].SetDown(true)

	if _, err := opt.Read(context.Background(), "k"); err != ErrNoQuorum {
		t.Fatalf("err = %v, want ErrNoQuorum", err)
	}

	// One follower recovers: quorum (leader + 1 of 2) is back.
	followers[0].SetDown(false)
	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatalf("err = %v, want nil after quorum restored", err)
	}
}

// Expired lease (remaining <= 0) must never use the local fast path.
func TestExpiredLeaseNeverServesLocally(t *testing.T) {
	opt, clock, store, _ := newTestOptimizer(10 * time.Millisecond)
	store.Put(1, "k", []byte("v"))
	clock.Advance(20 * time.Millisecond) // lease fully expired

	if _, err := opt.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if local, degraded, _, _ := opt.Metrics.Snapshot(); local != 0 || degraded != 1 {
		t.Fatalf("local=%d degraded=%d, want 0/1", local, degraded)
	}
}

// Read-your-writes under concurrency: every read observes the newest
// committed write, never an older value.
func TestLinearizableReadYourWrites(t *testing.T) {
	opt, _, store, _ := newTestOptimizer(10 * time.Second)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n)
			for i := 1; i <= 100; i++ {
				store.Put(uint64(i), key, []byte(fmt.Sprintf("v%d", i)))
				v, err := opt.Read(context.Background(), key)
				if err != nil {
					t.Error(err)
					return
				}
				if string(v) != fmt.Sprintf("v%d", i) {
					t.Errorf("key %s: got %q after writing v%d", key, v, i)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// Background renewal keeps the lease perpetually inside the safe window, so
// a sustained read storm never touches the network.
func TestClusterWithRenewalSustainsLocalReads(t *testing.T) {
	cluster := NewCluster(RealClock{}, 2, 0, 1*time.Second, DefaultDegradeThreshold)
	cluster.Store.Put(1, "k", []byte("v"))
	cluster.StartRenewal(1 * time.Second)
	defer cluster.StopRenewal()

	var wg sync.WaitGroup
	for i := 0; i < concurrentReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cluster.Leader.Read(context.Background(), "k"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	local, degraded, readNetwork, _ := cluster.Leader.Metrics.Snapshot()
	if local != concurrentReaders || degraded != 0 || readNetwork != 0 {
		t.Fatalf("local=%d degraded=%d readNetwork=%d, want %d/0/0",
			local, degraded, readNetwork, concurrentReaders)
	}
}

func BenchmarkLeaseRead(b *testing.B) {
	opt, _, store, _ := newTestOptimizer(1 * time.Hour)
	store.Put(1, "k", []byte("v"))
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := opt.Read(ctx, "k"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
