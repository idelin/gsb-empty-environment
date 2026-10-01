package pktscale

import "sort"

// Merge combines fragment batches from multiple streams into one
// timeline. Ordering is by (Tick, Seq, FragID): equal ticks fall back to
// the declared record order, so remainder compensation can never make a
// later-declared record overtake an earlier one across streams.
func Merge(batches ...[]Fragment) []Fragment {
	var all []Fragment
	for _, b := range batches {
		all = append(all, b...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.FragID < b.FragID
	})
	return all
}
