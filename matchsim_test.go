package matchsim

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func endPtr(sec int) *time.Time {
	v := at(sec)
	return &v
}

func testConfig() Config {
	return Config{
		MaxLead:        Duration{30 * time.Second},
		MaxLag:         Duration{300 * time.Second},
		SymptomClasses: map[string]string{"disk_latency": "disk", "io_wait": "disk"},
		EntityGroups:   map[string]string{"cache-1": "cache", "cache-2": "cache"},
	}
}

func matchedFaults(r Report) map[string]string {
	m := map[string]string{}
	for _, mt := range r.Matches {
		m[mt.FaultID] = mt.AlertID
	}
	return m
}

func fpIDs(r Report) map[string]bool {
	s := map[string]bool{}
	for _, u := range r.FalsePositives {
		s[u.ID] = true
	}
	return s
}

func fnIDs(r Report) map[string]bool {
	s := map[string]bool{}
	for _, u := range r.FalseNegatives {
		s[u.ID] = true
	}
	return s
}

// One alert must not be credited for two faults, and one fault must not
// absorb two alerts.
func TestOneToOneMatching(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)},
			{ID: "f2", Entity: "web-1", Symptom: "cpu", Start: at(150), End: endPtr(250)},
		},
		Alerts: []Alert{
			{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(120)},
			{ID: "a2", Entity: "web-1", Symptom: "cpu", Time: at(130)},
			{ID: "a3", Entity: "web-1", Symptom: "cpu", Time: at(160)},
		},
	}
	r := BuildReport(sc, 0)
	if r.Detected() != 2 {
		t.Fatalf("detected = %d, want 2", r.Detected())
	}
	seen := map[string]int{}
	for _, m := range r.Matches {
		seen[m.AlertID]++
		if seen[m.AlertID] > 1 {
			t.Fatalf("alert %s matched more than one fault", m.AlertID)
		}
	}
	if len(r.FalsePositives) != 1 {
		t.Fatalf("fp = %d, want 1 (the leftover duplicate alert)", len(r.FalsePositives))
	}
}

// Global min-cost matching must beat per-alert greedy order: a greedy pass
// that hands a1 to f2 would leave f1 dark; the optimum is unique here.
func TestGlobalBeatsGreedy(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)},
			{ID: "f2", Entity: "web-1", Symptom: "cpu", Start: at(120), End: endPtr(220)},
		},
		Alerts: []Alert{
			{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(80)},  // 20s lead of f1; 40s lead of f2 > MaxLead, so f1-only
			{ID: "a2", Entity: "web-1", Symptom: "cpu", Time: at(125)}, // feasible for both
		},
	}
	r := BuildReport(sc, 0)
	m := matchedFaults(r)
	if m["f1"] != "a1" || m["f2"] != "a2" {
		t.Fatalf("matches = %v, want f1<->a1 and f2<->a2", m)
	}
	if r.Detected() != 2 {
		t.Fatalf("detected = %d, want 2", r.Detected())
	}
}

// On small instances the solver must agree with exhaustive enumeration of
// every one-to-one matching.
func TestRandomSmallMatchesEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	entities := []string{"web-1", "db-1", "cache-1", "cache-2"}
	symptoms := []string{"cpu", "disk_latency", "io_wait", "mem"}
	for trial := 0; trial < 300; trial++ {
		cfg := testConfig()
		nf, na := 1+rng.Intn(5), 1+rng.Intn(5)
		sc := Scenario{ObservationEnd: at(1000), Config: cfg}
		for i := 0; i < nf; i++ {
			sc.Faults = append(sc.Faults, Fault{
				ID:      fmt.Sprintf("f%d", i),
				Entity:  entities[rng.Intn(len(entities))],
				Symptom: symptoms[rng.Intn(len(symptoms))],
				Start:   at(rng.Intn(600)),
				End:     endPtr(900),
			})
		}
		for j := 0; j < na; j++ {
			sc.Alerts = append(sc.Alerts, Alert{
				ID:      fmt.Sprintf("a%d", j),
				Entity:  entities[rng.Intn(len(entities))],
				Symptom: symptoms[rng.Intn(len(symptoms))],
				Time:    at(rng.Intn(600)),
			})
		}
		cost := CostMatrix(cfg, sc.Faults, sc.Alerts)
		pairs := MinCostMatching(cost)
		gotCost := MatchingCost(cost, pairs)
		bestN, bestC := EnumerateBest(cost)
		if len(pairs) != bestN || gotCost != bestC {
			t.Fatalf("trial %d: solver=(%d,%d) exhaustive=(%d,%d)", trial, len(pairs), gotCost, bestN, bestC)
		}
	}
}

// Early alerts: within MaxLead they match and report a lead; with
// EarlyAlertAsFP they are false positives instead.
func TestEarlyAlertRules(t *testing.T) {
	base := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults:         []Fault{{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)}},
		Alerts:         []Alert{{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(80)}},
	}
	r := BuildReport(base, 0)
	if r.Detected() != 1 || r.Matches[0].Latency != -20*time.Second {
		t.Fatalf("lead match = %+v, want latency -20s", r.Matches)
	}
	base.Config.EarlyAlertAsFP = true
	r = BuildReport(base, 0)
	if r.Detected() != 0 || !fpIDs(r)["a1"] || !fnIDs(r)["f1"] {
		t.Fatalf("early-as-fp: detected=%d fp=%v fn=%v", r.Detected(), r.FalsePositives, r.FalseNegatives)
	}
	// Beyond MaxLead the alert is out of window even in lead mode.
	base.Config.EarlyAlertAsFP = false
	base.Alerts[0].Time = at(60) // 40s lead > 30s MaxLead
	r = BuildReport(base, 0)
	if r.Detected() != 0 || !fpIDs(r)["a1"] {
		t.Fatalf("over-lead: detected=%d fp=%v", r.Detected(), r.FalsePositives)
	}
}

// Entity groups and symptom classes make alerts compatible across renamed
// entities and duplicate-worded symptoms.
func TestCompatibilityRules(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "cache-1", Symptom: "mem", Start: at(100), End: endPtr(200)},
			{ID: "f2", Entity: "db-1", Symptom: "disk_latency", Start: at(300), End: endPtr(400)},
		},
		Alerts: []Alert{
			{ID: "a1", Entity: "cache-2", Symptom: "mem", Time: at(110)},         // same entity group
			{ID: "a2", Entity: "db-1", Symptom: "io_wait", Time: at(310)},        // same symptom class
			{ID: "a3", Entity: "web-9", Symptom: "mem", Time: at(120)},           // no compatible fault
			{ID: "a4", Entity: "db-1", Symptom: "disk_latency", Time: at(10000)}, // outside window
		},
	}
	r := BuildReport(sc, 0)
	m := matchedFaults(r)
	if m["f1"] != "a1" || m["f2"] != "a2" {
		t.Fatalf("matches = %v", m)
	}
	if !fpIDs(r)["a3"] || !fpIDs(r)["a4"] {
		t.Fatalf("fp = %v, want a3 and a4", r.FalsePositives)
	}
}

// Faults still open at observation end are right-censored and listed
// separately, never counted as false negatives.
func TestRightCensoring(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(500),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100)},                   // open
			{ID: "f2", Entity: "web-1", Symptom: "cpu", Start: at(200), End: endPtr(600)}, // ends after window
			{ID: "f3", Entity: "db-1", Symptom: "cpu", Start: at(300), End: endPtr(400)},
		},
	}
	r := BuildReport(sc, 0)
	if len(r.Censored) != 2 {
		t.Fatalf("censored = %v, want f1 and f2", r.Censored)
	}
	if len(r.FalseNegatives) != 1 || r.FalseNegatives[0].ID != "f3" {
		t.Fatalf("fn = %v, want only f3", r.FalseNegatives)
	}
}

// Late corrections must update historical matches and per-class stats.
func TestCorrectionsUpdateHistory(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)},
			{ID: "f2", Entity: "web-1", Symptom: "cpu", Start: at(300), End: endPtr(400)},
		},
		Alerts: []Alert{
			{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(110)},
			{ID: "a2", Entity: "web-1", Symptom: "cpu", Time: at(120)}, // wrong clock, later fixed
		},
		Corrections: []Correction{{AlertID: "a2", NewTime: at(310)}},
	}
	before := BuildReport(sc, 0)
	if before.Detected() != 1 || !fpIDs(before)["a2"] || !fnIDs(before)["f2"] {
		t.Fatalf("before: detected=%d fp=%v fn=%v", before.Detected(), before.FalsePositives, before.FalseNegatives)
	}
	corrected, notes := ApplyCorrections(sc.Alerts, sc.Corrections)
	if len(notes) != 1 {
		t.Fatalf("notes = %v", notes)
	}
	sc.Alerts = corrected
	after := BuildReport(sc, 0)
	if after.Detected() != 2 {
		t.Fatalf("after: detected=%d, want 2", after.Detected())
	}
	d := DiffReports(before, after)
	if len(d.AddedMatches) != 1 || d.AddedMatches[0].FaultID != "f2" || d.AddedMatches[0].AlertID != "a2" {
		t.Fatalf("added = %+v", d.AddedMatches)
	}
	if len(d.ResolvedFP) != 1 || d.ResolvedFP[0] != "a2" || len(d.ResolvedFN) != 1 || d.ResolvedFN[0] != "f2" {
		t.Fatalf("diff = %+v", d)
	}
	if after.ClassStats["cpu"].Count != 2 {
		t.Fatalf("class stats = %+v", after.ClassStats["cpu"])
	}
}

// Sweeping the clock offset interval yields metric ranges.
func TestSweepRanges(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults:         []Fault{{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)}},
		Alerts:         []Alert{{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(150)}},
	}
	res := SweepScenario(sc, -10*time.Second, 10*time.Second, 5*time.Second)
	if len(res.Rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(res.Rows))
	}
	det := res.DetectedRange()
	if det.Min != 1 || det.Max != 1 {
		t.Fatalf("detected range = %+v, want [1,1]", det)
	}
	lat := res.MeanLatencyRange()
	if !lat.Valid || lat.Min != 40*time.Second || lat.Max != 60*time.Second {
		t.Fatalf("latency range = %+v, want [40s,60s]", lat)
	}
	// A large negative shift pushes the alert out of the lag window.
	res = SweepScenario(sc, -100*time.Second, 0, 50*time.Second)
	det = res.DetectedRange()
	if det.Min != 0 || det.Max != 1 {
		t.Fatalf("detected range = %+v, want [0,1]", det)
	}
	fp := res.FPRange()
	if fp.Min != 0 || fp.Max != 1 {
		t.Fatalf("fp range = %+v, want [0,1]", fp)
	}
}

// Unmatched entries carry human-readable reasons.
func TestUnmatchedReasons(t *testing.T) {
	sc := Scenario{
		ObservationEnd: at(1000),
		Config:         testConfig(),
		Faults: []Fault{
			{ID: "f1", Entity: "web-1", Symptom: "cpu", Start: at(100), End: endPtr(200)},
			{ID: "f2", Entity: "db-1", Symptom: "mem", Start: at(100), End: endPtr(200)},
		},
		Alerts: []Alert{
			{ID: "a1", Entity: "web-1", Symptom: "cpu", Time: at(110)},
			{ID: "a2", Entity: "web-1", Symptom: "cpu", Time: at(120)}, // loses contention
			{ID: "a3", Entity: "db-1", Symptom: "mem", Time: at(900)},  // out of window
		},
	}
	r := BuildReport(sc, 0)
	reasons := map[string]string{}
	for _, u := range r.FalsePositives {
		reasons[u.ID] = u.Reason
	}
	if reasons["a2"] == "" || reasons["a3"] == "" {
		t.Fatalf("missing reasons: %v", reasons)
	}
	if reasons["a2"] == reasons["a3"] {
		t.Fatalf("contention and out-of-window should differ: %v", reasons)
	}
	if len(r.FalseNegatives) != 1 || r.FalseNegatives[0].ID != "f2" {
		t.Fatalf("fn = %v", r.FalseNegatives)
	}
}
