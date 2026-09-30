package matchsim

import (
	"fmt"
	"sort"
	"time"
)

// Match is a credited fault<->alert pair.
type Match struct {
	FaultID string
	AlertID string
	Class   string        // symptom class of the fault
	Latency time.Duration // negative => alert led the fault (early warning)
}

func (m Match) Lead() bool { return m.Latency < 0 }

// Unmatched explains why an alert or fault got no credit.
type Unmatched struct {
	ID     string
	Reason string
}

// Stats accumulates latency samples.
type Stats struct {
	Count int
	Sum   time.Duration
	Min   time.Duration
	Max   time.Duration
}

func (s *Stats) Add(d time.Duration) {
	if s.Count == 0 || d < s.Min {
		s.Min = d
	}
	if s.Count == 0 || d > s.Max {
		s.Max = d
	}
	s.Sum += d
	s.Count++
}

func (s Stats) Mean() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return s.Sum / time.Duration(s.Count)
}

// Report is the alignment result for one clock offset.
type Report struct {
	Offset         time.Duration
	Matches        []Match
	FalsePositives []Unmatched // alerts with no credited fault
	FalseNegatives []Unmatched // ended faults with no credited alert
	Censored       []string    // right-censored faults (listed separately)
	Latency        Stats
	ClassStats     map[string]*Stats
	TotalFaults    int
	TotalAlerts    int
}

func (r Report) Detected() int { return len(r.Matches) }

// BuildReport aligns faults with alerts under a clock offset applied to
// alert timestamps, then derives detection latency, false positives,
// false negatives and the right-censored list.
func BuildReport(sc Scenario, offset time.Duration) Report {
	alerts := make([]Alert, len(sc.Alerts))
	for i, a := range sc.Alerts {
		a.Time = a.Time.Add(offset)
		alerts[i] = a
	}

	cost := CostMatrix(sc.Config, sc.Faults, alerts)
	pairs := MinCostMatching(cost)

	matchedF := make(map[int]bool, len(pairs))
	matchedA := make(map[int]bool, len(pairs))
	rep := Report{
		Offset:      offset,
		ClassStats:  map[string]*Stats{},
		TotalFaults: len(sc.Faults),
		TotalAlerts: len(alerts),
	}
	for _, p := range pairs {
		f, a := sc.Faults[p[0]], alerts[p[1]]
		lat := a.Time.Sub(f.Start)
		class := sc.Config.symptomClass(f.Symptom)
		rep.Matches = append(rep.Matches, Match{FaultID: f.ID, AlertID: a.ID, Class: class, Latency: lat})
		rep.Latency.Add(lat)
		cs := rep.ClassStats[class]
		if cs == nil {
			cs = &Stats{}
			rep.ClassStats[class] = cs
		}
		cs.Add(lat)
		matchedF[p[0]] = true
		matchedA[p[1]] = true
	}

	for j, a := range alerts {
		if !matchedA[j] {
			rep.FalsePositives = append(rep.FalsePositives, Unmatched{a.ID, alertReason(sc.Config, sc.Faults, a)})
		}
	}
	for i, f := range sc.Faults {
		if matchedF[i] {
			continue
		}
		if sc.censored(f) {
			rep.Censored = append(rep.Censored, f.ID)
		} else {
			rep.FalseNegatives = append(rep.FalseNegatives, Unmatched{f.ID, faultReason(sc.Config, f, alerts)})
		}
	}

	sort.Slice(rep.Matches, func(i, j int) bool { return rep.Matches[i].FaultID < rep.Matches[j].FaultID })
	sort.Slice(rep.FalsePositives, func(i, j int) bool { return rep.FalsePositives[i].ID < rep.FalsePositives[j].ID })
	sort.Slice(rep.FalseNegatives, func(i, j int) bool { return rep.FalseNegatives[i].ID < rep.FalseNegatives[j].ID })
	sort.Strings(rep.Censored)
	return rep
}

func alertReason(cfg Config, faults []Fault, a Alert) string {
	compatible, inWindow := false, false
	for _, f := range faults {
		if cfg.entityGroup(f.Entity) != cfg.entityGroup(a.Entity) ||
			cfg.symptomClass(f.Symptom) != cfg.symptomClass(a.Symptom) {
			continue
		}
		compatible = true
		if _, ok := feasible(cfg, f, a); ok {
			inWindow = true
		}
	}
	switch {
	case !compatible:
		return "no compatible fault (entity/symptom mismatch)"
	case !inWindow:
		return "compatible faults exist but all outside the time window"
	default:
		return "contention: compatible fault already claimed by a lower-cost alert"
	}
}

func faultReason(cfg Config, f Fault, alerts []Alert) string {
	compatible, inWindow := false, false
	for _, a := range alerts {
		if cfg.entityGroup(f.Entity) != cfg.entityGroup(a.Entity) ||
			cfg.symptomClass(f.Symptom) != cfg.symptomClass(a.Symptom) {
			continue
		}
		compatible = true
		if _, ok := feasible(cfg, f, a); ok {
			inWindow = true
		}
	}
	switch {
	case !compatible:
		return "no compatible alert (entity/symptom mismatch)"
	case !inWindow:
		return "compatible alerts exist but all outside the time window"
	default:
		return "contention: compatible alert claimed by another fault"
	}
}

// ApplyCorrections applies late-arriving timestamp fixes and returns the
// updated alert list plus human-readable notes (including skipped IDs).
func ApplyCorrections(alerts []Alert, cs []Correction) ([]Alert, []string) {
	byID := make(map[string]int, len(alerts))
	out := make([]Alert, len(alerts))
	for i, a := range alerts {
		out[i] = a
		byID[a.ID] = i
	}
	var notes []string
	for _, c := range cs {
		idx, ok := byID[c.AlertID]
		if !ok {
			notes = append(notes, fmt.Sprintf("%s: unknown alert, correction ignored", c.AlertID))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s: %s -> %s", c.AlertID,
			out[idx].Time.Format("15:04:05"), c.NewTime.Format("15:04:05")))
		out[idx].Time = c.NewTime
	}
	return out, notes
}

// Diff describes how a re-run (e.g. after corrections) changed history.
type Diff struct {
	AddedMatches   []Match
	RemovedMatches []Match
	NewFP          []string
	ResolvedFP     []string
	NewFN          []string
	ResolvedFN     []string
	NewCensored    []string
}

func matchKey(m Match) string { return m.FaultID + "|" + m.AlertID }

func idSet(us []Unmatched) map[string]bool {
	s := make(map[string]bool, len(us))
	for _, u := range us {
		s[u.ID] = true
	}
	return s
}

// DiffReports compares two revisions of a report.
func DiffReports(prev, cur Report) Diff {
	var d Diff
	prevM := make(map[string]Match, len(prev.Matches))
	for _, m := range prev.Matches {
		prevM[matchKey(m)] = m
	}
	curM := make(map[string]Match, len(cur.Matches))
	for _, m := range cur.Matches {
		curM[matchKey(m)] = m
		if _, ok := prevM[matchKey(m)]; !ok {
			d.AddedMatches = append(d.AddedMatches, m)
		}
	}
	for _, m := range prev.Matches {
		if _, ok := curM[matchKey(m)]; !ok {
			d.RemovedMatches = append(d.RemovedMatches, m)
		}
	}
	prevFP, curFP := idSet(prev.FalsePositives), idSet(cur.FalsePositives)
	for _, u := range cur.FalsePositives {
		if !prevFP[u.ID] {
			d.NewFP = append(d.NewFP, u.ID)
		}
	}
	for _, u := range prev.FalsePositives {
		if !curFP[u.ID] {
			d.ResolvedFP = append(d.ResolvedFP, u.ID)
		}
	}
	prevFN, curFN := idSet(prev.FalseNegatives), idSet(cur.FalseNegatives)
	for _, u := range cur.FalseNegatives {
		if !prevFN[u.ID] {
			d.NewFN = append(d.NewFN, u.ID)
		}
	}
	for _, u := range prev.FalseNegatives {
		if !curFN[u.ID] {
			d.ResolvedFN = append(d.ResolvedFN, u.ID)
		}
	}
	prevC := make(map[string]bool, len(prev.Censored))
	for _, id := range prev.Censored {
		prevC[id] = true
	}
	for _, id := range cur.Censored {
		if !prevC[id] {
			d.NewCensored = append(d.NewCensored, id)
		}
	}
	return d
}

// SweepRow holds headline metrics for one clock offset.
type SweepRow struct {
	Offset      time.Duration
	Detected    int
	FP          int
	FN          int
	Censored    int
	MeanLatency time.Duration
	HasMatches  bool
}

// SweepResult is the outcome of sweeping the clock offset interval.
type SweepResult struct {
	Rows []SweepRow
}

// SweepScenario replays the scenario for each offset in [min, max].
func SweepScenario(sc Scenario, min, max, step time.Duration) SweepResult {
	var res SweepResult
	for off := min; off <= max; off += step {
		r := BuildReport(sc, off)
		res.Rows = append(res.Rows, SweepRow{
			Offset:      off,
			Detected:    r.Detected(),
			FP:          len(r.FalsePositives),
			FN:          len(r.FalseNegatives),
			Censored:    len(r.Censored),
			MeanLatency: r.Latency.Mean(),
			HasMatches:  r.Latency.Count > 0,
		})
	}
	return res
}

// IntRange is an inclusive integer range.
type IntRange struct{ Min, Max int }

// DurRange is an inclusive duration range; Valid is false when no offset
// produced any match.
type DurRange struct {
	Min, Max time.Duration
	Valid    bool
}

func (s SweepResult) intRange(get func(SweepRow) int) IntRange {
	r := IntRange{}
	for i, row := range s.Rows {
		v := get(row)
		if i == 0 || v < r.Min {
			r.Min = v
		}
		if i == 0 || v > r.Max {
			r.Max = v
		}
	}
	return r
}

func (s SweepResult) DetectedRange() IntRange {
	return s.intRange(func(r SweepRow) int { return r.Detected })
}
func (s SweepResult) FPRange() IntRange { return s.intRange(func(r SweepRow) int { return r.FP }) }
func (s SweepResult) FNRange() IntRange { return s.intRange(func(r SweepRow) int { return r.FN }) }

func (s SweepResult) MeanLatencyRange() DurRange {
	var r DurRange
	for _, row := range s.Rows {
		if !row.HasMatches {
			continue
		}
		if !r.Valid || row.MeanLatency < r.Min {
			r.Min = row.MeanLatency
		}
		if !r.Valid || row.MeanLatency > r.Max {
			r.Max = row.MeanLatency
		}
		r.Valid = true
	}
	return r
}
