package extsort

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testOutput = "output.bin"

func writeTestInputs(t *testing.T, dir string, numFiles, linesPerFile, keySpace int, seed int64) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var paths []string
	for i := 0; i < numFiles; i++ {
		p := filepath.Join(dir, fmt.Sprintf("input-%02d.txt", i))
		var b bytes.Buffer
		for j := 0; j < linesPerFile; j++ {
			fmt.Fprintf(&b, "key-%04d\tvalue-%02d-%04d\n", rng.Intn(keySpace), i, j)
		}
		if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// cleanMergeArtifacts removes everything the merge produced while keeping
// the sorted runs and their manifest, so the merge replays from scratch.
func cleanMergeArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "run-") || name == runsManifestName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func testOptions(dir string) Options {
	return Options{
		Dir:             dir,
		MemRecords:      60,
		FanIn:           4,
		CheckpointEvery: 30,
	}
}

func checkOutput(t *testing.T, dir string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, testOutput))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

// TestFaultInjectionEveryCutPoint crashes the merge at every single
// checkpoint cut point, leaves unsynced garbage in the output tail, resumes,
// and requires the final output to equal one in-memory stable sort.
func TestFaultInjectionEveryCutPoint(t *testing.T) {
	dir := t.TempDir()
	inputs := writeTestInputs(t, dir, 4, 120, 16, 42)
	opts := testOptions(dir)

	runs, err := GenerateRuns(inputs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d runs", len(runs))

	want, err := ReferenceSort(inputs)
	if err != nil {
		t.Fatal(err)
	}

	// Pass 1: count the checkpoint cut points of a full clean merge.
	ResetOpenFileStats()
	total := 0
	countOpts := opts
	countOpts.CrashHook = func(CrashInfo) error { total++; return nil }
	if _, err := Merge(testOutput, countOpts); err != nil {
		t.Fatal(err)
	}
	checkOutput(t, dir, want)
	if total == 0 {
		t.Fatal("expected at least one checkpoint cut point")
	}
	t.Logf("merge publishes %d checkpoints; restarting at each cut point", total)

	// Pass 2: for every cut point, crash right after publishing it (with an
	// unsynced garbage tail in the output), then resume to completion.
	for cut := 1; cut <= total; cut++ {
		cleanMergeArtifacts(t, dir)
		published := 0
		crashOpts := opts
		crashOpts.CrashHook = func(ci CrashInfo) error {
			published++
			if published == cut {
				// Unsynced junk past the published prefix must not be trusted.
				f, err := os.OpenFile(ci.OutputFile, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					return err
				}
				if _, err := f.WriteString("GARBAGE-UNSYNCED-TAIL"); err != nil {
					f.Close()
					return err
				}
				f.Close()
				return ErrCrashed
			}
			return nil
		}
		if _, err := Merge(testOutput, crashOpts); err != ErrCrashed {
			t.Fatalf("cut %d: got %v, want ErrCrashed", cut, err)
		}
		if _, err := Merge(testOutput, opts); err != nil {
			t.Fatalf("cut %d: resume failed: %v", cut, err)
		}
		checkOutput(t, dir, want)
	}

	peak := PeakOpenFiles()
	t.Logf("peak open files: %d (fan-in %d)", peak, opts.FanIn)
	if limit := int64(opts.FanIn + 3); peak > limit {
		t.Fatalf("peak open files %d exceeds limit %d", peak, limit)
	}
}

// TestStableTies verifies records with identical keys keep their original
// input order across segment boundaries.
func TestStableTies(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	var wantValues []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("in-%d.txt", i))
		var b bytes.Buffer
		for j := 0; j < 50; j++ {
			v := fmt.Sprintf("seg%d-line%02d", i, j)
			fmt.Fprintf(&b, "samekey\t%s\n", v)
			wantValues = append(wantValues, v)
		}
		if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	opts := testOptions(dir)
	opts.MemRecords = 30 // force several runs so ties cross run boundaries
	if _, err := GenerateRuns(paths, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(testOutput, opts); err != nil {
		t.Fatal(err)
	}
	recs, err := DecodeFile(filepath.Join(dir, testOutput))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(wantValues) {
		t.Fatalf("got %d records, want %d", len(recs), len(wantValues))
	}
	for i, rec := range recs {
		if string(rec.Value) != wantValues[i] {
			t.Fatalf("record %d: got value %q, want %q (unstable)", i, rec.Value, wantValues[i])
		}
		if rec.Seq != uint64(i) {
			t.Fatalf("record %d: got seq %d, want %d", i, rec.Seq, i)
		}
	}
}

// TestResumeRejectsTamperedInput: a segment changed after the checkpoint
// must be detected via its bound digest, never silently trusted.
func TestResumeRejectsTamperedInput(t *testing.T) {
	dir := t.TempDir()
	inputs := writeTestInputs(t, dir, 3, 100, 10, 7)
	opts := testOptions(dir)
	if _, err := GenerateRuns(inputs, opts); err != nil {
		t.Fatal(err)
	}
	crashOpts := opts
	crashOpts.CrashHook = func(CrashInfo) error { return ErrCrashed }
	if _, err := Merge(testOutput, crashOpts); err != ErrCrashed {
		t.Fatalf("got %v, want ErrCrashed", err)
	}
	// Corrupt one run segment.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "run-") {
			p := filepath.Join(dir, e.Name())
			f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("x")
			f.Close()
			break
		}
	}
	if _, err := Merge(testOutput, opts); err == nil {
		t.Fatal("resume trusted a tampered input segment")
	} else {
		t.Logf("tampered input rejected: %v", err)
	}
}

// TestResumeRejectsTamperedPrefix: bytes inside the published output prefix
// are protected by the checkpoint digest.
func TestResumeRejectsTamperedPrefix(t *testing.T) {
	dir := t.TempDir()
	inputs := writeTestInputs(t, dir, 3, 100, 10, 9)
	opts := testOptions(dir)
	if _, err := GenerateRuns(inputs, opts); err != nil {
		t.Fatal(err)
	}
	var outFile string
	var publishedLen int64
	crashOpts := opts
	crashOpts.CrashHook = func(ci CrashInfo) error {
		outFile = ci.OutputFile
		publishedLen = ci.PublishedLen
		return ErrCrashed
	}
	if _, err := Merge(testOutput, crashOpts); err != ErrCrashed {
		t.Fatalf("got %v, want ErrCrashed", err)
	}
	if publishedLen == 0 {
		t.Fatal("no published prefix to corrupt")
	}
	f, err := os.OpenFile(outFile, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, publishedLen-1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := Merge(testOutput, opts); err == nil {
		t.Fatal("resume trusted a tampered published prefix")
	} else {
		t.Logf("tampered prefix rejected: %v", err)
	}
}

// TestSeqPrecision: sequence numbers keep full uint64 precision, so
// stability holds beyond 2^32 or float53-scale inputs.
func TestSeqPrecision(t *testing.T) {
	recs := []Record{
		{Key: "k", Seq: 1<<40 + 1},
		{Key: "k", Seq: 5},
		{Key: "k", Seq: 1 << 40},
		{Key: "k", Seq: 1<<32 + 7},
	}
	want := []uint64{5, 1<<32 + 7, 1 << 40, 1<<40 + 1}
	sorted := []uint64{}
	order := []int{0, 1, 2, 3}
	for a := 0; a < len(order); a++ {
		for b := a + 1; b < len(order); b++ {
			if recs[order[b]].Less(&recs[order[a]]) {
				order[a], order[b] = order[b], order[a]
			}
		}
	}
	for _, idx := range order {
		sorted = append(sorted, recs[idx].Seq)
	}
	for i := range want {
		if sorted[i] != want[i] {
			t.Fatalf("seq order %v, want %v", sorted, want)
		}
	}
}

// TestEmptyInput: zero records produce an empty output that still verifies.
func TestEmptyInput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(dir)
	if _, err := GenerateRuns([]string{p}, opts); err != nil {
		t.Fatal(err)
	}
	b, err := Merge(testOutput, opts)
	if err != nil {
		t.Fatal(err)
	}
	if b.Records != 0 || b.Size != 0 {
		t.Fatalf("got %+v, want empty output", b)
	}
	want, err := ReferenceSort([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, dir, want)
}
