package netsec

import (
	"sort"
	"strconv"
	"strings"
)

func (e *engine) refusalResult(enumBest *evaluation) *Result {
	top := make([]int, len(e.heights))
	for col, height := range e.heights {
		top[col] = height
	}
	ev := e.evaluate(top)

	matched := enumBest == nil
	detail := "no level vector satisfies k-anonymity within the suppression budget"
	if !matched {
		detail = "enumeration unexpectedly found a feasible vector"
	}

	return &Result{
		Published: false,
		Evidence: Evidence{
			Suppressed:      ev.suppressed,
			SuppressionCost: ev.suppCost,
			Stats:           e.stats,
			CrossCheck: CrossCheck{
				Matched:    matched,
				Enumerated: e.enumerated,
				Detail:     detail,
			},
		},
	}
}

func (e *engine) publishResult(best *evaluation) *Result {
	groups := e.materialize(best)
	published := make([]Group, 0, len(groups))
	var suppressedIDs []string
	for _, group := range groups {
		if group.Suppressed {
			suppressedIDs = append(suppressedIDs, group.RecordIDs...)
		} else {
			published = append(published, group)
		}
	}

	return &Result{
		Published:           true,
		Level:               append([]int(nil), best.level...),
		Groups:              groups,
		PublishedGroups:     published,
		SuppressedRecordIDs: suppressedIDs,
		Evidence: Evidence{
			BestLevel:       append([]int(nil), best.level...),
			LevelCost:       best.levelCost,
			Suppressed:      best.suppressed,
			SuppressionCost: best.suppCost,
			TotalCost:       best.totalCost,
			Stats:           e.stats,
			CrossCheck: CrossCheck{
				Matched:    true,
				Enumerated: e.enumerated,
			},
		},
	}
}

func (e *engine) materialize(ev *evaluation) []Group {
	groups := make([]Group, 0, len(ev.buckets))
	for _, group := range ev.buckets {
		ids := make([]string, len(group.recordIdx))
		for i, row := range group.recordIdx {
			ids[i] = e.cfg.Records[row].ID
		}
		groups = append(groups, Group{
			Key:        append([]string(nil), group.key...),
			RecordIDs:  ids,
			Suppressed: len(group.recordIdx) < e.cfg.K,
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		return lexCompareString(groups[i].Key, groups[j].Key) < 0
	})
	return groups
}

func levelKey(level []int) string {
	var builder strings.Builder
	for col, value := range level {
		if col > 0 {
			builder.WriteByte(0)
		}
		builder.WriteString(strconv.Itoa(value))
	}
	return builder.String()
}

func lexCompareInt(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func lexCompareString(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
