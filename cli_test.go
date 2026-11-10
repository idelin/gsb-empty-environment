package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunWritesReportAndReplaySucceeds(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")

	sc := loadTest(t, "testdata/v1_ttl_boundary.json")
	engine, err := newEngine(sc)
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := engine.run()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("report JSON invalid: %v", err)
	}

	witnessPath := filepath.Join(dir, "witness.json")
	witness, err := json.MarshalIndent(decoded.Violations[0].SubScenario, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(witnessPath, witness, 0o644); err != nil {
		t.Fatal(err)
	}

	witnessScenario, err := loadScenario(witnessPath)
	if err != nil {
		t.Fatalf("witness must be a loadable Scenario: %v", err)
	}
	replayReport, err := execute(witnessScenario)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayReport.Violations) != 1 {
		t.Fatalf("standalone witness must reproduce exactly one violation, got %d", len(replayReport.Violations))
	}
}

func TestReplayRejectsWitnessWithoutViolation(t *testing.T) {
	sc := loadTest(t, "testdata/blocked_and_safe.json")
	report, err := execute(sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("sanity: scenario has no violations")
	}
}
