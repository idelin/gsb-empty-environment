package doublesign

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Builds the standalone verifier and proves a real conviction is accepted using
// only the evidence file plus the authority public key (no private keys).
func TestCLIVerifiesEvidenceOffline(t *testing.T) {
	if runtime.GOOS == "" {
		t.Skip("unavailable platform")
	}
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, digest(0x90), nil, 0)), VerdictAccepted)
	ev := a.Observe(share(k, 1, 0, 0, PhasePrepare, digest(0x91), nil, 0)).Evidence

	dir := t.TempDir()
	evPath := filepath.Join(dir, "conviction.json")
	raw, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(dir, "verifier")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary,
		"formal.local/w3-39e1170702/cmd/doublesign-verify")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build verifier: %v\n%s", err, out)
	}

	cmd := exec.Command(binary, "-authority-key", hex.EncodeToString(authPub), evPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("valid evidence rejected: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "VALID conviction") {
		t.Fatalf("unexpected output: %s", out)
	}

	// A wrong authority key must reject the same evidence.
	_, _, wrongPub := genAuthority(t)
	bad := exec.Command(binary, "-authority-key", hex.EncodeToString(wrongPub), evPath)
	if err := bad.Run(); err == nil {
		t.Fatal("evidence unexpectedly verified under wrong authority key")
	}
}
