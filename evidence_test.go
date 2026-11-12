package netsec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a real test; it is the child process spawned by
// the evidence tests to get a real, short-lived subprocess.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	behavior := "ok"
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			behavior = os.Args[i+1]
		}
	}
	switch behavior {
	case "ok":
		os.Exit(0)
	case "fail":
		os.Exit(3)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(0)
}

func helperSpec(behavior string) RunSpec {
	return RunSpec{
		TestName: "helper/" + behavior,
		Input:    []byte("input-for-" + behavior),
		Path:     os.Args[0],
		Args:     []string{"-test.run=^TestHelperProcess$", "--", behavior},
		Env:      append(os.Environ(), "GO_WANT_HELPER_PROCESS=1"),
	}
}

func makeSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package fixture\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(dir, "main_test.go"), "package fixture\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRecord(t *testing.T, src string, behaviors ...string) *Bundle {
	t.Helper()
	r, err := NewRecorder(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range behaviors {
		if _, err := r.RecordRun(context.Background(), helperSpec(b)); err != nil {
			t.Fatal(err)
		}
	}
	return r.Seal()
}

func TestRecordAndVerifyPassedAndFailed(t *testing.T) {
	src := makeSourceDir(t)
	r, err := NewRecorder(src)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := r.RecordRun(context.Background(), helperSpec("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if ok.Status != StatusPassed || ok.ExitCode != 0 {
		t.Fatalf("want passed/0, got %s/%d", ok.Status, ok.ExitCode)
	}
	bad, err := r.RecordRun(context.Background(), helperSpec("fail"))
	if err != nil {
		t.Fatal(err)
	}
	if bad.Status != StatusFailed || bad.ExitCode != 3 {
		t.Fatalf("want failed/3, got %s/%d", bad.Status, bad.ExitCode)
	}
	b := r.Seal()

	data, err := b.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := UnmarshalBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(loaded); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := VerifyAgainstSource(loaded, src); err != nil {
		t.Fatalf("VerifyAgainstSource: %v", err)
	}
}

func TestVerifyAgainstSourceDetectsSnapshotDrift(t *testing.T) {
	src := makeSourceDir(t)
	b := mustRecord(t, src, "ok")
	writeFile(t, filepath.Join(src, "main.go"), "package fixture\n\nfunc Add(a, b int) int { return a - b }\n")
	if err := VerifyAgainstSource(b, src); err == nil {
		t.Fatal("expected rejection after source changed")
	}
	if err := Verify(b); err != nil {
		t.Fatalf("structural verify should still pass: %v", err)
	}
}

func TestMissingStepRejected(t *testing.T) {
	b := mustRecord(t, makeSourceDir(t), "ok")
	for _, drop := range []int{1, 2, 3, 4, 5} { // test, input, exec_start, exec_end, seal
		cp := *b
		cp.Records = append(append([]Record(nil), b.Records[:drop]...), b.Records[drop+1:]...)
		if err := Verify(&cp); err == nil {
			t.Fatalf("dropping record %d (%s) must be rejected", drop, b.Records[drop].Kind)
		}
	}
}

func TestReorderedRecordsRejected(t *testing.T) {
	b := mustRecord(t, makeSourceDir(t), "ok")
	cp := *b
	cp.Records = append([]Record(nil), b.Records...)
	cp.Records[3], cp.Records[4] = cp.Records[4], cp.Records[3] // swap exec_start/exec_end
	if err := Verify(&cp); err == nil {
		t.Fatal("swapped exec_start/exec_end must be rejected")
	}
}

func TestCrossSnapshotSpliceRejected(t *testing.T) {
	a := mustRecord(t, makeSourceDir(t), "ok")
	srcB := makeSourceDir(t)
	writeFile(t, filepath.Join(srcB, "extra.go"), "package fixture\n")
	bb := mustRecord(t, srcB, "ok")

	// Splice a whole test block (records 1..4) from bundle B into bundle A.
	cp := *a
	cp.Records = append(append([]Record(nil), a.Records[:1]...), bb.Records[1:5]...)
	cp.Records = append(cp.Records, a.Records[5])
	if err := Verify(&cp); err == nil {
		t.Fatal("cross-snapshot splice must be rejected")
	}
}

// rechain recomputes all links after tampering, simulating a determined
// forger; semantic checks must still catch impossible claims.
func rechain(b *Bundle) {
	prev := zeroDigest
	for i := range b.Records {
		b.Records[i].Seq = i
		b.Records[i].Prev = prev
		b.Records[i].Digest = b.Records[i].computeDigest()
		prev = b.Records[i].Digest
	}
	last := &b.Records[len(b.Records)-1]
	last.Payload.Head = b.Records[len(b.Records)-2].Digest
	last.Digest = last.computeDigest()
	b.Head = last.Digest
}

func TestInterruptedRunNotMarkedSuccess(t *testing.T) {
	src := makeSourceDir(t)
	r, err := NewRecorder(src)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := r.RecordRun(ctx, helperSpec("sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusInterrupted {
		t.Fatalf("want interrupted, got %s", res.Status)
	}
	if res.ExitCode == 0 {
		t.Fatal("interrupted run must not carry exit code 0")
	}
	b := r.Seal()
	if err := Verify(b); err != nil {
		t.Fatalf("honest interrupted record must verify: %v", err)
	}

	// Forge: relabel the interrupted exec_end as passed, rechain everything.
	cp := *b
	cp.Records = append([]Record(nil), b.Records...)
	for i := range cp.Records {
		if cp.Records[i].Kind == KindExecEnd {
			cp.Records[i].Payload.Status = StatusPassed
			cp.Records[i].Payload.Signal = ""
		}
	}
	rechain(&cp)
	if err := Verify(&cp); err == nil {
		t.Fatal("forged 'passed' without exit code 0 must be rejected")
	}
}

func TestUnfinishedExecutionCannotBePassed(t *testing.T) {
	b := mustRecord(t, makeSourceDir(t), "ok")
	cp := *b
	cp.Records = append([]Record(nil), b.Records...)
	for i := range cp.Records {
		if cp.Records[i].Kind == KindExecEnd {
			cp.Records[i].Payload.ExitCode = nil // pretend the run never finished
			cp.Records[i].Payload.Status = StatusPassed
		}
	}
	rechain(&cp)
	if err := Verify(&cp); err == nil {
		t.Fatal("passed status without exit code must be rejected")
	}
}

// TestRealGoTestSubprocess records a genuine `go test` run of a fixture
// package as the evidence-carrying subprocess.
func TestRealGoTestSubprocess(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "go.mod"), "module fixture\n\ngo 1.24.0\n")
	writeFile(t, filepath.Join(src, "add.go"), "package fixture\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(src, "add_test.go"), "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"Add broken\")\n\t}\n}\n")

	r, err := NewRecorder(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.RecordRun(context.Background(), RunSpec{
		TestName: "TestAdd",
		Input:    []byte("go test ./..."),
		Path:     "go",
		Args:     []string{"test", "-count=1", "-run", "^TestAdd$", "./..."},
		Dir:      src,
		Env: append(os.Environ(),
			"GOCACHE="+filepath.Join(t.TempDir(), "gocache"),
			"GOPROXY=off",
			"GOFLAGS=-mod=mod",
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPassed {
		t.Fatalf("real go test run should pass, got %s (err=%v)", res.Status, res.Err)
	}
	b := r.Seal()
	if err := VerifyAgainstSource(b, src); err != nil {
		t.Fatalf("VerifyAgainstSource: %v", err)
	}

	// The bundle must mention the fixture's test file digest via the snapshot.
	if !strings.HasPrefix(b.Snapshot, "sha256:") {
		t.Fatalf("bad snapshot digest %q", b.Snapshot)
	}
}
