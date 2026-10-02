package eventbus

import (
	"context"
	"log"
	"runtime"
	"time"
)

// Zombie describes a subscription that is still active although its
// owner was expected to be destroyed long ago.
type Zombie struct {
	Topic string
	ID    uint64
	Age   time.Duration
}

// Report is the result of one leak-detector scan.
type Report struct {
	ScannedAt time.Time
	Counts    map[string]int
	Zombies   []Zombie
}

// Total returns the number of active subscribers across all topics.
func (r Report) Total() int {
	total := 0
	for _, n := range r.Counts {
		total += n
	}
	return total
}

// LeakDetector actively inspects a Bus for leftover (zombie) subscribers.
// A scan forces a GC cycle, waits for pending cleanups to run, and then
// flags any subscription older than maxAge: such a subscription's owner
// has outlived its expected lifetime, which indicates a leak.
type LeakDetector struct {
	bus    *Bus
	logger *log.Logger
	maxAge time.Duration

	// gcWait bounds how long a scan waits for GC cleanups to settle.
	gcWait time.Duration
}

// NewLeakDetector creates a detector for bus. Subscriptions older than
// maxAge at scan time are reported as zombies; warnings are written to
// logger (may be nil to silence output).
func NewLeakDetector(bus *Bus, logger *log.Logger, maxAge time.Duration) *LeakDetector {
	return &LeakDetector{bus: bus, logger: logger, maxAge: maxAge, gcWait: 2 * time.Second}
}

// Scan forces garbage collection, waits for automatic deregistration to
// settle, and returns a report of active counts and zombie subscribers.
// Suspected zombies are logged as warnings.
func (d *LeakDetector) Scan() Report {
	counts := d.waitForCleanups()
	report := Report{ScannedAt: time.Now(), Counts: counts}
	for topic := range counts {
		for _, info := range d.bus.Subscribers(topic) {
			if info.Age >= d.maxAge {
				report.Zombies = append(report.Zombies, Zombie{
					Topic: info.Topic,
					ID:    info.ID,
					Age:   info.Age,
				})
			}
		}
	}
	if len(report.Zombies) > 0 && d.logger != nil {
		for _, z := range report.Zombies {
			d.logger.Printf("WARNING: zombie subscriber detected: topic=%q id=%d age=%s (possible leaked component)",
				z.Topic, z.ID, z.Age.Round(time.Millisecond))
		}
	}
	return report
}

// waitForCleanups runs GC cycles until the subscriber counts stop
// changing across a fully drained cleanup cycle, or gcWait elapses.
func (d *LeakDetector) waitForCleanups() map[string]int {
	deadline := time.Now().Add(d.gcWait)
	prev := d.bus.Counts()
	stable := 0
	for {
		if !awaitCleanupCycle(deadline.Sub(time.Now())) {
			return d.bus.Counts()
		}
		cur := d.bus.Counts()
		if equalCounts(prev, cur) {
			stable++
		} else {
			stable = 0
		}
		// Cleanups are not necessarily drained in the GC cycle that
		// queues them, so require the counts to survive two consecutive
		// fully drained cycles unchanged.
		if stable >= 2 {
			return cur
		}
		prev = cur
		if time.Now().After(deadline) {
			return d.bus.Counts()
		}
	}
}

// awaitCleanupCycle forces a GC and blocks until the runtime's cleanup
// goroutine has drained past this cycle, proven by a sentinel object's
// cleanup having executed. It reports whether that happened before
// timeout.
func awaitCleanupCycle(timeout time.Duration) bool {
	done := make(chan struct{})
	armCleanupSentinel(done)
	runtime.GC()
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

//go:noinline
func armCleanupSentinel(done chan<- struct{}) {
	// The sentinel must not be tiny-allocated: objects smaller than 16
	// bytes share allocator blocks, and the runtime cannot queue a
	// cleanup for an object whose block is still partly alive.
	sentinel := new(cleanupSentinel)
	runtime.AddCleanup(sentinel, func(c chan<- struct{}) { close(c) }, done)
}

type cleanupSentinel struct{ _ [16]byte }

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Monitor scans the bus every interval until ctx is cancelled, logging
// warnings for any zombies it finds.
func (d *LeakDetector) Monitor(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Scan()
		}
	}
}
