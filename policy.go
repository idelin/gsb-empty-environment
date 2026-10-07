package retention

import (
	"fmt"
	"sort"
	"time"
)

// Policy defines GFS-style retention: keep the newest restorable recovery
// point in each of the last Daily days, Weekly weeks and Monthly months.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Decision records the retention outcome for a single backup.
type Decision struct {
	Backup   *Backup
	Kept     bool
	Selected bool     // selected directly by a retention rule
	Reasons  []string // why the backup is kept
	Chain    []string // dependency chain IDs, root first; nil if unavailable
	// Unavailable explains why the backup is not a usable recovery point.
	// Empty means restorable. Unavailable backups are reported, never
	// silently dropped.
	Unavailable string
}

// Report is the full outcome of a retention run.
type Report struct {
	Policy    Policy
	Now       time.Time
	Decisions []*Decision // one per backup, time order
	DeleteSet []string    // backup IDs safe to delete
	// Gaps lists retention buckets with no restorable recovery point.
	Gaps []string
}

type bucket struct {
	label string
	key   string
}

func dayKey(t time.Time) string   { return t.Format("2006-01-02") }
func monthKey(t time.Time) string { return t.Format("2006-01") }
func weekKey(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// Select applies the retention policy at time now and returns a report
// with per-backup decisions, selection rationale, dependency chains and
// the final delete set. The delete set always preserves the full
// dependency chain of every selected recovery point.
func (g *Graph) Select(p Policy, now time.Time) *Report {
	now = now.UTC()
	rep := &Report{Policy: p, Now: now}

	// Restorability and chains for every backup.
	chains := make(map[string][]*Backup)
	unavail := make(map[string]string)
	var candidates []*Backup // restorable, time order
	for _, b := range g.Backups() {
		chain, err := g.Chain(b.ID)
		if err != nil {
			unavail[b.ID] = err.Error()
			continue
		}
		chains[b.ID] = chain
		candidates = append(candidates, b)
	}

	// Build retention buckets relative to now.
	var buckets []bucket
	for i := 0; i < p.Daily; i++ {
		t := now.AddDate(0, 0, -i)
		buckets = append(buckets, bucket{label: "daily", key: dayKey(t)})
	}
	for i := 0; i < p.Weekly; i++ {
		t := now.AddDate(0, 0, -7*i)
		buckets = append(buckets, bucket{label: "weekly", key: weekKey(t)})
	}
	for i := 0; i < p.Monthly; i++ {
		t := now.AddDate(0, -i, 0)
		buckets = append(buckets, bucket{label: "monthly", key: monthKey(t)})
	}

	keyOf := func(label string, t time.Time) string {
		switch label {
		case "daily":
			return dayKey(t)
		case "weekly":
			return weekKey(t)
		default:
			return monthKey(t)
		}
	}

	selected := make(map[string]bool)
	reasons := make(map[string][]string)
	seenBucket := make(map[string]bool)
	for _, bk := range buckets {
		if seenBucket[bk.label+":"+bk.key] {
			continue
		}
		seenBucket[bk.label+":"+bk.key] = true
		// Newest restorable candidate in this bucket.
		var best *Backup
		for _, c := range candidates {
			if keyOf(bk.label, c.Time.UTC()) == bk.key {
				best = c
			}
		}
		if best == nil {
			rep.Gaps = append(rep.Gaps, fmt.Sprintf("%s %s: no restorable recovery point", bk.label, bk.key))
			continue
		}
		selected[best.ID] = true
		reasons[best.ID] = append(reasons[best.ID], fmt.Sprintf("%s %s", bk.label, bk.key))
	}

	// Keep set = selected points plus their entire dependency chains.
	kept := make(map[string]bool)
	for id := range selected {
		kept[id] = true
		for _, dep := range chains[id] {
			if kept[dep.ID] {
				continue
			}
			kept[dep.ID] = true
			if !selected[dep.ID] {
				reasons[dep.ID] = append(reasons[dep.ID], fmt.Sprintf("dependency of %s", id))
			}
		}
	}

	for _, b := range g.Backups() {
		d := &Decision{Backup: b, Kept: kept[b.ID], Selected: selected[b.ID]}
		if u, bad := unavail[b.ID]; bad {
			d.Unavailable = u
		} else {
			for _, c := range chains[b.ID] {
				d.Chain = append(d.Chain, c.ID)
			}
		}
		d.Reasons = reasons[b.ID]
		rep.Decisions = append(rep.Decisions, d)
		if !d.Kept {
			rep.DeleteSet = append(rep.DeleteSet, b.ID)
		}
	}
	sort.Strings(rep.Gaps)
	return rep
}
