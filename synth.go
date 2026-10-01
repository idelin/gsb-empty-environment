package cardinalguard

import (
	"fmt"
	"math/rand"
)

// SynthWorkload deterministically synthesizes a telemetry event log:
// samples across tenants/metrics/label combinations, periodic reaps, and
// occasional label corrections. The same seed always produces the same log,
// so a synthesized workload can be saved as a fixture and replayed.
func SynthWorkload(seed int64, tenants, metrics, combos, events int) []Event {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Event, 0, events)
	var tick, seq int64 = 1, 1

	next := func(ev Event) {
		ev.Seq = seq
		seq++
		out = append(out, ev)
	}
	tenant := func() string { return fmt.Sprintf("tenant-%d", rng.Intn(tenants)) }
	metric := func() string { return fmt.Sprintf("metric-%d", rng.Intn(metrics)) }
	labels := func() LabelSet {
		return LabelSet{
			"host":  fmt.Sprintf("host-%d", rng.Intn(combos)),
			"zone":  fmt.Sprintf("z%d", rng.Intn(3)),
			"phase": fmt.Sprintf("p%d", rng.Intn(2)),
		}
	}

	for i := 0; i < events; i++ {
		tick += 1 + int64(rng.Intn(3))
		switch roll := rng.Intn(100); {
		case roll < 78:
			next(Event{
				Type:   EventSample,
				Tick:   tick,
				Tenant: tenant(),
				Metric: metric(),
				Labels: labels(),
				Bytes:  int64(8 + rng.Intn(64)),
			})
		case roll < 88:
			next(Event{Type: EventReap, Tick: tick})
		case roll < 96:
			// Correct a real combination by rewriting one label value.
			l := labels()
			fixed := copyLabels(l)
			fixed["host"] = fmt.Sprintf("host-%d", rng.Intn(combos))
			next(Event{
				Type:      EventCorrect,
				Tick:      tick,
				Tenant:    tenant(),
				Metric:    metric(),
				Labels:    l,
				NewLabels: fixed,
			})
		default:
			// Late sample: reuse an old tick so it belongs to a past
			// generation once the clock has advanced.
			lateTick := tick - int64(rng.Intn(50))
			if lateTick < 1 {
				lateTick = 1
			}
			next(Event{
				Type:   EventSample,
				Tick:   lateTick,
				Tenant: tenant(),
				Metric: metric(),
				Labels: labels(),
				Bytes:  int64(8 + rng.Intn(64)),
			})
		}
	}
	return out
}
