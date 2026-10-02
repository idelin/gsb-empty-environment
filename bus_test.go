package eventbus

import (
	"bytes"
	"log"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// subscribeAndForget simulates a component subscribing to the global event
// stream and then being destroyed: the *Subscription handle is dropped with
// no external strong reference once this function returns.
func subscribeAndForget(b *Bus, topic string, counter *int64) {
	sub := b.Subscribe(topic, func(payload interface{}) {
		atomic.AddInt64(counter, 1)
	})
	_ = sub // component destroyed: handle goes out of scope here
}

// forceGC runs full GC cycles so pending finalizers get a chance to run.
func forceGC() {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

// Acceptance test: component A subscribes, is destroyed, and after GC the
// bus automatically cleans up. The active subscriber count must decrease
// monotonically all the way to zero.
func TestAutoCleanupAfterGC_MonotonicToZero(t *testing.T) {
	b := New()
	var counter int64

	subscribeAndForget(b, "global", &counter)
	if got := b.ActiveCount(); got != 1 {
		t.Fatalf("before GC: active = %d, want 1", got)
	}

	// Deliver one event while the subscriber is (maybe) still alive.
	if n := b.Publish("global", "hello"); n != 1 {
		t.Fatalf("delivered to %d listeners, want 1", n)
	}

	samples := []int{b.ActiveCount()}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		forceGC()
		b.Sweep()
		active := b.ActiveCount()
		samples = append(samples, active)
		if active == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Monotonic non-increasing check.
	for i := 1; i < len(samples); i++ {
		if samples[i] > samples[i-1] {
			t.Fatalf("active count not monotonic: %v", samples)
		}
	}
	if got := b.ActiveCount(); got != 0 {
		t.Fatalf("after GC: active = %d, want 0 (samples: %v)", got, samples)
	}
	if stats := b.Stats(); stats.Cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1", stats.Cleaned)
	}
	t.Logf("active count samples (monotonic to zero): %v", samples)
}

// The watchdog must clean up dead subscribers on its own, without an
// explicit Sweep call.
func TestWatchdogAutoSweep(t *testing.T) {
	b := New()
	stop := b.StartWatchdog(20 * time.Millisecond)
	defer stop()

	var counter int64
	subscribeAndForget(b, "global", &counter)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		forceGC()
		if b.ActiveCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("watchdog did not clean up: active = %d, want 0", b.ActiveCount())
}

// A subscriber that is finalized but never swept within the zombie timeout
// must trigger a warning and be force-removed.
func TestZombieDetectionWarns(t *testing.T) {
	var buf bytes.Buffer
	b := New(
		WithZombieTimeout(time.Millisecond),
		WithLogger(log.New(&buf, "[eventbus] ", 0)),
	)

	var counter int64
	subscribeAndForget(b, "global", &counter)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		forceGC()
		if b.Stats().Pending > 0 {
			break
		}
	}
	if b.Stats().Pending == 0 {
		t.Fatal("finalizer never marked the subscription as pending")
	}

	time.Sleep(5 * time.Millisecond) // let it age past the zombie timeout
	swept, zombies := b.Sweep()
	if swept != 1 || zombies != 1 {
		t.Fatalf("swept=%d zombies=%d, want 1/1", swept, zombies)
	}
	if !bytes.Contains(buf.Bytes(), []byte("zombie subscription detected")) {
		t.Fatalf("expected zombie warning, got log: %q", buf.String())
	}
	if got := b.ActiveCount(); got != 0 {
		t.Fatalf("active = %d, want 0", got)
	}
}

// Explicit Unsubscribe removes the listener deterministically and disarms
// the finalizer (no double cleanup, no zombie noise).
func TestExplicitUnsubscribe(t *testing.T) {
	b := New()
	sub := b.Subscribe("global", func(interface{}) {})
	sub.Unsubscribe()
	sub.Unsubscribe() // idempotent
	if got := b.ActiveCount(); got != 0 {
		t.Fatalf("active = %d, want 0", got)
	}
	forceGC()
	b.Sweep()
	if stats := b.Stats(); stats.Pending != 0 || stats.Cleaned != 0 {
		t.Fatalf("unexpected finalizer cleanup after explicit unsubscribe: %+v", stats)
	}
	runtime.KeepAlive(sub)
}

// Live subscribers keep receiving events; only dead ones are reaped.
func TestLiveSubscribersUnaffected(t *testing.T) {
	b := New()
	var hits int64
	live := b.Subscribe("global", func(interface{}) { atomic.AddInt64(&hits, 1) })

	var dead int64
	subscribeAndForget(b, "global", &dead)

	forceGC()
	b.Sweep()

	if n := b.Publish("global", "ping"); n != 1 {
		t.Fatalf("delivered to %d listeners, want 1 (only the live one)", n)
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("live handler hits = %d, want 1", hits)
	}
	runtime.KeepAlive(live)
}
