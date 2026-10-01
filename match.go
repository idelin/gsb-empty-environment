package main

import (
	"math"
	"time"
)

const (
	// dummyCost is the cost of leaving a fault unmatched; any feasible
	// real pairing is cheaper, so the solver maximizes matched pairs first.
	dummyCost = 1e9
	// infCost marks infeasible fault/alert pairs.
	infCost = 1e15
)

// Pair is a one-to-one match between a fault and an alert.
type Pair struct {
	FaultID string
	AlertID string
	Latency time.Duration // alert time minus fault start; negative means lead (early)
}

type UnmatchedAlert struct {
	Alert  Alert
	Reason string
}

type UnmatchedFault struct {
	Fault  Fault
	Reason string
}

// MatchResult is the outcome of the global minimum-cost one-to-one matching.
type MatchResult struct {
	Pairs           []Pair
	UnmatchedAlerts []UnmatchedAlert // false positives
	MissedFaults    []UnmatchedFault // false negatives
	CensoredFaults  []UnmatchedFault // right-censored, reported separately
	MatchedCount    int
	TotalCost       float64
}

// feasible reports whether alert a may be matched to fault f under cfg:
// same entity, same symptom, and alert time inside
// [start-MaxLead, end+MaxLag] (open faults use ObsEnd as effective end).
func feasible(f Fault, a Alert, cfg Config) bool {
	if f.Entity != a.Entity || f.Symptom != a.Symptom {
		return false
	}
	end := cfg.ObsEnd
	if f.End != nil {
		end = *f.End
	}
	if a.Time.Before(f.Start.Add(-time.Duration(cfg.MaxLead))) {
		return false
	}
	if a.Time.After(end.Add(time.Duration(cfg.MaxLag))) {
		return false
	}
	if cfg.EarlyAsFP && a.Time.Before(f.Start) {
		return false
	}
	return true
}

// pairCost is the absolute detection offset in seconds (latency or lead).
func pairCost(f Fault, a Alert) float64 {
	return math.Abs(a.Time.Sub(f.Start).Seconds())
}

// Match computes the global minimum-cost one-to-one matching between faults
// and alerts. It is not greedy: the Hungarian algorithm optimizes the whole
// assignment, so one alert can never be credited to two faults and vice versa.
func Match(faults []Fault, alerts []Alert, cfg Config) MatchResult {
	n := len(faults)
	m := len(alerts)
	cols := m + n // one dummy column per fault = "leave unmatched"
	cost := make([][]float64, n)
	for i := range cost {
		cost[i] = make([]float64, cols)
		for j := 0; j < m; j++ {
			if feasible(faults[i], alerts[j], cfg) {
				cost[i][j] = pairCost(faults[i], alerts[j])
			} else {
				cost[i][j] = infCost
			}
		}
		for j := m; j < cols; j++ {
			cost[i][j] = dummyCost
		}
	}
	assign := hungarian(cost)

	matchedAlert := make([]bool, m)
	matchedFault := make([]bool, n)
	var res MatchResult
	for i, j := range assign {
		if j < 0 || j >= m || cost[i][j] >= dummyCost {
			continue
		}
		matchedFault[i] = true
		matchedAlert[j] = true
		res.Pairs = append(res.Pairs, Pair{
			FaultID: faults[i].ID,
			AlertID: alerts[j].ID,
			Latency: alerts[j].Time.Sub(faults[i].Start),
		})
		res.TotalCost += cost[i][j]
	}
	res.MatchedCount = len(res.Pairs)

	for j, a := range alerts {
		if !matchedAlert[j] {
			res.UnmatchedAlerts = append(res.UnmatchedAlerts, UnmatchedAlert{a, alertReason(a, faults, cfg)})
		}
	}
	for i, f := range faults {
		if matchedFault[i] {
			continue
		}
		if f.End == nil || f.End.After(cfg.ObsEnd) {
			res.CensoredFaults = append(res.CensoredFaults, UnmatchedFault{f, "right-censored: fault still open at observation end"})
		} else {
			res.MissedFaults = append(res.MissedFaults, UnmatchedFault{f, "missed: no alert matched within window"})
		}
	}
	return res
}

// alertReason explains why an alert ended up unmatched (a false positive).
func alertReason(a Alert, faults []Fault, cfg Config) string {
	compatible := false
	inWindow := false
	for _, f := range faults {
		if f.Entity == a.Entity && f.Symptom == a.Symptom {
			compatible = true
			if feasible(f, a, cfg) {
				inWindow = true
			}
		}
	}
	switch {
	case !compatible:
		return "false positive: no fault with matching entity/symptom"
	case !inWindow:
		return "false positive: outside fault time window"
	default:
		return "false positive: duplicate symptom, compatible fault already matched"
	}
}

// hungarian solves the rectangular assignment problem (rows <= columns) in
// O(n^2 * cols). It returns for each row the matched column index, or -1.
func hungarian(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	m := len(cost[0])
	u := make([]float64, n+1)
	v := make([]float64, m+1)
	p := make([]int, m+1)
	way := make([]int, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, m+1)
		used := make([]bool, m+1)
		for j := range minv {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= m; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	match := make([]int, n)
	for i := range match {
		match[i] = -1
	}
	for j := 1; j <= m; j++ {
		if p[j] > 0 {
			match[p[j]-1] = j - 1
		}
	}
	return match
}
