package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func activeRecords(recs []*workRecord) []*workRecord {
	out := make([]*workRecord, 0, len(recs))
	for _, rec := range recs {
		if rec.active && len(rec.points) > 0 {
			out = append(out, rec)
		}
	}
	return out
}

func cellSupports(recs []*workRecord) map[string]int {
	supports := make(map[string]int)
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		seen := make(map[string]struct{}, len(rec.cells))
		for _, cell := range rec.cells {
			seen[cell.ID] = struct{}{}
		}
		for id := range seen {
			supports[id]++
		}
	}
	return supports
}

func removeRareLocations(recs []*workRecord, cfg Config, report *PublishReport) bool {
	supports := cellSupports(recs)
	changed := false

	for _, rec := range recs {
		if !rec.active {
			continue
		}
		points := make([]Point, 0, len(rec.points))
		cells := make([]cellBox, 0, len(rec.cells))
		removed := 0
		for i := range rec.points {
			if supports[rec.cells[i].ID] < cfg.MinLocationSupport {
				removed++
				continue
			}
			points = append(points, rec.points[i])
			cells = append(cells, rec.cells[i])
		}
		if removed == 0 {
			continue
		}

		changed = true
		rec.points = points
		rec.cells = cells
		rec.pointsRemoved += removed
		report.RareLocationPointsRemoved += removed
		if len(points) < cfg.MinPointsAfterSuppression {
			rec.suppress("suppression", "rare-location",
				fmt.Sprintf("%d rare points removed, only %d remain", removed, len(points)))
		}
	}
	return changed
}

func generalizedPath(cells []cellBox) string {
	if len(cells) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cells))
	var previous string
	for _, cell := range cells {
		if cell.ID == previous {
			continue
		}
		parts = append(parts, cell.ID)
		previous = cell.ID
	}
	return strings.Join(parts, ">")
}

func pathSupports(recs []*workRecord) map[string]int {
	supports := make(map[string]int)
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		supports[generalizedPath(rec.cells)]++
	}
	return supports
}

func removeRareTrajectories(recs []*workRecord, cfg Config, report *PublishReport) bool {
	if cfg.MinTrajectorySupport <= 1 {
		return false
	}
	supports := pathSupports(recs)
	changed := false
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		path := generalizedPath(rec.cells)
		if supports[path] < cfg.MinTrajectorySupport {
			rec.suppress("suppression", "rare-trajectory",
				fmt.Sprintf("generalized path support %d is below %d", supports[path], cfg.MinTrajectorySupport))
			report.RareTrajectoriesSuppressed++
			changed = true
		}
	}
	return changed
}

func timeOfDaySlot(t time.Time, bucket time.Duration) int64 {
	if bucket <= 0 {
		return 0
	}
	nanos := time.Duration(t.Hour())*time.Hour +
		time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second +
		time.Duration(t.Nanosecond())
	return int64(nanos / bucket)
}

func regularPattern(rec *workRecord, cfg Config) (string, bool) {
	bySlot := make(map[int64]map[string]struct{})
	for _, p := range rec.points {
		slot := timeOfDaySlot(p.Time, cfg.TimeBucket)
		date := p.Time.Format("2006-01-02")
		if bySlot[slot] == nil {
			bySlot[slot] = make(map[string]struct{})
		}
		bySlot[slot][date] = struct{}{}
	}

	slots := make([]int64, 0)
	for slot, dates := range bySlot {
		if len(dates) >= cfg.RegularRepeatDays {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		return "", false
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })

	parts := make([]string, 0, len(slots))
	for _, slot := range slots {
		days := make(map[int]struct{})
		for date := range bySlot[slot] {
			t, err := time.Parse("2006-01-02", date)
			if err == nil {
				days[int(t.Weekday())] = struct{}{}
			}
		}
		dayList := make([]int, 0, len(days))
		for day := range days {
			dayList = append(dayList, day)
		}
		sort.Ints(dayList)
		parts = append(parts, strconv.FormatInt(slot, 10)+":"+joinInts(dayList, ","))
	}
	return strings.Join(parts, ";"), true
}

func removeRareRegularPatterns(recs []*workRecord, cfg Config, report *PublishReport) bool {
	supports := make(map[string]int)
	patterns := make(map[*workRecord]string)
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		if key, regular := regularPattern(rec, cfg); regular {
			patterns[rec] = key
			supports[key]++
		}
	}
	changed := false
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		key, ok := patterns[rec]
		if !ok {
			continue
		}
		if supports[key] < cfg.MinPatternSupport {
			rec.suppress("suppression", "rare-regular-pattern",
				fmt.Sprintf("regular temporal pattern support %d is below %d", supports[key], cfg.MinPatternSupport))
			report.RegularPatternsSuppressed++
			changed = true
		}
	}
	return changed
}

func joinInts(values []int, sep string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, sep)
}
