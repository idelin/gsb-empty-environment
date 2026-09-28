package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildCLI builds the skewcheck binary once into the test temp dir.
func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "skewcheck")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build cli: %v", err)
	}
	return bin
}

func runCLI(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	return string(out), code
}

func TestCLIFeasible(t *testing.T) {
	bin := buildCLI(t)
	fixture := filepath.Join("..", "..", "fixtures", "feasible_rtt.json")
	out, code := runCLI(t, bin, fixture)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if !strings.Contains(out, "[-100ns, -80ns]") {
		t.Fatalf("output missing interval:\n%s", out)
	}
}

func TestCLIConflictExitCode(t *testing.T) {
	bin := buildCLI(t)
	fixture := filepath.Join("..", "..", "fixtures", "contradiction_rtt.json")
	out, code := runCLI(t, bin, fixture)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "INFEASIBLE") || !strings.Contains(out, "-5ns") {
		t.Fatalf("output missing contradiction:\n%s", out)
	}
}

func TestCLIJSON(t *testing.T) {
	bin := buildCLI(t)
	fixture := filepath.Join("..", "..", "fixtures", "disconnected.json")
	out, code := runCLI(t, bin, "-json", fixture)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	var rpt map[string]any
	if err := json.Unmarshal([]byte(out), &rpt); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if rpt["feasible"] != true {
		t.Fatalf("feasible field: %v", rpt["feasible"])
	}
}

func TestCLIWideFlag(t *testing.T) {
	bin := buildCLI(t)
	fixture := filepath.Join("..", "..", "fixtures", "disconnected.json")
	out, code := runCLI(t, bin, "-wide", "1s", fixture)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if !strings.Contains(out, "[-1000000000, 1000000000]") {
		t.Fatalf("wide interval missing:\n%s", out)
	}
}
