package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"
)

func usage() {
	fmt.Fprintf(os.Stderr, `faultalign - align simulated fault events with observed alerts

usage:
  faultalign run    -fixture F.json           replay fixture, match, print metrics + corrections
  faultalign sweep  -fixture F.json -offsets lo:hi:step
                                              shift alert clock over a range, print metric ranges
  faultalign verify -fixture F.json           brute-force all matchings, check Hungarian optimality
`)
	os.Exit(2)
}

func loadFixture(path string) (*Fixture, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fx Fixture
	if err := json.Unmarshal(b, &fx); err != nil {
		return nil, err
	}
	return &fx, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fixturePath := fs.String("fixture", "testdata/fixture.json", "path to replayable fixture JSON")
	offsets := fs.String("offsets", "-5m:5m:1m", "clock offset sweep lo:hi:step (durations)")
	fs.Parse(os.Args[2:])

	fx, err := loadFixture(*fixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load fixture:", err)
		os.Exit(1)
	}

	switch cmd {
	case "run":
		runReport(fx)
	case "sweep":
		lo, hi, step, err := parseSweep(*offsets)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad -offsets:", err)
			os.Exit(1)
		}
		runSweep(fx, lo, hi, step)
	case "verify":
		runVerify(fx)
	default:
		usage()
	}
}

func sortedPairs(pairs []Pair) []Pair {
	out := make([]Pair, len(pairs))
	copy(out, pairs)
	sort.Slice(out, func(i, j int) bool { return out[i].FaultID < out[j].FaultID })
	return out
}

func printResult(res MatchResult) {
	fmt.Println("== matched pairs (fault <- alert, latency) ==")
	if len(res.Pairs) == 0 {
		fmt.Println("  (none)")
	}
	for _, p := range sortedPairs(res.Pairs) {
		note := ""
		if p.Latency < 0 {
			note = " (early, lead " + (-p.Latency).String() + ")"
		}
		fmt.Printf("  %-6s <- %-6s latency %s%s\n", p.FaultID, p.AlertID, p.Latency, note)
	}
	fmt.Println("== unmatched alerts (false positives) ==")
	if len(res.UnmatchedAlerts) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range res.UnmatchedAlerts {
		fmt.Printf("  %-6s %s\n", u.Alert.ID, u.Reason)
	}
	fmt.Println("== unmatched faults (false negatives) ==")
	if len(res.MissedFaults) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range res.MissedFaults {
		fmt.Printf("  %-6s %s\n", u.Fault.ID, u.Reason)
	}
	fmt.Println("== right-censored faults (open at observation end, not counted as missed) ==")
	if len(res.CensoredFaults) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range res.CensoredFaults {
		fmt.Printf("  %-6s %s\n", u.Fault.ID, u.Reason)
	}
}

func printStats(s Stats) {
	fmt.Println("== stats ==")
	fmt.Printf("  matched=%d  false_positives=%d  false_negatives=%d  right_censored=%d\n",
		s.Matched, s.FalsePositives, s.FalseNegatives, s.RightCensored)
	fmt.Printf("  detections=%d mean_latency=%s max_latency=%s  leads=%d mean_lead=%s\n",
		s.Detections, s.MeanLatency, s.MaxLatency, s.Leads, s.MeanLead)
}

func runReport(fx *Fixture) {
	fmt.Println("# replay fixture: initial matching")
	res := Match(fx.Faults, fx.Alerts, fx.Config)
	printResult(res)
	printStats(computeStats(res))

	if len(fx.Corrections) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("# late corrections: replay and update history")
	hist, err := ApplyCorrections(fx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corrections:", err)
		os.Exit(1)
	}
	for i, h := range hist {
		c := h.Correction
		fmt.Printf("correction %d: %s %s updated\n", i+1, c.Kind, c.ID)
		if len(h.Added) == 0 && len(h.Removed) == 0 {
			fmt.Println("  matches unchanged")
		}
		for _, p := range h.Removed {
			fmt.Printf("  - removed match %s <- %s\n", p.FaultID, p.AlertID)
		}
		for _, p := range h.Added {
			fmt.Printf("  + added match   %s <- %s (latency %s)\n", p.FaultID, p.AlertID, p.Latency)
		}
		printStats(h.Stats)
	}
}

func parseSweep(spec string) (time.Duration, time.Duration, time.Duration, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("want lo:hi:step, got %q", spec)
	}
	var out [3]time.Duration
	for i, p := range parts {
		d, err := time.ParseDuration(p)
		if err != nil {
			return 0, 0, 0, err
		}
		out[i] = d
	}
	if out[2] <= 0 || out[1] < out[0] {
		return 0, 0, 0, fmt.Errorf("need lo <= hi and step > 0")
	}
	return out[0], out[1], out[2], nil
}

func runSweep(fx *Fixture, lo, hi, step time.Duration) {
	fmt.Printf("# clock offset sweep: alert clock shifted over [%s, %s] step %s\n", lo, hi, step)
	fmt.Printf("%-8s %7s %4s %4s %4s %12s %10s\n", "offset", "matched", "fp", "fn", "cens", "mean_latency", "mean_lead")
	var mins, maxs Stats
	var minLat, maxLat time.Duration
	first := true
	for off := lo; off <= hi; off += step {
		shifted := make([]Alert, len(fx.Alerts))
		for i, a := range fx.Alerts {
			a.Time = a.Time.Add(off)
			shifted[i] = a
		}
		s := computeStats(Match(fx.Faults, shifted, fx.Config))
		fmt.Printf("%-8s %7d %4d %4d %4d %12s %10s\n",
			off, s.Matched, s.FalsePositives, s.FalseNegatives, s.RightCensored, s.MeanLatency, s.MeanLead)
		if first {
			mins, maxs = s, s
			minLat, maxLat = s.MeanLatency, s.MeanLatency
			first = false
			continue
		}
		if s.Matched < mins.Matched {
			mins.Matched = s.Matched
		}
		if s.Matched > maxs.Matched {
			maxs.Matched = s.Matched
		}
		if s.FalsePositives < mins.FalsePositives {
			mins.FalsePositives = s.FalsePositives
		}
		if s.FalsePositives > maxs.FalsePositives {
			maxs.FalsePositives = s.FalsePositives
		}
		if s.FalseNegatives < mins.FalseNegatives {
			mins.FalseNegatives = s.FalseNegatives
		}
		if s.FalseNegatives > maxs.FalseNegatives {
			maxs.FalseNegatives = s.FalseNegatives
		}
		if s.MeanLatency < minLat {
			minLat = s.MeanLatency
		}
		if s.MeanLatency > maxLat {
			maxLat = s.MeanLatency
		}
	}
	fmt.Println("== metric ranges over the offset interval ==")
	fmt.Printf("  matched:         [%d, %d]\n", mins.Matched, maxs.Matched)
	fmt.Printf("  false_positives: [%d, %d]\n", mins.FalsePositives, maxs.FalsePositives)
	fmt.Printf("  false_negatives: [%d, %d]\n", mins.FalseNegatives, maxs.FalseNegatives)
	fmt.Printf("  mean_latency:    [%s, %s]\n", minLat, maxLat)
}

func runVerify(fx *Fixture) {
	if len(fx.Faults) > 12 || len(fx.Alerts) > 12 {
		fmt.Println("instance too large for exhaustive enumeration (limit 12+12)")
		os.Exit(1)
	}
	res := Match(fx.Faults, fx.Alerts, fx.Config)
	bestCount, bestCost := enumerateOptimal(fx.Faults, fx.Alerts, fx.Config)
	fmt.Printf("hungarian: matched=%d cost=%.1f\n", res.MatchedCount, res.TotalCost)
	fmt.Printf("enumerate: matched=%d cost=%.1f\n", bestCount, bestCost)
	if res.MatchedCount == bestCount && res.TotalCost == bestCost {
		fmt.Println("OK: Hungarian matching is globally optimal on this instance")
		return
	}
	fmt.Println("MISMATCH: Hungarian result is not optimal")
	os.Exit(1)
}
