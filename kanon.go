package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func deriveQIValues(rec *workRecord, cfg Config) map[string]string {
	values := make(map[string]string, len(rec.quasi)+len(reservedQIs))
	for name, value := range rec.quasi {
		if _, reserved := reservedQIs[name]; reserved {
			continue
		}
		values[name] = value
	}

	counts := make(map[string]int)
	for _, cell := range rec.cells {
		counts[cell.ID]++
	}
	cells := make([]string, 0, len(counts))
	for cell := range counts {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if counts[cells[i]] != counts[cells[j]] {
			return counts[cells[i]] > counts[cells[j]]
		}
		return cells[i] < cells[j]
	})

	values[QIFrequentLocation1] = "none"
	values[QIFrequentLocation2] = "none"
	if len(cells) > 0 {
		values[QIFrequentLocation1] = cells[0]
	}
	if len(cells) > 1 {
		values[QIFrequentLocation2] = cells[1]
	}

	if len(rec.points) == 0 {
		values[QIStartTimeOfDay] = "none"
		values[QIDayPattern] = "none"
		values[QIGeneralizedPath] = "none"
		values[QITemporalShape] = "none"
		return values
	}
	values[QIStartTimeOfDay] = "tod-" + strconv.FormatInt(timeOfDaySlot(rec.points[0].Time, cfg.TimeBucket), 10)
	values[QIDayPattern] = dayPattern(rec.points)
	values[QIGeneralizedPath] = generalizedPath(rec.cells)
	values[QITemporalShape] = temporalShape(rec.points)
	return values
}

func temporalShape(points []Point) string {
	parts := make([]string, len(points))
	for i, p := range points {
		parts[i] = p.Time.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join(parts, ">")
}

func dayPattern(points []Point) string {
	days := make(map[int]struct{})
	for _, p := range points {
		days[int(p.Time.Weekday())] = struct{}{}
	}
	order := []int{1, 2, 3, 4, 5, 6, 0}
	names := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	out := make([]string, 0, len(days))
	for _, day := range order {
		if _, ok := days[day]; ok {
			out = append(out, names[day])
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}

func generalizeRareAttributes(recs []*workRecord, cfg Config) bool {
	counts := make(map[string]map[string]int)
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		for name, value := range rec.quasi {
			if _, reserved := reservedQIs[name]; reserved {
				continue
			}
			if counts[name] == nil {
				counts[name] = make(map[string]int)
			}
			counts[name][value]++
		}
	}

	changed := false
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		for name := range counts {
			value, present := rec.quasi[name]
			if !present {
				continue
			}
			if counts[name][value] < cfg.K && value != "*" {
				if rec.quasi == nil {
					rec.quasi = make(map[string]string)
				}
				rec.quasi[name] = "*"
				changed = true
			}
		}
	}
	return changed
}

func availableQIs(recs []*workRecord) []string {
	names := map[string]struct{}{
		QIFrequentLocation1: {},
		QIFrequentLocation2: {},
		QIStartTimeOfDay:    {},
		QIDayPattern:        {},
		QIGeneralizedPath:   {},
		QITemporalShape:     {},
	}
	for _, rec := range recs {
		if !rec.active {
			continue
		}
		for name := range rec.quasi {
			if _, reserved := reservedQIs[name]; reserved {
				continue
			}
			names[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func combinationsFor(recs []*workRecord, cfg Config) ([][]string, error) {
	available := availableQIs(recs)

	if len(cfg.QICombinations) > 0 {
		out := make([][]string, 0, len(cfg.QICombinations))
		seen := make(map[string]struct{})
		for _, combo := range cfg.QICombinations {
			normalized := make([]string, 0, len(combo))
			local := make(map[string]struct{}, len(combo))
			for _, name := range combo {
				if _, duplicate := local[name]; duplicate {
					continue
				}
				local[name] = struct{}{}
				normalized = append(normalized, name)
			}
			sort.Strings(normalized)
			key := strings.Join(normalized, "\x00")
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, normalized)
		}
		full := append([]string(nil), available...)
		sort.Strings(full)
		key := strings.Join(full, "\x00")
		if _, ok := seen[key]; !ok {
			out = append(out, full)
		}
		return out, nil
	}

	out := make([][]string, 0)
	seen := make(map[string]struct{})
	add := func(combo []string) {
		if len(combo) == 0 {
			return
		}
		normalized := append([]string(nil), combo...)
		sort.Strings(normalized)
		key := strings.Join(normalized, "\x00")
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}

	for _, name := range available {
		add([]string{name})
	}
	if len(available) <= 12 {
		for i := 0; i < len(available); i++ {
			for j := i + 1; j < len(available); j++ {
				add([]string{available[i], available[j]})
			}
		}
	} else {
		for i := 0; i < len(available); i++ {
			add([]string{available[i], available[(i+1)%len(available)]})
		}
	}
	add(available)
	return out, nil
}

func validateQICombinationNames(ds Dataset, cfg Config) error {
	known := make(map[string]struct{}, len(reservedQIs))
	for name := range reservedQIs {
		known[name] = struct{}{}
	}
	for _, rec := range ds {
		for name := range rec.QuasiIdentifiers {
			known[name] = struct{}{}
		}
	}
	for _, combo := range cfg.QICombinations {
		for _, name := range combo {
			if _, ok := known[name]; !ok {
				return fmt.Errorf("netsec: unknown quasi identifier %q", name)
			}
		}
	}
	return nil
}

func comboKey(values map[string]string, combo []string) string {
	var b strings.Builder
	for _, name := range combo {
		value := values[name]
		b.WriteString(strconv.Itoa(len(name)))
		b.WriteByte(':')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(strconv.Itoa(len(value)))
		b.WriteByte(':')
		b.WriteString(value)
		b.WriteByte(';')
	}
	return b.String()
}

func comboValues(values map[string]string, combo []string) map[string]string {
	out := make(map[string]string, len(combo))
	for _, name := range combo {
		out[name] = values[name]
	}
	return out
}

func checkKAnonymity(recs []*workRecord, cfg Config) (KAnonymityReport, map[*workRecord]struct{}, error) {
	active := activeRecords(recs)
	report := KAnonymityReport{
		K:                   cfg.K,
		Satisfied:           len(active) > 0,
		MinGroupSize:        0,
		CheckedCombinations: 0,
	}
	if len(active) == 0 {
		return report, nil, nil
	}

	combos, err := combinationsFor(active, cfg)
	if err != nil {
		return KAnonymityReport{}, nil, err
	}

	report.CheckedCombinations = len(combos)
	report.MinGroupSize = len(active) + 1
	violators := make(map[*workRecord]struct{})

	values := make(map[*workRecord]map[string]string, len(active))
	for _, rec := range active {
		values[rec] = deriveQIValues(rec, cfg)
	}

	const maxViolations = 50
	for _, combo := range combos {
		groups := make(map[string][]*workRecord)
		for _, rec := range active {
			key := comboKey(values[rec], combo)
			groups[key] = append(groups[key], rec)
		}
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		minSize := len(active)
		maxSize := 0
		satisfied := true
		for _, key := range keys {
			members := groups[key]
			size := len(members)
			if size < minSize {
				minSize = size
			}
			if size > maxSize {
				maxSize = size
			}
			if size < cfg.K {
				satisfied = false
				for _, rec := range members {
					violators[rec] = struct{}{}
				}
				if len(report.Violations) < maxViolations {
					first := members[0]
					report.Violations = append(report.Violations, Violation{
						Combination: append([]string(nil), combo...),
						Values:      comboValues(values[first], combo),
						Size:        size,
					})
				}
			}
		}
		if minSize < report.MinGroupSize {
			report.MinGroupSize = minSize
		}
		report.Combinations = append(report.Combinations, CombinationReport{
			Combination:  append([]string(nil), combo...),
			MinGroupSize: minSize,
			MaxGroupSize: maxSize,
			GroupCount:   len(groups),
			Satisfied:    satisfied,
		})
		if !satisfied {
			report.Satisfied = false
		}
	}
	return report, violators, nil
}

func enforceKAnonymity(recs []*workRecord, cfg Config, report *PublishReport) (KAnonymityReport, bool, error) {
	generalizeRareAttributes(recs, cfg)
	kReport, violators, err := checkKAnonymity(recs, cfg)
	if err != nil {
		return KAnonymityReport{}, false, err
	}
	if len(violators) == 0 {
		return kReport, false, nil
	}
	for rec := range violators {
		rec.suppress("suppression", "k-anonymity",
			fmt.Sprintf("record belongs to a quasi-identifier equivalence class smaller than k=%d", cfg.K))
		report.KAnonymitySuppressed++
	}
	return kReport, true, nil
}
