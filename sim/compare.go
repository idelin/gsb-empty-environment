package sim

import (
	"fmt"
	"sort"
)

// Diff compares two engines field by field — clock, entity attributes,
// per-entity random-stream state and draw counts, and pending queues —
// and returns a human-readable list of differences. An empty result
// means the two branches replayed identically.
func Diff(a, b *Engine) []string {
	var diffs []string
	if a.now != b.now {
		diffs = append(diffs, fmt.Sprintf("clock: %d != %d", a.now, b.now))
	}
	if a.seed != b.seed {
		diffs = append(diffs, fmt.Sprintf("seed: %d != %d", a.seed, b.seed))
	}
	aIDs, bIDs := a.EntityIDs(), b.EntityIDs()
	if !equalStrings(aIDs, bIDs) {
		diffs = append(diffs, fmt.Sprintf("entity sets: %v != %v", aIDs, bIDs))
	}
	for _, id := range aIDs {
		ea, eb := a.entities[id], b.entities[id]
		if eb == nil {
			continue
		}
		diffs = append(diffs, diffEntity(id, ea, eb)...)
	}
	diffs = append(diffs, diffQueue(a.queue, b.queue)...)
	return diffs
}

func diffEntity(id string, a, b *Entity) []string {
	var diffs []string
	keys := make(map[string]bool, len(a.Attrs)+len(b.Attrs))
	for k := range a.Attrs {
		keys[k] = true
	}
	for k := range b.Attrs {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if a.Attrs[k] != b.Attrs[k] {
			diffs = append(diffs, fmt.Sprintf("entity %s attr %s: %d != %d", id, k, a.Attrs[k], b.Attrs[k]))
		}
	}
	if a.rng.state != b.rng.state {
		diffs = append(diffs, fmt.Sprintf("entity %s rng state: %d != %d", id, a.rng.state, b.rng.state))
	}
	if a.rng.calls != b.rng.calls {
		diffs = append(diffs, fmt.Sprintf("entity %s rand calls: %d != %d", id, a.rng.calls, b.rng.calls))
	}
	return diffs
}

func diffQueue(a, b []Event) []string {
	if len(a) != len(b) {
		return []string{fmt.Sprintf("queue length: %d != %d", len(a), len(b))}
	}
	as := append([]Event(nil), a...)
	bs := append([]Event(nil), b...)
	sort.Slice(as, func(i, j int) bool { return lessEvent(as[i], as[j]) })
	sort.Slice(bs, func(i, j int) bool { return lessEvent(bs[i], bs[j]) })
	var diffs []string
	for i := range as {
		if as[i] != bs[i] {
			diffs = append(diffs, fmt.Sprintf("queue[%d]: %+v != %+v", i, as[i], bs[i]))
		}
	}
	return diffs
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
