package capgate_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	capgate "formal.local/w28-372608570c"
)

func TestDemoJobEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("go", "run", "./cmd/capgate", "testdata/demo_job.json")
	cmd.Dir = root
	run, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capgate run failed: %v\n%s", err, run)
	}
	if err := os.WriteFile(out, run, 0o600); err != nil {
		t.Fatal(err)
	}

	var report capgate.Report
	if err := json.Unmarshal(run, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Coverage.Complete || !report.Coverage.RegionsDisjoint {
		t.Fatalf("coverage proof invalid: %+v", report.Coverage)
	}
	if report.ComboCount != report.Coverage.CombosInRegions {
		t.Fatal("not every legal combo is covered")
	}
	for _, c := range report.Checks {
		if !c.Pass {
			t.Fatalf("semantic check %s failed: %s", c.ID, c.Detail)
		}
	}
	reasons := map[string]bool{}
	for _, d := range report.Denials {
		reasons[d.Reason] = true
	}
	for _, want := range []string{"cross_stage_leak_blocked", "incomplete_half_set"} {
		if !reasons[want] {
			t.Fatalf("expected denial %q, got %v", want, reasons)
		}
	}
}
