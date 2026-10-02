package eventbus_test

import (
	"bytes"
	"log"
	"runtime"
	"sync"
	"testing"
	"time"

	"weakbus/eventbus"
)

// component simulates a UI component that subscribes to the global event
// stream. It is deliberately larger than the runtime's tiny-allocator
// threshold: objects under 16 bytes share allocator blocks, which can
// delay GC cleanups for block-mates that are still alive.
type component struct {
	id   int
	name string
}

// subscribeComponent creates a component, subscribes it, and drops every
// strong reference to it before returning. noinline guarantees the last
// pointer leaves the caller's stack frame when the function returns.
//
//go:noinline
func subscribeComponent(bus *eventbus.Bus, topic string, id int, h eventbus.Handler) {
	c := &component{id: id}
	eventbus.Subscribe(bus, topic, c, h)
}

// waitFor polls cond until it holds or timeout elapses, forcing a GC
// cycle on every iteration so cleanups get scheduled.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

// Acceptance test: component A subscribes to the global event stream, is
// destroyed with no external strong references, and after GC the bus
// automatically removes its listeners. The active subscriber count must
// decrease monotonically to zero.
func TestAutoCleanup_SubscriberCountMonotonicToZero(t *testing.T) {
	bus := eventbus.New()

	const components = 8
	for i := 0; i < components; i++ {
		subscribeComponent(bus, "global", i, func(eventbus.Event) {})
	}
	if got := bus.SubscriberCount("global"); got != components {
		t.Fatalf("after subscribe: got %d subscribers, want %d", got, components)
	}

	// Components are now unreachable. Sample the count while GC runs and
	// assert it never increases and eventually reaches zero.
	var samples []int
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		n := bus.SubscriberCount("global")
		if len(samples) == 0 || n != samples[len(samples)-1] {
			samples = append(samples, n)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber count did not reach zero; samples=%v", samples)
		}
	}

	for i := 1; i < len(samples); i++ {
		if samples[i] > samples[i-1] {
			t.Fatalf("subscriber count increased: samples=%v", samples)
		}
	}
	if samples[len(samples)-1] != 0 {
		t.Fatalf("final count = %d, want 0", samples[len(samples)-1])
	}
	t.Logf("subscriber count decayed monotonically to zero: %v", samples)

	if got := bus.TotalSubscribers(); got != 0 {
		t.Fatalf("total subscribers = %d, want 0", got)
	}
}

// After automatic cleanup, publishing must not invoke dead handlers.
func TestPublish_DeadSubscribersNotInvoked(t *testing.T) {
	bus := eventbus.New()

	var mu sync.Mutex
	received := 0
	for i := 0; i < 4; i++ {
		subscribeComponent(bus, "global", i, func(eventbus.Event) {
			mu.Lock()
			received++
			mu.Unlock()
		})
	}

	if n := bus.Publish("global", "before-gc"); n != 4 {
		t.Fatalf("delivered to %d handlers, want 4", n)
	}
	mu.Lock()
	if received != 4 {
		t.Fatalf("received = %d, want 4", received)
	}
	mu.Unlock()

	waitFor(t, 10*time.Second, func() bool {
		return bus.SubscriberCount("global") == 0
	}, "all subscribers to be collected")

	if n := bus.Publish("global", "after-gc"); n != 0 {
		t.Fatalf("delivered to %d handlers after GC, want 0", n)
	}
	mu.Lock()
	if received != 4 {
		t.Fatalf("received = %d after GC, want 4 (no zombie deliveries)", received)
	}
	mu.Unlock()
}

// Manual unsubscribe still works and disables the pending GC cleanup.
func TestUnsubscribe_Eager(t *testing.T) {
	bus := eventbus.New()
	c := &component{id: 1}
	sub := eventbus.Subscribe(bus, "global", c, func(eventbus.Event) {})
	sub.Unsubscribe()
	sub.Unsubscribe() // idempotent
	if got := bus.SubscriberCount("global"); got != 0 {
		t.Fatalf("after unsubscribe: got %d subscribers, want 0", got)
	}
	runtime.KeepAlive(c)
}

// The leak detector flags subscribers that outlive their expected
// lifetime and stays quiet once everything is collected.
func TestLeakDetector_WarnsOnZombies(t *testing.T) {
	bus := eventbus.New()
	var buf bytes.Buffer
	detector := eventbus.NewLeakDetector(bus, log.New(&buf, "", 0), 20*time.Millisecond)

	// Leaked component: subscribed but kept alive far beyond maxAge.
	leaked := &component{id: 99}
	eventbus.Subscribe(bus, "global", leaked, func(eventbus.Event) {})

	// Healthy components: destroyed right after subscribing.
	for i := 0; i < 3; i++ {
		subscribeComponent(bus, "global", i, func(eventbus.Event) {})
	}

	time.Sleep(50 * time.Millisecond) // exceed maxAge
	report := detector.Scan()
	runtime.KeepAlive(leaked) // keep the leaked component alive through the scan

	if report.Total() != 1 {
		t.Fatalf("active subscribers = %d, want 1 (only the leaked one)", report.Total())
	}
	if len(report.Zombies) != 1 {
		t.Fatalf("zombies = %d, want 1", len(report.Zombies))
	}
	if report.Zombies[0].Topic != "global" {
		t.Fatalf("zombie topic = %q, want %q", report.Zombies[0].Topic, "global")
	}
	if buf.Len() == 0 {
		t.Fatal("expected a leak warning to be logged")
	}
	t.Logf("detector warning: %s", bytes.TrimSpace(buf.Bytes()))

	// Release the leaked component; the next scan must be clean.
	leaked = nil
	report = detector.Scan()
	if report.Total() != 0 || len(report.Zombies) != 0 {
		t.Fatalf("after release: total=%d zombies=%d, want 0/0", report.Total(), len(report.Zombies))
	}
}

// A clean bus produces no warnings.
func TestLeakDetector_NoFalsePositives(t *testing.T) {
	bus := eventbus.New()
	var buf bytes.Buffer
	detector := eventbus.NewLeakDetector(bus, log.New(&buf, "", 0), time.Hour)

	for i := 0; i < 3; i++ {
		subscribeComponent(bus, "global", i, func(eventbus.Event) {})
	}
	report := detector.Scan()
	if report.Total() != 0 {
		t.Fatalf("active subscribers = %d, want 0", report.Total())
	}
	if len(report.Zombies) != 0 {
		t.Fatalf("zombies = %d, want 0", len(report.Zombies))
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning output: %q", buf.String())
	}
}
