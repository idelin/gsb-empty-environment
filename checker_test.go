package negcache

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func snapOf(success bool, class ErrorClass, ver uint64, at time.Time, ttl, hard time.Duration) EntrySnapshot {
	return EntrySnapshot{
		Tenant: "t1", Key: "k", Success: success, Class: class,
		SourceVersion: ver, StoredAt: at,
		ExpiresAt: at.Add(ttl), HardExpireAt: at.Add(hard),
	}
}

func TestCheckerFlagsLateNotFoundOverSuccess(t *testing.T) {
	c := NewChecker(time.Minute, 4*time.Second)
	prev := snapOf(true, ClassNone, 5, t0, time.Minute, time.Minute)
	c.OnStore(nil, prev)
	c.OnStore(&prev, snapOf(false, ClassNotFound, 5, t0, 10*time.Second, time.Minute))
	v := c.Violations()
	if len(v) != 1 || !strings.Contains(v[0], "late not-found") {
		t.Fatalf("want late not-found violation, got %v", v)
	}

	c2 := NewChecker(time.Minute, 4*time.Second)
	c2.OnStore(nil, prev)
	newer := snapOf(false, ClassNotFound, 6, t0, 10*time.Second, time.Minute)
	c2.OnStore(&prev, newer) // newer source version: legitimate
	if v := c2.Violations(); len(v) != 0 {
		t.Fatalf("newer not-found must be allowed: %v", v)
	}
}

func TestCheckerFlagsBackoffRegression(t *testing.T) {
	c := NewChecker(time.Minute, 4*time.Second)
	prev := snapOf(false, ClassTemporary, 0, t0, 4*time.Second, time.Minute)
	c.OnStore(nil, prev)
	c.OnStore(&prev, snapOf(false, ClassTemporary, 0, t0, 2*time.Second, time.Minute))
	if v := c.Violations(); len(v) != 1 || !strings.Contains(v[0], "backoff") {
		t.Fatalf("want backoff violation, got %v", v)
	}

	c2 := NewChecker(time.Minute, 4*time.Second)
	c2.OnStore(nil, prev)
	c2.OnStore(&prev, snapOf(false, ClassTemporary, 0, t0, 8*time.Second, time.Minute))
	if v := c2.Violations(); len(v) != 1 || !strings.Contains(v[0], "exceeds cap") {
		t.Fatalf("want cap violation, got %v", v)
	}
}

func TestCheckerFlagsHardTTLBreach(t *testing.T) {
	c := NewChecker(time.Minute, 4*time.Second)
	bad := snapOf(true, ClassNone, 1, t0, 2*time.Minute, 2*time.Minute)
	c.OnStore(nil, bad)
	if v := c.Violations(); len(v) == 0 {
		t.Fatal("want hard TTL violation")
	}

	c2 := NewChecker(time.Minute, 4*time.Second)
	c2.OnStore(nil, snapOf(true, ClassNone, 1, t0, 30*time.Second, time.Minute))
	if v := c2.Violations(); len(v) != 0 {
		t.Fatalf("valid entry flagged: %v", v)
	}
}

func TestCheckerFlagsCancelLoggedAsSourceError(t *testing.T) {
	c := NewChecker(time.Minute, 4*time.Second)
	c.OnLog(LogEntry{Time: t0, Tenant: "t1", Key: "k", Class: ClassCanceled,
		Cancellation: true, SourceErr: true})
	if v := c.Violations(); len(v) != 1 || !strings.Contains(v[0], "misreported") {
		t.Fatalf("want misreport violation, got %v", v)
	}

	c2 := NewChecker(time.Minute, 4*time.Second)
	c2.OnLog(LogEntry{Time: t0, Class: ClassCanceled, Cancellation: true})
	c2.OnLog(LogEntry{Time: t0, Class: ClassTemporary, SourceErr: true})
	if v := c2.Violations(); len(v) != 0 {
		t.Fatalf("clean logs flagged: %v", v)
	}
}

// TestCheckerCleanRun wires the checker into a real cache and drives a mixed
// workload: not-found, temporary backoff, permanent, success overwrite,
// invalidation race and cancellation. No invariant may fire.
func TestCheckerCleanRun(t *testing.T) {
	clock := NewFakeClock(t0)
	checker := NewChecker(60*time.Second, 4*time.Second)
	var step atomic.Int32
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		switch step.Add(1) {
		case 1:
			return nil, 1, ErrNotFound
		case 2:
			return nil, 2, &TempError{Msg: "timeout"}
		case 3:
			return nil, 3, &TempError{Msg: "timeout"}
		default:
			return []byte("ok"), 4, nil
		}
	}, clock)
	cfg.OnStore = checker.OnStore
	cfg.Log = checker.Logger(nil)
	c := New(cfg)
	defer c.Close()

	c.Get(context.Background(), "t1", "k")         // not-found v1
	clock.Advance(11 * time.Second)                // expire not-found
	c.Get(context.Background(), "t1", "k")         // temp, backoff 1s
	clock.Advance(2 * time.Second)                 // expire temp
	c.Get(context.Background(), "t1", "k")         // temp, backoff 2s
	clock.Advance(3 * time.Second)                 // expire temp
	r, _ := c.Get(context.Background(), "t1", "k") // success v4
	if r.Negative || string(r.Value) != "ok" {
		t.Fatalf("%+v", r)
	}
	if v := checker.Violations(); len(v) != 0 {
		t.Fatalf("clean run produced violations: %v", v)
	}
}
