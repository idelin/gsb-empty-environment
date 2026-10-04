package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSourceFiles(t *testing.T, dir string, contents [][]string) []string {
	t.Helper()
	var paths []string
	for i, lines := range contents {
		p := filepath.Join(dir, fmt.Sprintf("src-%d.txt", i))
		var sb strings.Builder
		for _, ln := range lines {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func testConfig(dir string, sources []string, mem int64) Config {
	return Config{
		MemLimit: mem,
		OutPath:  filepath.Join(dir, "out.txt"),
		WorkDir:  filepath.Join(dir, "work"),
		Sources:  sources,
	}
}

// runUntilDone re-runs the merger (fresh process state each time, like a real
// restart) until it completes. crashAfter <= 0 disables crash injection.
func runUntilDone(t *testing.T, cfg Config, crashAfter int64) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		c := cfg
		c.CrashAfter = crashAfter
		err := NewMerger(c).Run()
		if err == nil {
			return
		}
		if !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("invocation %d: %v", i, err)
		}
	}
	t.Fatal("merge did not converge after 1000 crash/resume cycles")
}

func mustVerify(t *testing.T, cfg Config) {
	t.Helper()
	if err := Verify(cfg.OutPath, cfg.Sources); err != nil {
		t.Fatal(err)
	}
}

func randomSources(r *rand.Rand, numSources, totalRecords, keySpace int) [][]string {
	srcs := make([][]string, numSources)
	for i := 0; i < totalRecords; i++ {
		s := r.Intn(numSources)
		srcs[s] = append(srcs[s], fmt.Sprintf("key%06d", r.Intn(keySpace)))
	}
	return srcs
}

func TestMergeBasic(t *testing.T) {
	dir := t.TempDir()
	srcs := writeSourceFiles(t, dir, [][]string{
		{"banana", "apple", "cherry"},
		{"apple", "date"},
		{"cherry", "apple", "banana"},
	})
	cfg := testConfig(dir, srcs, 1<<20)
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg)
}

func TestMergeEmptyAndSingle(t *testing.T) {
	dir := t.TempDir()
	srcs := writeSourceFiles(t, dir, [][]string{{}, {"only"}, {}})
	cfg := testConfig(dir, srcs, 1<<20)
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg)

	dir2 := t.TempDir()
	srcs2 := writeSourceFiles(t, dir2, [][]string{{}, {}, {}})
	cfg2 := testConfig(dir2, srcs2, 1<<20)
	runUntilDone(t, cfg2, 0)
	mustVerify(t, cfg2)
}

func TestMergeSmallMemory(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewSource(42))
	srcs := writeSourceFiles(t, dir, randomSources(r, 4, 500, 50))
	cfg := testConfig(dir, srcs, 200) // ~8 records per run
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg)
}

func TestMergeOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 5000) // much larger than the memory limit
	srcs := writeSourceFiles(t, dir, [][]string{
		{"a", big, "z"},
		{"m", big + "y"},
	})
	cfg := testConfig(dir, srcs, 128)
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg)
}

func TestCrashResumeRunsAndMerge(t *testing.T) {
	for _, crashAfter := range []int64{13, 40} {
		t.Run(fmt.Sprintf("crashAfter=%d", crashAfter), func(t *testing.T) {
			dir := t.TempDir()
			r := rand.New(rand.NewSource(7))
			srcs := writeSourceFiles(t, dir, randomSources(r, 5, 400, 60))
			cfg := testConfig(dir, srcs, 256)
			runUntilDone(t, cfg, crashAfter)
			mustVerify(t, cfg)
		})
	}
}

func TestLostOutputAck(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewSource(99))
	srcs := writeSourceFiles(t, dir, randomSources(r, 3, 300, 40))
	cfg := testConfig(dir, srcs, 512)

	// First run: crash after output fsync but before the checkpoint write.
	c := cfg
	c.CrashBeforeCheckpoint = true
	err := NewMerger(c).Run()
	if !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("expected simulated crash, got %v", err)
	}

	// The output file must contain unacknowledged bytes beyond the checkpoint.
	ck, err := loadCheckpoint(cfg.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(cfg.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() <= ck.OutOffset {
		t.Fatalf("expected un-acked output tail: file=%d acked=%d", st.Size(), ck.OutOffset)
	}

	// Resume without injection: must truncate the un-acked tail and finish.
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg)
}

func TestTruncatedInput(t *testing.T) {
	dir := t.TempDir()
	srcs := writeSourceFiles(t, dir, [][]string{
		{"alpha", "beta"},
		{"gamma", "delta"},
	})
	// Inject truncation: chop the second source mid-record.
	truncated := []byte("gamma\ndel")
	if err := os.WriteFile(srcs[1], truncated, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(dir, srcs, 1<<20)
	runUntilDone(t, cfg, 0)
	mustVerify(t, cfg) // verifier applies the same truncation rule

	// Sanity: the partial record must not appear in the output.
	data, err := os.ReadFile(cfg.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "del\t") || strings.Contains(string(data), "delta") {
		t.Fatalf("truncated record leaked into output:\n%s", data)
	}
	if !strings.Contains(string(data), "gamma\t1\t0") {
		t.Fatalf("complete record missing from output:\n%s", data)
	}
}

func TestTruncatedInputWithCrashResume(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewSource(5))
	srcs := writeSourceFiles(t, dir, randomSources(r, 4, 300, 40))
	// Truncate the last source mid-record.
	p := srcs[len(srcs)-1]
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data[:len(data)-3], 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(dir, srcs, 256)
	runUntilDone(t, cfg, 40)
	mustVerify(t, cfg)
}

func TestRerunAfterDoneIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	srcs := writeSourceFiles(t, dir, [][]string{{"b", "a"}, {"c"}})
	cfg := testConfig(dir, srcs, 1<<20)
	runUntilDone(t, cfg, 0)
	before, err := os.ReadFile(cfg.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, cfg, 0) // second run must be a no-op
	after, err := os.ReadFile(cfg.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("output changed on idempotent re-run")
	}
	mustVerify(t, cfg)
}

func TestSourceListMismatch(t *testing.T) {
	dir := t.TempDir()
	srcs := writeSourceFiles(t, dir, [][]string{{"a"}, {"b"}})
	cfg := testConfig(dir, srcs, 1<<20)
	runUntilDone(t, cfg, 0)
	bad := cfg
	bad.Sources = []string{srcs[0]}
	if err := NewMerger(bad).Run(); err == nil {
		t.Fatal("expected source mismatch error")
	}
}
