// matchsim replays a local telemetry fixture, aligns known fault events
// with observed alerts under a global one-to-one min-cost matching, and
// reports detection latency, false positives, false negatives and
// right-censored faults. It can sweep a clock-offset interval for metric
// ranges, apply late corrections, and brute-force verify optimality on
// small instances.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"matchsim"
)

func main() {
	fixture := flag.String("fixture", "", "path to scenario JSON fixture")
	sweepMin := flag.Duration("sweep-min", 0, "clock offset sweep lower bound (e.g. -10s)")
	sweepMax := flag.Duration("sweep-max", 0, "clock offset sweep upper bound (e.g. 10s)")
	sweepStep := flag.Duration("sweep-step", time.Second, "clock offset sweep step")
	verify := flag.Bool("verify", false, "brute-force verify matching optimality (small instances)")
	noCorrections := flag.Bool("no-corrections", false, "ignore corrections in the fixture")
	flag.Parse()

	if *fixture == "" {
		fmt.Fprintln(os.Stderr, "usage: matchsim -fixture scenario.json [-sweep-min -10s -sweep-max 10s] [-verify]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*fixture)
	if err != nil {
		fatal(err)
	}
	var sc matchsim.Scenario
	if err := json.Unmarshal(data, &sc); err != nil {
		fatal(err)
	}

	fmt.Printf("scenario: %d faults, %d alerts, observation ends %s\n",
		len(sc.Faults), len(sc.Alerts), sc.ObservationEnd.Format(time.RFC3339))
	fmt.Printf("rules: maxLead=%s maxLag=%s earlyAlertAsFP=%v\n\n",
		sc.Config.MaxLead, sc.Config.MaxLag, sc.Config.EarlyAlertAsFP)

	base := matchsim.BuildReport(sc, 0)
	printReport("BASELINE (offset 0)", base)

	if len(sc.Corrections) > 0 && !*noCorrections {
		corrected, notes := matchsim.ApplyCorrections(sc.Alerts, sc.Corrections)
		fmt.Println("== LATE CORRECTIONS ==")
		for _, n := range notes {
			fmt.Println("  applied:", n)
		}
		sc2 := sc
		sc2.Alerts = corrected
		cur := matchsim.BuildReport(sc2, 0)
		printDiff(matchsim.DiffReports(base, cur))
		printReport("AFTER CORRECTIONS (offset 0)", cur)
	}

	if *sweepMax > *sweepMin {
		res := matchsim.SweepScenario(sc, *sweepMin, *sweepMax, *sweepStep)
		printSweep(res)
	}

	if *verify {
		cost := matchsim.CostMatrix(sc.Config, sc.Faults, sc.Alerts)
		nf, na := len(sc.Faults), len(sc.Alerts)
		if nf > 12 || na > 12 {
			fmt.Printf("== VERIFY ==\nskipped: %dx%d too large for brute force\n", nf, na)
		} else {
			pairs := matchsim.MinCostMatching(cost)
			gotCost := matchsim.MatchingCost(cost, pairs)
			bestN, bestC := matchsim.EnumerateBest(cost)
			ok := len(pairs) == bestN && gotCost == bestC
			fmt.Printf("== VERIFY ==\nsolver: %d matches, cost %d; exhaustive: %d matches, cost %d; optimal=%v\n",
				len(pairs), gotCost, bestN, bestC, ok)
			if !ok {
				os.Exit(1)
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "matchsim:", err)
	os.Exit(1)
}

func fmtLat(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("lead %s", -d)
	}
	return fmt.Sprintf("lag %s", d)
}

func printReport(title string, r matchsim.Report) {
	fmt.Printf("== %s ==\n", title)
	fmt.Println("matches (one-to-one, globally min-cost):")
	if len(r.Matches) == 0 {
		fmt.Println("  (none)")
	}
	for _, m := range r.Matches {
		fmt.Printf("  %-6s <-> %-6s class=%-8s %s\n", m.FaultID, m.AlertID, m.Class, fmtLat(m.Latency))
	}
	fmt.Println("false positives (unmatched alerts):")
	if len(r.FalsePositives) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range r.FalsePositives {
		fmt.Printf("  %-6s %s\n", u.ID, u.Reason)
	}
	fmt.Println("false negatives (unmatched faults):")
	if len(r.FalseNegatives) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range r.FalseNegatives {
		fmt.Printf("  %-6s %s\n", u.ID, u.Reason)
	}
	fmt.Println("right-censored faults (open at observation end, not counted as FN):")
	if len(r.Censored) == 0 {
		fmt.Println("  (none)")
	}
	for _, id := range r.Censored {
		fmt.Printf("  %s\n", id)
	}
	fmt.Printf("metrics: detected=%d/%d fp=%d fn=%d censored=%d",
		r.Detected(), r.TotalFaults, len(r.FalsePositives), len(r.FalseNegatives), len(r.Censored))
	if r.Latency.Count > 0 {
		fmt.Printf(" latency mean=%s min=%s max=%s", r.Latency.Mean(), r.Latency.Min, r.Latency.Max)
	}
	fmt.Println()
	classes := make([]string, 0, len(r.ClassStats))
	for c := range r.ClassStats {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		s := r.ClassStats[c]
		fmt.Printf("  class %-8s n=%d mean=%s min=%s max=%s\n", c, s.Count, s.Mean(), s.Min, s.Max)
	}
	fmt.Println()
}

func printDiff(d matchsim.Diff) {
	fmt.Println("history diff vs baseline:")
	for _, m := range d.AddedMatches {
		fmt.Printf("  + match %s <-> %s (%s)\n", m.FaultID, m.AlertID, fmtLat(m.Latency))
	}
	for _, m := range d.RemovedMatches {
		fmt.Printf("  - match %s <-> %s (%s)\n", m.FaultID, m.AlertID, fmtLat(m.Latency))
	}
	for _, id := range d.NewFP {
		fmt.Printf("  + fp %s\n", id)
	}
	for _, id := range d.ResolvedFP {
		fmt.Printf("  - fp %s\n", id)
	}
	for _, id := range d.NewFN {
		fmt.Printf("  + fn %s\n", id)
	}
	for _, id := range d.ResolvedFN {
		fmt.Printf("  - fn %s\n", id)
	}
	for _, id := range d.NewCensored {
		fmt.Printf("  + censored %s\n", id)
	}
	fmt.Println()
}

func printSweep(res matchsim.SweepResult) {
	fmt.Println("== CLOCK OFFSET SWEEP ==")
	fmt.Printf("  %-8s %-9s %-3s %-3s %-9s %-12s\n", "offset", "detected", "fp", "fn", "censored", "mean-latency")
	for _, row := range res.Rows {
		lat := "-"
		if row.HasMatches {
			lat = row.MeanLatency.String()
		}
		fmt.Printf("  %-8s %-9d %-3d %-3d %-9d %-12s\n",
			row.Offset, row.Detected, row.FP, row.FN, row.Censored, lat)
	}
	det, fp, fn := res.DetectedRange(), res.FPRange(), res.FNRange()
	lat := res.MeanLatencyRange()
	fmt.Println("metric ranges over the offset interval:")
	fmt.Printf("  detected: [%d, %d]\n", det.Min, det.Max)
	fmt.Printf("  fp:       [%d, %d]\n", fp.Min, fp.Max)
	fmt.Printf("  fn:       [%d, %d]\n", fn.Min, fn.Max)
	if lat.Valid {
		fmt.Printf("  mean latency: [%s, %s]\n", lat.Min, lat.Max)
	} else {
		fmt.Println("  mean latency: n/a (no matches in interval)")
	}
	fmt.Println()
}
