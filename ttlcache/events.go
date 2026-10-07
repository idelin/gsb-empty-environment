package ttlcache

import "time"

// EventKind identifies the type of a recorded cache mutation.
type EventKind int

const (
	// EventSet records a Set: a new generation of the key was created.
	EventSet EventKind = iota
	// EventDelete records an explicit Delete of a specific generation.
	EventDelete
	// EventExpire records the natural expiry of a specific generation.
	EventExpire
)

func (k EventKind) String() string {
	switch k {
	case EventSet:
		return "set"
	case EventDelete:
		return "delete"
	case EventExpire:
		return "expire"
	}
	return "unknown"
}

// Event is a single recorded mutation. Gen ties the event to one generation
// of the key, so stale events can never touch a newer value.
type Event struct {
	Kind  EventKind
	Key   string
	Value interface{}   // set events only
	Gen   uint64        // generation the event applies to
	At    time.Duration // cache-monotonic time of the event
	TTL   time.Duration // set events only
}

// Replay folds an event log into the set of key/value pairs live at the
// given monotonic time: only events stamped at or before now are applied,
// and entries whose deadline has passed by then are dropped. Delete and
// expire events only remove an entry when their generation matches the
// generation created by the most recent set event for that key; stale
// events are ignored.
func Replay(events []Event, now time.Duration) map[string]interface{} {
	type liveEntry struct {
		value     interface{}
		gen       uint64
		expiresAt time.Duration
	}
	state := make(map[string]liveEntry)
	for _, ev := range events {
		if ev.At > now {
			continue
		}
		switch ev.Kind {
		case EventSet:
			state[ev.Key] = liveEntry{value: ev.Value, gen: ev.Gen, expiresAt: ev.At + ev.TTL}
		case EventDelete, EventExpire:
			if cur, ok := state[ev.Key]; ok && cur.gen == ev.Gen {
				delete(state, ev.Key)
			}
		}
	}
	out := make(map[string]interface{})
	for k, e := range state {
		if e.expiresAt > now {
			out[k] = e.value
		}
	}
	return out
}
