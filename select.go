package retention

import (
	"fmt"
	"time"
)

// Policy is a GFS (grandfather-father-son) retention policy. For each
// granularity, the newest recovery point in each of the N most recent
// buckets (days / ISO weeks / months, relative to the reference time)
// is selected for retention.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// SelectedPoint is a recovery point chosen by the retention policy,
// annotated with the reasons it was selected, its dependency chain,
// and its restorability.
type SelectedPoint struct {
	Point    Point
	Reasons  []string
	Chain    []Point
	Usable   bool
	Problems []string
}

type bucket struct {
	index int
	label string
}

func dayBucket(t time.Time) bucket {
	y, m, d := t.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	return bucket{int(midnight.Unix() / 86400), fmt.Sprintf("daily:%s", midnight.Format("2006-01-02"))}
}

func weekBucket(t time.Time) bucket {
	y, m, d := t.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	// Roll back to Monday of this ISO week.
	offset := (int(midnight.Weekday()) + 6) % 7
	monday := midnight.AddDate(0, 0, -offset)
	yw, ww := monday.ISOWeek()
	return bucket{int(monday.Unix() / 86400), fmt.Sprintf("weekly:%d-W%02d", yw, ww)}
}

func monthBucket(t time.Time) bucket {
	y, m, _ := t.Date()
	return bucket{y*12 + int(m) - 1, fmt.Sprintf("monthly:%04d-%02d", y, int(m))}
}

// Select chooses recovery points according to the policy. The newest
// point in each retained bucket wins; a point kept by several rules
// accumulates several reasons. Every selected point is annotated with
// its dependency chain and restorability — corrupt or incomplete
// chains mark the point unusable but never remove it from the report.
func Select(g *Graph, p Policy, now time.Time) []SelectedPoint {
	type rule struct {
		keep   int
		bucket func(time.Time) bucket
	}
	rules := []rule{
		{p.Daily, dayBucket},
		{p.Weekly, weekBucket},
		{p.Monthly, monthBucket},
	}

	reasons := make(map[string][]string)
	for _, r := range rules {
		if r.keep <= 0 {
			continue
		}
		nowIdx := r.bucket(now).index
		// newest point per bucket index
		best := make(map[int]Point)
		for _, pt := range g.Points() {
			b := r.bucket(pt.Time)
			if b.index > nowIdx || b.index <= nowIdx-r.keep {
				continue
			}
			cur, ok := best[b.index]
			if !ok || pt.Time.After(cur.Time) ||
				(pt.Time.Equal(cur.Time) && pt.ID > cur.ID) {
				best[b.index] = pt
			}
		}
		for _, pt := range best {
			label := r.bucket(pt.Time).label
			reasons[pt.ID] = append(reasons[pt.ID], label)
		}
	}

	var out []SelectedPoint
	for _, pt := range g.Points() {
		rs, ok := reasons[pt.ID]
		if !ok {
			continue
		}
		usable, problems := g.Usable(pt.ID)
		out = append(out, SelectedPoint{
			Point:    pt,
			Reasons:  rs,
			Chain:    g.Chain(pt.ID),
			Usable:   usable,
			Problems: problems,
		})
	}
	return out
}
