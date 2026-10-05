package schedule

import (
	"sort"
	"time"
)

// Index is an incrementally updatable query index over the instances of
// one rule. Instances are kept sorted by (Start, Week); queries binary
// search the slice instead of expanding all historical weeks.
type Index struct {
	items  []Instance       // sorted by (Start, Week)
	byWeek map[int]Instance // week index -> live instance
	maxDur time.Duration    // upper bound on instance duration
}

func newIndex() *Index {
	return &Index{byWeek: make(map[int]Instance)}
}

// search returns the insertion position for (start, week).
func (ix *Index) search(start time.Time, week int) int {
	return sort.Search(len(ix.items), func(i int) bool {
		it := ix.items[i]
		if it.Start.Equal(start) {
			return it.Week >= week
		}
		return it.Start.After(start)
	})
}

// upsert inserts or replaces the instance for its week.
func (ix *Index) upsert(inst Instance) {
	ix.remove(inst.Week)
	pos := ix.search(inst.Start, inst.Week)
	ix.items = append(ix.items, Instance{})
	copy(ix.items[pos+1:], ix.items[pos:])
	ix.items[pos] = inst
	ix.byWeek[inst.Week] = inst
	if d := inst.End.Sub(inst.Start); d > ix.maxDur {
		ix.maxDur = d
	}
}

// remove deletes the instance for a week, if present. maxDur is left
// untouched: a stale (larger) bound only widens the query scan window
// and never breaks correctness.
func (ix *Index) remove(week int) {
	old, ok := ix.byWeek[week]
	if !ok {
		return
	}
	pos := ix.search(old.Start, week)
	if pos < len(ix.items) && ix.items[pos].Week == week {
		ix.items = append(ix.items[:pos], ix.items[pos+1:]...)
	}
	delete(ix.byWeek, week)
}

// Query returns every instance whose actual half-open interval
// [Start, End) intersects the UTC half-open query interval [qs, qe).
// Intersection test: Start < qe && qs < End.
func (ix *Index) Query(qs, qe time.Time) []Instance {
	if !qs.Before(qe) {
		return nil
	}
	// An instance starting before qs can still overlap if its duration
	// reaches into the window; maxDur bounds how far back we must look.
	lo := ix.search(qs.Add(-ix.maxDur), -1)
	var out []Instance
	for i := lo; i < len(ix.items); i++ {
		it := ix.items[i]
		if !it.Start.Before(qe) {
			break
		}
		if qs.Before(it.End) {
			out = append(out, it)
		}
	}
	return out
}
