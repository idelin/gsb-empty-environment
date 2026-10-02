// Package memsched implements a coroutine (fiber) scheduler that cooperates
// with memory accounting: a Monitor tracks temporary allocations per fiber,
// and the Scheduler applies backpressure (admission control) when global
// memory usage crosses a high watermark, prioritizing GC-capable fibers.
package memsched

import "sync"

// Monitor tracks temporary memory allocated by fibers and reports global
// memory pressure. It is the single source of truth for memory accounting.
type Monitor struct {
	mu       sync.Mutex
	cond     sync.Cond
	capacity int64
	used     int64
	maxUsed  int64
	perFiber map[int64]int64

	highWatermark float64 // backpressure engages at/above this usage ratio
	lowWatermark  float64 // backpressure releases below this usage ratio

	onChange func()
}

// NewMonitor creates a Monitor with the given capacity (in abstract memory
// units) and high/low watermarks as fractions of capacity, e.g. 0.8/0.6.
func NewMonitor(capacity int64, high, low float64) *Monitor {
	if capacity <= 0 {
		panic("memsched: capacity must be positive")
	}
	if low <= 0 || high <= low || high > 1 {
		panic("memsched: require 0 < low < high <= 1")
	}
	m := &Monitor{
		capacity:      capacity,
		perFiber:      make(map[int64]int64),
		highWatermark: high,
		lowWatermark:  low,
	}
	m.cond.L = &m.mu
	return m
}

// SetOnChange registers a callback invoked (without the monitor lock held)
// whenever the accounting changes. The scheduler uses it to wake up its
// admission loop.
func (m *Monitor) SetOnChange(cb func()) {
	m.mu.Lock()
	m.onChange = cb
	m.mu.Unlock()
}

func (m *Monitor) notify() {
	m.mu.Lock()
	cb := m.onChange
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// tryAlloc atomically accounts size units to the fiber if they fit within
// capacity, and reports whether it did. The scheduler uses it to reserve a
// task's estimated demand at admission time.
func (m *Monitor) tryAlloc(fiberID, size int64) bool {
	m.mu.Lock()
	if m.used+size > m.capacity {
		m.mu.Unlock()
		return false
	}
	m.used += size
	if m.used > m.maxUsed {
		m.maxUsed = m.used
	}
	m.perFiber[fiberID] += size
	m.mu.Unlock()
	m.notify()
	return true
}

// alloc blocks until size units can be accounted to the fiber, then records
// them. It never overcommits: used never exceeds capacity.
func (m *Monitor) alloc(fiberID, size int64) {
	m.mu.Lock()
	for m.used+size > m.capacity {
		m.cond.Wait()
	}
	m.used += size
	if m.used > m.maxUsed {
		m.maxUsed = m.used
	}
	m.perFiber[fiberID] += size
	m.mu.Unlock()
	m.notify()
}

// release returns size units previously accounted to the fiber.
func (m *Monitor) release(fiberID, size int64) {
	m.mu.Lock()
	cur := m.perFiber[fiberID]
	if size > cur {
		size = cur
	}
	m.perFiber[fiberID] = cur - size
	if m.perFiber[fiberID] == 0 {
		delete(m.perFiber, fiberID)
	}
	m.used -= size
	m.cond.Broadcast()
	m.mu.Unlock()
	m.notify()
}

// releaseAll frees everything the fiber still holds (including unconsumed
// reservation) and returns the amount.
func (m *Monitor) releaseAll(fiberID int64) int64 {
	m.mu.Lock()
	size := m.perFiber[fiberID]
	delete(m.perFiber, fiberID)
	m.used -= size
	m.cond.Broadcast()
	m.mu.Unlock()
	m.notify()
	return size
}

// Used returns the currently accounted memory units.
func (m *Monitor) Used() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used
}

// MaxUsed returns the high-water mark of accounted memory units.
func (m *Monitor) MaxUsed() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxUsed
}

// Usage returns the current usage ratio in [0,1].
func (m *Monitor) Usage() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return float64(m.used) / float64(m.capacity)
}

// Capacity returns the configured capacity.
func (m *Monitor) Capacity() int64 { return m.capacity }

// HighWatermark returns the backpressure engagement ratio.
func (m *Monitor) HighWatermark() float64 { return m.highWatermark }

// LowWatermark returns the backpressure release ratio.
func (m *Monitor) LowWatermark() float64 { return m.lowWatermark }

// Pressured reports whether usage is at or above the high watermark.
func (m *Monitor) Pressured() bool { return m.Usage() >= m.highWatermark }

// FiberUsage returns the temp memory currently accounted to one fiber.
func (m *Monitor) FiberUsage(fiberID int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.perFiber[fiberID]
}
