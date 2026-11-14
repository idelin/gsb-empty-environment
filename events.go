package netsec

import "sync"

// Op describes what happened to a key.
type Op int

const (
	// OpPut is a write that creates or replaces a key.
	OpPut Op = iota + 1
	// OpDelete removes a key.
	OpDelete
)

// Event is a single key change delivered to authorized watches.
type Event struct {
	// Version is the monotonically increasing, process-wide version
	// assigned by the authorizer when the event was published.
	Version int64
	// Tenant identifies the owner of the key.
	Tenant string
	// Op is the mutation type.
	Op Op
	// Key is the full key, which always starts with one of the tenant's
	// namespace prefixes.
	Key string
	// Value is the put payload; nil for deletes.
	Value []byte
}

// eventLog is a bounded ring buffer of historical events. Old entries
// are overwritten once capacity is reached, and minVersion tracks the
// oldest version that can still be replayed.
type eventLog struct {
	mu       sync.RWMutex
	ring     []Event
	capacity int
	head     int // index of the oldest retained event
	size     int
	version  int64
}

func newEventLog(capacity int) *eventLog {
	if capacity < 1 {
		capacity = 1
	}
	return &eventLog{
		ring:     make([]Event, capacity),
		capacity: capacity,
	}
}

func (l *eventLog) append(ev Event) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.version++
	ev.Version = l.version
	if l.size < l.capacity {
		l.ring[(l.head+l.size)%l.capacity] = ev
		l.size++
	} else {
		l.ring[l.head] = ev
		l.head = (l.head + 1) % l.capacity
	}
	return ev.Version
}

func (l *eventLog) latestVersion() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.version
}

// minVersion returns the oldest retained version, or zero when the log is
// empty.
func (l *eventLog) minVersion() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.size == 0 {
		return 0
	}
	return l.ring[l.head].Version
}

// since returns retained events whose version is at least start, in
// version order.
func (l *eventLog) since(start int64) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Event, 0, l.size)
	for i := 0; i < l.size; i++ {
		ev := l.ring[(l.head+i)%l.capacity]
		if ev.Version >= start {
			out = append(out, ev)
		}
	}
	return out
}
