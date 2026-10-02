// Package timingwheel implements a hierarchical timing wheel scheduler
// driven by a discrete, manually advanced clock.
//
// Adding and cancelling timers are O(1) operations. The clock advances in
// discrete ticks via Advance; expired timers fire synchronously, exactly on
// their deadline tick. Timers scheduled far in the future are stored in
// higher-level wheels and cascade down automatically as time advances.
// Both one-shot timers and periodic tickers are supported.
package timingwheel

const (
	stateActive = iota
	stateFired
	stateCancelled
)

// Timer is a handle to a scheduled task.
type Timer struct {
	tw       *TimingWheel
	deadline int64 // absolute tick at which the task fires
	period   int64 // 0 for one-shot timers, >0 for tickers
	task     func()
	state    int
	bucket   *bucket
}

// Cancel cancels the timer in O(1). A cancelled timer never fires again and
// is unlinked from its bucket immediately. It returns false if the timer was
// already cancelled or has already fired (one-shot).
func (t *Timer) Cancel() bool {
	if t.state != stateActive {
		return false
	}
	t.state = stateCancelled
	if t.bucket != nil {
		delete(t.bucket.timers, t)
		t.bucket = nil
	}
	t.tw.pending--
	return true
}

// Cancelled reports whether the timer has been cancelled.
func (t *Timer) Cancelled() bool { return t.state == stateCancelled }

// Deadline returns the absolute tick at which the timer is next due.
func (t *Timer) Deadline() int64 { return t.deadline }

// bucket holds all timers sharing one slot of a wheel for the current round.
type bucket struct {
	expiration int64 // absolute tick of slot expiry; -1 when empty
	timers     map[*Timer]struct{}
}

func newBucket() *bucket {
	return &bucket{expiration: -1, timers: make(map[*Timer]struct{})}
}

// wheel is one level of the hierarchy. Each level's tick equals the full
// interval of the level below it.
type wheel struct {
	tick        int64
	size        int64
	interval    int64 // tick * size
	currentTime int64 // aligned down to a multiple of tick
	buckets     []*bucket
	overflow    *wheel
}

func newWheel(tick, size, startTime int64) *wheel {
	w := &wheel{
		tick:        tick,
		size:        size,
		interval:    tick * size,
		currentTime: startTime - startTime%tick,
	}
	w.buckets = make([]*bucket, size)
	for i := range w.buckets {
		w.buckets[i] = newBucket()
	}
	return w
}

// add inserts t into the appropriate level in O(1) per level. It returns
// false if the timer is already due (deadline <= currentTime).
func (w *wheel) add(t *Timer) bool {
	if t.deadline <= w.currentTime {
		return false
	}
	if t.deadline < w.currentTime+w.interval {
		virtualID := t.deadline / w.tick
		b := w.buckets[virtualID%w.size]
		b.timers[t] = struct{}{}
		t.bucket = b
		b.expiration = virtualID * w.tick
		return true
	}
	if w.overflow == nil {
		w.overflow = newWheel(w.interval, w.size, w.currentTime)
	}
	return w.overflow.add(t)
}

// advanceClock moves the wheel's clock forward to now and collects the
// timers held by the bucket expiring at the new currentTime, cascading into
// overflow wheels. Collected timers are re-added (cascaded down) or fired
// by the caller.
func (w *wheel) advanceClock(now int64, out *[]*Timer) {
	if now < w.currentTime+w.tick {
		return
	}
	w.currentTime = now - now%w.tick
	slot := (w.currentTime / w.tick) % w.size
	b := w.buckets[slot]
	if b.expiration >= 0 && b.expiration <= w.currentTime {
		for t := range b.timers {
			*out = append(*out, t)
			delete(b.timers, t)
			t.bucket = nil
		}
		b.expiration = -1
	}
	if w.overflow != nil {
		w.overflow.advanceClock(now, out)
	}
}

// countTimers returns the number of timers stored in this wheel and all
// overflow wheels. Used by tests to verify memory is fully released.
func (w *wheel) countTimers() int {
	n := 0
	for _, b := range w.buckets {
		n += len(b.timers)
	}
	if w.overflow != nil {
		n += w.overflow.countTimers()
	}
	return n
}

// TimingWheel is a hierarchical timing wheel scheduler driven by a discrete
// clock. It is not safe for concurrent use; the clock is advanced manually
// via Advance, which fires due timers synchronously.
type TimingWheel struct {
	now     int64
	wheel   *wheel
	pending int
}

// New creates a TimingWheel whose base wheel has the given number of slots
// (one tick per slot). Higher levels are created lazily as needed.
func New(wheelSize int64) *TimingWheel {
	if wheelSize <= 0 {
		panic("timingwheel: wheel size must be positive")
	}
	return &TimingWheel{wheel: newWheel(1, wheelSize, 0)}
}

// Now returns the current tick of the discrete clock.
func (tw *TimingWheel) Now() int64 { return tw.now }

// Pending returns the number of live (scheduled, not yet fired or
// cancelled) timers.
func (tw *TimingWheel) Pending() int { return tw.pending }

// After schedules task to run once, delay ticks from now. A delay <= 0
// runs the task immediately and returns an already-fired timer.
func (tw *TimingWheel) After(delay int64, task func()) *Timer {
	if delay <= 0 {
		t := &Timer{tw: tw, deadline: tw.now, task: task, state: stateFired}
		task()
		return t
	}
	return tw.schedule(delay, 0, task)
}

// Ticker schedules task to run every period ticks, first firing at
// now+period, until the returned timer is cancelled.
func (tw *TimingWheel) Ticker(period int64, task func()) *Timer {
	if period <= 0 {
		panic("timingwheel: ticker period must be positive")
	}
	return tw.schedule(period, period, task)
}

func (tw *TimingWheel) schedule(delay, period int64, task func()) *Timer {
	t := &Timer{
		tw:       tw,
		deadline: tw.now + delay,
		period:   period,
		task:     task,
		state:    stateActive,
	}
	tw.pending++
	tw.wheel.add(t) // delay > 0, so it always fits
	return t
}

// Advance moves the discrete clock forward by the given number of ticks,
// firing every timer whose deadline is reached, exactly on its deadline
// tick. Tasks run synchronously in deadline order.
func (tw *TimingWheel) Advance(ticks int64) {
	for i := int64(0); i < ticks; i++ {
		tw.now++
		var due []*Timer
		tw.wheel.advanceClock(tw.now, &due)
		for _, t := range due {
			if t.state != stateActive {
				continue
			}
			if tw.wheel.add(t) {
				continue // cascaded to a lower wheel; not due yet
			}
			tw.fire(t)
		}
	}
}

func (tw *TimingWheel) fire(t *Timer) {
	t.task()
	if t.period > 0 && t.state == stateActive {
		t.deadline += t.period
		tw.wheel.add(t) // deadline > now, always fits
		return
	}
	if t.state == stateActive {
		t.state = stateFired
		tw.pending--
	}
}
