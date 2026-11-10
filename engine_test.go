package main

import (
	"encoding/json"
	"os"
	"testing"
)

func loadTest(t *testing.T, path string) Scenario {
	t.Helper()
	sc, err := loadScenario(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return sc
}

func runTest(t *testing.T, sc Scenario) *Report {
	t.Helper()
	report, err := execute(sc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return report
}

func findEvent(report *Report, evType, requestID string) *Event {
	for i := range report.Events {
		ev := &report.Events[i]
		if ev.Type == evType && (requestID == "" || ev.RequestID == requestID) {
			return ev
		}
	}
	return nil
}

func TestTTLExactExpiryDialViolation(t *testing.T) {
	report := runTest(t, loadTest(t, "testdata/v1_ttl_boundary.json"))

	expiry := findEvent(report, EvTimerExpiry, "")
	if expiry == nil || expiry.At != 15 {
		t.Fatalf("expected inclusive TTL expiry at t=15, got %+v", expiry)
	}

	check := findEvent(report, EvValidate, "r1")
	if check == nil || check.Detail["result"] != "PASS" {
		t.Fatalf("validation must PASS on 172.16.0.10, got %+v", check)
	}

	dial := findEvent(report, EvDial, "r1")
	if dial == nil || dial.Detail["dial_address"] != "10.20.30.40:8080" {
		t.Fatalf("dial must target forbidden 10.20.30.40 after expiry, got %+v", dial)
	}

	if len(report.Violations) != 1 || report.Violations[0].Kind != ViolationDial {
		t.Fatalf("expected exactly one V1 violation, got %+v", report.Violations)
	}
	v := report.Violations[0]
	if len(v.ValidatedAddrs) != 1 || v.ValidatedAddrs[0] != "172.16.0.10" {
		t.Fatalf("validated address must stay 172.16.0.10, got %v", v.ValidatedAddrs)
	}

	subReport := runTest(t, v.SubScenario)
	if len(subReport.Violations) != 1 || subReport.Violations[0].Kind != ViolationDial {
		t.Fatalf("extracted subscenario must reproduce V1 standalone, got %+v", subReport.Violations)
	}

	var sawResolve bool
	for _, seq := range v.CausalEventSeqs {
		if report.Events[seq-1].Type == EvResolve {
			sawResolve = true
		}
	}
	if !sawResolve {
		t.Fatalf("causal subsequence must include the post-expiry dns_resolve event")
	}
}

func TestPoolReuseViolation(t *testing.T) {
	report := runTest(t, loadTest(t, "testdata/v2_pool_reuse.json"))

	if len(report.Violations) != 2 {
		t.Fatalf("expected V1 poisoning + V2 reuse violations, got %+v", report.Violations)
	}

	var v1, v2 *Violation
	for i := range report.Violations {
		switch report.Violations[i].Kind {
		case ViolationDial:
			v1 = &report.Violations[i]
		case ViolationPoolReuse:
			v2 = &report.Violations[i]
		}
	}
	if v1 == nil || v1.RequestID != "r1" || v1.DialAddress != "10.20.30.40:8080" {
		t.Fatalf("V1 poisoning request mismatch: %+v", v1)
	}
	if v2 == nil || v2.RequestID != "r2" || v2.ReusedPeer != "10.20.30.40:8080" {
		t.Fatalf("V2 must reuse forbidden peer without re-dial, got %+v", v2)
	}

	get := findEvent(report, EvPoolGet, "r2")
	if get == nil {
		t.Fatalf("r2 must hit the pool")
	}
	if findEvent(report, EvDial, "r2") != nil {
		t.Fatalf("r2 must not dial when the pool has an idle connection")
	}

	check2 := findEvent(report, EvValidate, "r2")
	if check2 == nil || check2.Detail["validated_addresses"] != "172.16.0.10" {
		t.Fatalf("r2 validation must pass on the safe re-resolved address, got %+v", check2)
	}

	subReport := runTest(t, v2.SubScenario)
	if len(subReport.Violations) != 2 {
		t.Fatalf("V2 witness must retain the full poisoning chain (both violations), got %d", len(subReport.Violations))
	}
	if len(v2.SubScenario.Requests) != 2 {
		t.Fatalf("V2 witness needs both r1 and r2 requests, got %d", len(v2.SubScenario.Requests))
	}
}

func TestBlockedAndSafeProduceNoViolation(t *testing.T) {
	report := runTest(t, loadTest(t, "testdata/blocked_and_safe.json"))

	if len(report.Violations) != 0 {
		t.Fatalf("blocked check and safe dial must not create violations, got %+v", report.Violations)
	}
	blocked := findEvent(report, EvValidate, "blocked-check")
	if blocked == nil || blocked.Detail["result"] != "BLOCK" {
		t.Fatalf("first check must BLOCK, got %+v", blocked)
	}
	if findEvent(report, EvDial, "blocked-check") != nil {
		t.Fatalf("blocked request must never dial")
	}
	safe := findEvent(report, EvDial, "safe-after-expiry")
	if safe == nil || safe.Detail["dial_address"] != "172.16.0.10:8080" {
		t.Fatalf("second request must dial the safe address after re-resolution, got %+v", safe)
	}

	resolves := 0
	for _, ev := range report.Events {
		if ev.Type == EvResolve {
			resolves++
		}
	}
	if resolves != 2 {
		t.Fatalf("each boundary lookup must re-resolve, expected 2 resolves got %d", resolves)
	}
}

func TestCausalClosureIsTopologicalAndConnected(t *testing.T) {
	report := runTest(t, loadTest(t, "testdata/v1_ttl_boundary.json"))
	v := report.Violations[0]
	seen := map[int]bool{}
	for _, seq := range v.CausalEventSeqs {
		for _, parent := range report.Events[seq-1].Parents {
			if !seen[parent] && parent != seq {
				t.Fatalf("closure missing parent %d of event %d", parent, seq)
			}
		}
		seen[seq] = true
	}
}

func TestMainRejectsBadCIDR(t *testing.T) {
	sc := Scenario{
		Forbidden: []string{"not-a-cidr"},
		Hosts:     map[string]Host{"h": {Port: 1, TTL: 1, Steps: [][]string{{"1.1.1.1"}}}},
		Requests:  []RequestSpec{{ID: "r", Host: "h", ValidateAt: 0}},
	}
	if _, err := execute(sc); err == nil {
		t.Fatal("expected invalid CIDR to fail")
	}
}

func TestReportFileRoundTrip(t *testing.T) {
	path := t.TempDir() + "/report.json"
	report := runTest(t, loadTest(t, "testdata/v1_ttl_boundary.json"))
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("report must be valid JSON: %v", err)
	}
	if len(decoded.Violations) != 1 {
		t.Fatalf("decoded report lost violations: %d", len(decoded.Violations))
	}
}
