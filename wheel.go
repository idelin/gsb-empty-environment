// Package timingwheel implements a hierarchical timing wheel: a discrete
// clock driven scheduler with O(1) timer insertion and cancellation.
//
// The scheduler does not own a goroutine and never touches wall-clock
// time. Time advances only when the caller drives the discrete clock via
// Advance / AdvanceTo, which makes scheduling fully deterministic: every
// timer fires exactly at its deadline tick.
package timingwheel

import (
	"container/list"
	"sync"
	"sync/atomic"
)

const (
	// WheelSize is the number of buckets on each wheel level.
	WheelSize = 64
	// NumLevels is the number of wheel levels. Level i advances one bucket
	// every WheelSize^i base ticks, so deadlines up to roughly
	// WheelSize^NumLevels (~6.9e10) ticks are covered before wrapping.
	NumLevels = 6
)

// Task is executed when a timer fires. now is the timer's deadline tick,
// always exactly aligned to the discrete clock.
type Task func(now int64)

// Timer is a handle to a scheduled task. A zero-period Timer fires once;
// a positive period makes it a ticker that reschedules itself.
type Timer struct {
	deadline  int64
	period    int64
	task      Task
	cancelled int32 // atomic

	tw     *TimingWheel
	bucket *list.List
	elem   *list.Element
}

// Cancel prevents the timer from firing (again) and releases its slot.
// It reports whether this call was the one that cancelled the timer.
func (t *Timer) Cancel() bool {
	if !atomic.CompareAndSwapInt32(&t.cancelled, 0, 1) {
		return false
	}
	tw := t.tw
	tw.mu.Lock()
	if t.elem != nil {
		t.bucket.Remove(t.elem) // O(1)
		t.bucket, t.elem = nil, nil
		tw.active--
	}
	t.task = nil
	tw.mu.Unlock()
	return true
}

// Cancelled reports whether Cancel has been called.
func (t *Timer) Cancelled() bool {
	return atomic.LoadInt32(&t.cancelled) != 0
}

// wheel is one level of the hierarchy. Buckets hold timers whose deadline
// falls inside the bucket's tick range; when the wheel advances into a
// bucket, its timers are cascaded down to lower (finer) levels.
type wheel struct {
	tickSize int64 // base ticks per bucket
	interval int64 // tickSize * WheelSize
	current  int64 // current time of this level, aligned to tickSize
	buckets  [WheelSize]list.List
}

// add places t in a bucket. It returns false when the deadline lies inside
// the current (already entered) slot, meaning t belongs to a lower level
// or has expired.
func (w *wheel) add(t *Timer) bool {
	if t.deadline < w.current+w.tickSize {
		return false
	}
	w.forceAdd(t)
	return true
}

func (w *wheel) forceAdd(t *Timer) {
	b := &w.buckets[int((t.deadline/w.tickSize)%WheelSize)]
	t.bucket = b
	t.elem = b.PushBack(t)
}

// TimingWheel is a hierarchical timing wheel scheduler. It is safe for
// concurrent use, but tasks run synchronously on the goroutine that
// advances the clock.
type TimingWheel struct {
	mu     sync.Mutex
	now    int64
	active int
	wheels [NumLevels]wheel
}

// New creates a TimingWheel whose discrete clock starts at tick start
// (start must be >= 0).
func New(start int64) *TimingWheel {
	tw := &TimingWheel{now: start}
	tick := int64(1)
	for i := range tw.wheels {
		tw.wheels[i] = wheel{
			tickSize: tick,
			interval: tick * WheelSize,
			current:  start - start%tick,
		}
		tick *= WheelSize
	}
	return tw
}

// Now returns the current tick of the discrete clock.
func (tw *TimingWheel) Now() int64 {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.now
}

// Active returns the number of live (scheduled, not yet fired or
// cancelled) timers.
func (tw *TimingWheel) Active() int {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.active
}

// AfterFunc schedules task to run once, delay ticks from now. A delay <= 0
// runs the task immediately on the calling goroutine.
func (tw *TimingWheel) AfterFunc(delay int64, task Task) *Timer {
	t := &Timer{task: task, tw: tw}
	tw.mu.Lock()
	t.deadline = tw.now + delay
	now := tw.now
	if tw.placeLocked(t) {
		tw.active++
		tw.mu.Unlock()
		return t
	}
	tw.mu.Unlock()
	task(now) // already expired: run immediately
	return t
}

// TickFunc schedules task to run every period ticks, first at now+period.
// The returned Timer reschedules itself until Cancel is called.
func (tw *TimingWheel) TickFunc(period int64, task Task) *Timer {
	if period <= 0 {
		panic("timingwheel: ticker period must be positive")
	}
	t := &Timer{period: period, task: task, tw: tw}
	tw.mu.Lock()
	t.deadline = tw.now + period
	tw.placeLocked(t) // always succeeds: deadline > now
	tw.active++
	tw.mu.Unlock()
	return t
}

// Advance moves the discrete clock forward by exactly one tick, firing any
// timers that expire.
func (tw *TimingWheel) Advance() {
	tw.AdvanceTo(tw.Now() + 1)
}

// AdvanceTo moves the discrete clock forward to the given tick, firing
// every timer whose deadline has been reached, in deadline order. Tasks
// run synchronously after the wheel state has been updated.
func (tw *TimingWheel) AdvanceTo(tick int64) {
	tw.mu.Lock()
	if tick <= tw.now {
		tw.mu.Unlock()
		return
	}
	var fired []firing
	for i := range tw.wheels {
		w := &tw.wheels[i]
		for w.current+w.tickSize <= tick {
			w.current += w.tickSize
			b := &w.buckets[int((w.current/w.tickSize)%WheelSize)]
			// Snapshot first: cascading may re-insert into this bucket.
			batch := make([]*Timer, 0, b.Len())
			for e := b.Front(); e != nil; e = e.Next() {
				batch = append(batch, e.Value.(*Timer))
			}
			b.Init()
			for _, t := range batch {
				t.bucket, t.elem = nil, nil
				if t.Cancelled() {
					tw.active--
					t.task = nil
					continue
				}
				if tw.placeLocked(t) {
					continue // cascaded to a lower wheel
				}
				// Expired exactly at t.deadline. Tickers re-arm and may
				// expire several times within one AdvanceTo call.
				for {
					fired = append(fired, firing{t: t, task: t.task, at: t.deadline})
					if t.period <= 0 {
						tw.active--
						t.task = nil
						break
					}
					t.deadline += t.period
					if tw.placeLocked(t) {
						break
					}
				}
			}
		}
	}
	tw.now = tick
	tw.mu.Unlock()
	for _, f := range fired {
		if !f.t.Cancelled() {
			f.task(f.at)
		}
	}
}

// placeLocked inserts t into the lowest wheel whose interval contains the
// deadline. It returns false when the deadline has already been reached
// and the timer must fire. Deadlines beyond the top wheel's range are
// parked in the top wheel and cascade down as it revolves.
func (tw *TimingWheel) placeLocked(t *Timer) bool {
	for i := range tw.wheels {
		w := &tw.wheels[i]
		if t.deadline < w.current+w.interval {
			return w.add(t)
		}
	}
	tw.wheels[NumLevels-1].forceAdd(t)
	return true
}

// bucketed counts the timers currently stored in buckets (for tests).
func (tw *TimingWheel) bucketed() int {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	n := 0
	for i := range tw.wheels {
		for j := range tw.wheels[i].buckets {
			n += tw.wheels[i].buckets[j].Len()
		}
	}
	return n
}

type firing struct {
	t    *Timer
	task Task
	at   int64
}
