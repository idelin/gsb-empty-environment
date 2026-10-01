package main

import (
	"math/rand"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		MaxLead:   Duration(20 * time.Minute),
		MaxLag:    Duration(10 * time.Minute),
		EarlyAsFP: false,
		ObsEnd:    base.Add(90 * time.Minute),
	}
}

func mkFault(id, entity, symptom string, start, end time.Time) Fault {
	return Fault{ID: id, Entity: entity, Symptom: symptom, Start: start, End: &end}
}

func mkAlert(id, entity, symptom string, t time.Time) Alert {
	return Alert{ID: id, Entity: entity, Symptom: symptom, Time: t}
}

// TestHungarianMatchesEnumeration verifies global optimality of the Hungarian
// matching against exhaustive enumeration on many random small instances.
func TestHungarianMatchesEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	entities := []string{"host-a", "host-b", "host-c"}
	symptoms := []string{"cpu_spike", "disk_full"}
	for trial := 0; trial < 300; trial++ {
		cfg := testConfig()
		nf := rng.Intn(6)
		na := rng.Intn(7)
		faults := make([]Fault, nf)
		for i := range faults {
			start := base.Add(time.Duration(rng.Intn(60)) * time.Minute)
			end := start.Add(time.Duration(5+rng.Intn(20)) * time.Minute)
			faults[i] = mkFault("f"+itoa(i), entities[rng.Intn(3)], symptoms[rng.Intn(2)], start, end)
		}
		alerts := make([]Alert, na)
		for j := range alerts {
			t := base.Add(time.Duration(rng.Intn(80)-10) * time.Minute)
			alerts[j] = mkAlert("a"+itoa(j), entities[rng.Intn(3)], symptoms[rng.Intn(2)], t)
		}
		res := Match(faults, alerts, cfg)
		bestCount, bestCost := enumerateOptimal(faults, alerts, cfg)
		if res.MatchedCount != bestCount {
			t.Fatalf("trial %d: matched count %d != optimal %d", trial, res.MatchedCount, bestCount)
		}
		if res.TotalCost != bestCost {
			t.Fatalf("trial %d: cost %v != optimal %v", trial, res.TotalCost, bestCost)
		}
		// one-to-one: no alert or fault credited twice
		seenA := map[string]int{}
		seenF := map[string]int{}
		for _, p := range res.Pairs {
			seenA[p.AlertID]++
			seenF[p.FaultID]++
			if seenA[p.AlertID] > 1 || seenF[p.FaultID] > 1 {
				t.Fatalf("trial %d: pair %v breaks one-to-one rule", trial, p)
			}
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestDuplicateSymptomCountsOnce: one fault, three identical alerts ->
// exactly one match, the other two are false positives, never double-counted.
func TestDuplicateSymptomCountsOnce(t *testing.T) {
	cfg := testConfig()
	faults := []Fault{mkFault("f1", "h", "cpu", base, base.Add(30*time.Minute))}
	alerts := []Alert{
		mkAlert("a1", "h", "cpu", base.Add(3*time.Minute)),
		mkAlert("a2", "h", "cpu", base.Add(5*time.Minute)),
		mkAlert("a3", "h", "cpu", base.Add(9*time.Minute)),
	}
	res := Match(faults, alerts, cfg)
	if res.MatchedCount != 1 {
		t.Fatalf("want 1 match, got %d", res.MatchedCount)
	}
	if len(res.UnmatchedAlerts) != 2 {
		t.Fatalf("want 2 false positives, got %d", len(res.UnmatchedAlerts))
	}
	// the closest alert (a1, 3m) must win the single slot
	if res.Pairs[0].AlertID != "a1" || res.Pairs[0].Latency != 3*time.Minute {
		t.Fatalf("want a1 with 3m latency, got %+v", res.Pairs[0])
	}
	for _, u := range res.UnmatchedAlerts {
		if u.Reason != "false positive: duplicate symptom, compatible fault already matched" {
			t.Fatalf("unexpected reason: %s", u.Reason)
		}
	}
}

// TestGlobalBeatsGreedy: a greedy per-fault pass would grab the wrong alert;
// the global optimum reassigns so both faults get detected.
func TestGlobalBeatsGreedy(t *testing.T) {
	cfg := testConfig()
	f1 := mkFault("f1", "h", "cpu", base, base.Add(10*time.Minute))
	f2 := mkFault("f2", "h", "cpu", base.Add(8*time.Minute), base.Add(20*time.Minute))
	// a1 fits both faults but is far better for f1; a2 only fits f2's window
	// relative to f1's end+lag? construct so a2 infeasible for f1:
	// f1 ends at +10m, lag 10m -> a2 at +21m infeasible for f1, feasible for f2.
	a1 := mkAlert("a1", "h", "cpu", base.Add(1*time.Minute))
	a2 := mkAlert("a2", "h", "cpu", base.Add(21*time.Minute))
	res := Match([]Fault{f1, f2}, []Alert{a1, a2}, cfg)
	if res.MatchedCount != 2 {
		t.Fatalf("want 2 matches, got %d (%+v)", res.MatchedCount, res)
	}
	got := map[string]string{}
	for _, p := range res.Pairs {
		got[p.FaultID] = p.AlertID
	}
	if got["f1"] != "a1" || got["f2"] != "a2" {
		t.Fatalf("unexpected assignment: %v", got)
	}
}

// TestEarlyAlertRules: early alerts count as lead when allowed, and become
// false positives when EarlyAsFP is set.
func TestEarlyAlertRules(t *testing.T) {
	cfg := testConfig()
	faults := []Fault{mkFault("f1", "h", "cpu", base, base.Add(30*time.Minute))}
	alerts := []Alert{mkAlert("a1", "h", "cpu", base.Add(-15*time.Minute))}

	res := Match(faults, alerts, cfg)
	if res.MatchedCount != 1 || res.Pairs[0].Latency != -15*time.Minute {
		t.Fatalf("want lead match of -15m, got %+v", res.Pairs)
	}
	s := computeStats(res)
	if s.Leads != 1 || s.MeanLead != 15*time.Minute || s.Detections != 0 {
		t.Fatalf("bad lead stats: %+v", s)
	}

	cfg.EarlyAsFP = true
	res = Match(faults, alerts, cfg)
	if res.MatchedCount != 0 || len(res.UnmatchedAlerts) != 1 || len(res.MissedFaults) != 1 {
		t.Fatalf("early alert should be FP and fault missed: %+v", res)
	}
}

// TestRightCensored: a fault still open at observation end is listed
// separately, not counted as a false negative.
func TestRightCensored(t *testing.T) {
	cfg := testConfig()
	open := Fault{ID: "f1", Entity: "h", Symptom: "cpu", Start: base.Add(40 * time.Minute)}
	res := Match([]Fault{open}, nil, cfg)
	if len(res.CensoredFaults) != 1 || len(res.MissedFaults) != 0 {
		t.Fatalf("want 1 censored, 0 missed: %+v", res)
	}
	s := computeStats(res)
	if s.FalseNegatives != 0 || s.RightCensored != 1 {
		t.Fatalf("bad stats: %+v", s)
	}
}

// TestEntityAndSymptomCompatibility: alerts on other entities or with other
// symptoms are false positives, and the fault is missed.
func TestEntityAndSymptomCompatibility(t *testing.T) {
	cfg := testConfig()
	faults := []Fault{mkFault("f1", "h1", "cpu", base, base.Add(30*time.Minute))}
	alerts := []Alert{
		mkAlert("a1", "h2", "cpu", base.Add(5*time.Minute)), // wrong entity
		mkAlert("a2", "h1", "mem", base.Add(5*time.Minute)), // wrong symptom
	}
	res := Match(faults, alerts, cfg)
	if res.MatchedCount != 0 || len(res.UnmatchedAlerts) != 2 || len(res.MissedFaults) != 1 {
		t.Fatalf("incompatible alerts must not match: %+v", res)
	}
	for _, u := range res.UnmatchedAlerts {
		if u.Reason != "false positive: no fault with matching entity/symptom" {
			t.Fatalf("unexpected reason: %s", u.Reason)
		}
	}
}
