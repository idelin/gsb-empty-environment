package ttlcache

import "time"

// Record is the persistent form of one cache entry. It stores the remaining
// lifetime and the cache-monotonic time at which the snapshot was taken, so
// a restore can subtract the downtime that elapsed while the process was
// stopped instead of restarting the TTL from scratch.
type Record struct {
	Key       string
	Value     interface{}
	Gen       uint64
	Remaining time.Duration
	SavedAt   time.Duration
}

// Snapshot serializes every live entry, capturing each entry's remaining
// lifetime at the cache's current time.
func (c *Cache) Snapshot() []Record {
	var out []Record
	for k, e := range c.entries {
		remaining := e.expiresAt - c.now
		if remaining <= 0 {
			continue
		}
		out = append(out, Record{
			Key:       k,
			Value:     e.value,
			Gen:       e.gen,
			Remaining: remaining,
			SavedAt:   c.now,
		})
	}
	return out
}

// Restore rebuilds a cache from a snapshot taken before a downtime-long
// stop. Every record's remaining lifetime is reduced by downtime; records
// whose remaining lifetime is not positive expire immediately (an expire
// event is logged at time zero) and are never re-granted a full TTL.
//
// The restored cache's clock starts at zero, which represents the moment of
// restoration. Generations are preserved and the generation counter resumes
// above every restored generation, so events from before the stop can never
// delete values set afterwards.
func Restore(records []Record, downtime time.Duration) *Cache {
	c := New()
	for _, r := range records {
		if r.Gen > c.genCounter {
			c.genCounter = r.Gen
		}
		remaining := r.Remaining - downtime
		if remaining <= 0 {
			c.events = append(c.events, Event{
				Kind: EventExpire, Key: r.Key, Gen: r.Gen, At: 0,
			})
			continue
		}
		c.entries[r.Key] = &entry{value: r.Value, gen: r.Gen, expiresAt: remaining}
		c.events = append(c.events, Event{
			Kind: EventSet, Key: r.Key, Value: r.Value, Gen: r.Gen, At: 0, TTL: remaining,
		})
	}
	return c
}
