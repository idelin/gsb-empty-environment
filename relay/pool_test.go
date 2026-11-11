package relay

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testQuarantine = 50 * time.Millisecond
	testLease      = 100 * time.Millisecond
)

func newTestPool(ports []int, quota int) (*Pool, *VirtualClock) {
	clock := NewVirtualClock(time.Unix(0, 0))
	pool := NewPool(Config{
		Ports:       ports,
		Quarantine:  testQuarantine,
		Lease:       testLease,
		TenantQuota: quota,
		Clock:       clock,
	})
	return pool, clock
}

func mustAllocate(t *testing.T, pool *Pool, tenant, remote string) *Mapping {
	t.Helper()
	m, err := pool.Allocate(tenant, remote)
	if err != nil {
		t.Fatalf("Allocate(%q, %q): %v", tenant, remote, err)
	}
	return m
}

// Late packets must be rejected once a mapping is closed, and must stay
// rejected after the port is recycled under a new generation.
func TestLatePacketsRejected(t *testing.T) {
	pool, clock := newTestPool([]int{40000, 40001}, 0)

	m1 := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5000")

	if !pool.HandleReply(m1.Port, m1.Remote, m1.Generation) {
		t.Fatal("live mapping should accept its reply")
	}
	if pool.HandleReply(m1.Port, "10.0.0.2:9999", m1.Generation) {
		t.Fatal("reply from wrong remote must be rejected")
	}

	if !pool.Close(m1.Port, m1.Generation) {
		t.Fatal("close of live mapping should succeed")
	}
	if pool.HandleReply(m1.Port, m1.Remote, m1.Generation) {
		t.Fatal("late packet during quarantine must be rejected")
	}

	clock.Advance(testQuarantine)
	m2 := mustAllocate(t, pool, "tenant-b", "10.0.0.3:6000")
	if m2.Port != m1.Port {
		t.Fatalf("expected port %d to be recycled, got %d", m1.Port, m2.Port)
	}
	if m2.Generation == m1.Generation {
		t.Fatal("recycled port must bump the generation")
	}
	if pool.HandleReply(m1.Port, m1.Remote, m1.Generation) {
		t.Fatal("late packet with stale generation must be rejected")
	}
	if !pool.HandleReply(m2.Port, m2.Remote, m2.Generation) {
		t.Fatal("new mapping should accept its reply")
	}
}

// A lease timer scheduled by a previous allocation of the same port must
// not clear the mapping of the current generation.
func TestStaleLeaseTimerCannotClearNewMapping(t *testing.T) {
	pool, clock := newTestPool([]int{40000}, 0)

	m1 := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5000")
	clock.Advance(10 * time.Millisecond)
	if !pool.Close(m1.Port, m1.Generation) {
		t.Fatal("close of live mapping should succeed")
	}

	clock.Advance(testQuarantine) // t=60ms: quarantine over, port free
	m2 := mustAllocate(t, pool, "tenant-b", "10.0.0.2:6000")
	if m2.Port != m1.Port {
		t.Fatalf("expected recycled port %d, got %d", m1.Port, m2.Port)
	}

	// t=100ms: m1's lease timer fires; it must be a no-op for m2.
	clock.Advance(testLease - 10*time.Millisecond - testQuarantine)
	if !pool.HandleReply(m2.Port, m2.Remote, m2.Generation) {
		t.Fatal("stale lease timer cleared the new mapping")
	}
	if got := pool.ActiveCount("tenant-b"); got != 1 {
		t.Fatalf("tenant-b active count = %d, want 1", got)
	}

	// t=160ms: m2's own lease expires and the mapping closes.
	clock.Advance(testLease)
	if pool.HandleReply(m2.Port, m2.Remote, m2.Generation) {
		t.Fatal("mapping should be closed after its lease expires")
	}
	if got := pool.ActiveCount("tenant-b"); got != 0 {
		t.Fatalf("tenant-b active count = %d, want 0", got)
	}
}

// With a tiny pool, allocation fails once every port is active or in
// quarantine, and recovers only after quarantine elapses.
func TestAllocationExhaustion(t *testing.T) {
	pool, clock := newTestPool([]int{40000, 40001}, 0)

	m1 := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5000")
	mustAllocate(t, pool, "tenant-a", "10.0.0.1:5001")

	if _, err := pool.Allocate("tenant-a", "10.0.0.1:5002"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("third allocate: got %v, want ErrPoolExhausted", err)
	}

	pool.Close(m1.Port, m1.Generation)
	if _, err := pool.Allocate("tenant-a", "10.0.0.1:5002"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("allocate during quarantine: got %v, want ErrPoolExhausted", err)
	}

	clock.Advance(testQuarantine)
	if _, err := pool.Allocate("tenant-a", "10.0.0.1:5002"); err != nil {
		t.Fatalf("allocate after quarantine: %v", err)
	}
}

// Concurrent closes of the same mapping must succeed exactly once and
// decrement the tenant's active count exactly once.
func TestConcurrentClose(t *testing.T) {
	pool, clock := newTestPool([]int{40000, 40001}, 0)

	m := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5000")

	const closers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < closers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pool.Close(m.Port, m.Generation) {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("concurrent closes succeeded %d times, want 1", succeeded)
	}
	if got := pool.ActiveCount("tenant-a"); got != 0 {
		t.Fatalf("tenant-a active count = %d, want 0", got)
	}
	if got := pool.FreePorts(); got != 1 {
		t.Fatalf("free ports = %d, want 1 (closed port is quarantined)", got)
	}

	clock.Advance(testQuarantine)
	if got := pool.FreePorts(); got != 2 {
		t.Fatalf("free ports after quarantine = %d, want 2", got)
	}
}

// Per-tenant quotas isolate tenants from each other: one tenant hitting its
// quota must not affect another tenant's ability to allocate.
func TestTenantQuotaIsolation(t *testing.T) {
	pool, clock := newTestPool([]int{40000, 40001}, 1)

	ma := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5000")

	if _, err := pool.Allocate("tenant-a", "10.0.0.1:5001"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("tenant-a second allocate: got %v, want ErrQuotaExceeded", err)
	}

	mb := mustAllocate(t, pool, "tenant-b", "10.0.0.2:6000")
	if mb.Port == ma.Port {
		t.Fatal("tenant-b must get a different port")
	}

	// tenant-c has quota room but the pool is drained by others.
	if _, err := pool.Allocate("tenant-c", "10.0.0.3:7000"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("tenant-c allocate: got %v, want ErrPoolExhausted", err)
	}

	// Once tenant-a's mapping closes and quarantine passes, tenant-a (and
	// only then the pool) has capacity again.
	pool.Close(ma.Port, ma.Generation)
	if _, err := pool.Allocate("tenant-a", "10.0.0.1:5002"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("tenant-a allocate during quarantine: got %v, want ErrPoolExhausted", err)
	}
	clock.Advance(testQuarantine)
	m2 := mustAllocate(t, pool, "tenant-a", "10.0.0.1:5002")
	if m2.Port != ma.Port {
		t.Fatalf("expected recycled port %d, got %d", ma.Port, m2.Port)
	}
	if got := pool.ActiveCount("tenant-a"); got != 1 {
		t.Fatalf("tenant-a active count = %d, want 1", got)
	}
	if got := pool.ActiveCount("tenant-b"); got != 1 {
		t.Fatalf("tenant-b active count = %d, want 1", got)
	}
}
