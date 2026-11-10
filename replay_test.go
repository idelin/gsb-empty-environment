package main

import "testing"

func TestExtractedWitnessReplaysStandalone(t *testing.T) {
	report := runTest(t, loadTest(t, "testdata/v2_pool_reuse.json"))
	var v2 *Violation
	for i := range report.Violations {
		if report.Violations[i].Kind == ViolationPoolReuse {
			v2 = &report.Violations[i]
		}
	}
	if v2 == nil {
		t.Fatal("missing V2 violation")
	}
	replayReport := runTest(t, v2.SubScenario)
	if len(replayReport.Events) != len(report.Events) {
		t.Fatalf("witness replay event count = %d, want %d", len(replayReport.Events), len(report.Events))
	}
	for i := range report.Events {
		want := report.Events[i]
		got := replayReport.Events[i]
		if got.Type != want.Type || got.At != want.At || got.RequestID != want.RequestID {
			t.Fatalf("witness replay diverges at seq %d: want %+v got %+v", i+1, want, got)
		}
	}
}

func TestValidationChecksEveryResolvedAddress(t *testing.T) {
	sc := Scenario{
		Forbidden: []string{"10.0.0.0/8"},
		Hosts: map[string]Host{
			"dual.internal": {
				Port:  443,
				TTL:   5,
				Steps: [][]string{{"172.16.0.9", "10.0.0.9"}},
			},
		},
		Requests: []RequestSpec{
			{ID: "r", Host: "dual.internal", ValidateAt: 1, DialAt: 1},
		},
	}
	report := runTest(t, sc)
	check := findEvent(report, EvValidate, "r")
	if check == nil || check.Detail["result"] != "BLOCK" {
		t.Fatalf("one forbidden address among several must BLOCK, got %+v", check)
	}
	if findEvent(report, EvDial, "r") != nil {
		t.Fatalf("blocked request must not reach dial")
	}
	if len(report.Violations) != 0 {
		t.Fatalf("a blocked check is not a check-pass violation: %+v", report.Violations)
	}
}

func TestPoolKeyedByHostAndPort(t *testing.T) {
	sc := Scenario{
		Forbidden: []string{"10.0.0.0/8"},
		Hosts: map[string]Host{
			"svc.internal": {
				Port: 8080,
				TTL:  10,
				Steps: [][]string{
					{"172.16.0.7"},
					{"172.16.0.8"},
				},
			},
		},
		Requests: []RequestSpec{
			{ID: "first", Host: "svc.internal", ValidateAt: 0, DialAt: 0, Port: 9000},
			{ID: "second", Host: "svc.internal", ValidateAt: 10, DialAt: 10, Port: 8080},
		},
	}
	report := runTest(t, sc)
	if len(report.Violations) != 0 {
		t.Fatalf("both addresses are safe, got unexpected violations: %+v", report.Violations)
	}
	dials := 0
	poolKeys := map[string]bool{}
	for _, ev := range report.Events {
		if ev.Type == EvDial {
			dials++
		}
		if ev.Type == EvPoolMiss {
			poolKeys[ev.Detail["pool_key"]] = true
		}
	}
	if dials != 2 {
		t.Fatalf("distinct ports must not share a pool, expected two dials got %d", dials)
	}
	if len(poolKeys) != 2 {
		t.Fatalf("expected two distinct pool keys, got %v", poolKeys)
	}
}
