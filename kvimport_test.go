package kvimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rec(k, v string) string { return fmt.Sprintf(`{"k":%q,"v":%q}`, k, v) }

func inputOf(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func readStore(t *testing.T, dir string) snapshot {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, storeFileName))
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parse store.json: %v", err)
	}
	return s
}

func storeBytes(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, storeFileName))
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	return b
}

func rejectLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rejectsFileName))
	if err != nil {
		t.Fatalf("read rejects.log: %v", err)
	}
	out := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if out[0] == "" {
		return nil
	}
	return out
}

// failAt returns an Inject func that fails on the n-th (1-based) call to
// the given point.
func failAt(point string, n int) func(string, int) error {
	calls := 0
	return func(p string, batch int) error {
		if p == point {
			calls++
			if calls == n {
				return errors.New("simulated crash")
			}
		}
		return nil
	}
}

func TestFreshImportPublishesSnapshot(t *testing.T) {
	dir := t.TempDir()
	in := inputOf(rec("a", "1"), rec("b", "2"), rec("a", "3"))
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Resumed {
		t.Fatal("fresh import must not be marked resumed")
	}
	if res.Records != 3 || res.Rejects != 0 || res.Keys != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Offset != int64(len(in)) {
		t.Fatalf("offset %d, want %d", res.Offset, len(in))
	}
	s := readStore(t, dir)
	if got := s.Keys["a"]; got.Value != "3" || got.Version != 2 {
		t.Fatalf("key a = %+v, want value=3 version=2", got)
	}
	if got := s.Keys["b"]; got.Value != "2" || got.Version != 1 {
		t.Fatalf("key b = %+v, want value=2 version=1", got)
	}
	if s.Meta.Records != 3 || s.Meta.Keys != 2 {
		t.Fatalf("unexpected meta: %+v", s.Meta)
	}
	// Checkpoint must exist and cover the whole input.
	cpBytes, err := os.ReadFile(filepath.Join(dir, checkpointFileName))
	if err != nil {
		t.Fatal(err)
	}
	cp, err := parseCheckpoint(cpBytes)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Offset != int64(len(in)) || cp.Digest != res.Digest {
		t.Fatalf("checkpoint %+v does not match result %+v", cp, res)
	}
}

func TestBadRecordsAreIsolatedAndImportContinues(t *testing.T) {
	dir := t.TempDir()
	in := inputOf(
		rec("good1", "1"),
		`{"broken json`,
		rec("good2", "2"),
		`{"k":"novalue"}`,
		``,
		`{"k":"","v":"emptykey"}`,
		`{"k":"x","v":"1","extra":true}`,
		rec("good3", "3"),
	)
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 3 || res.Rejects != 4 {
		t.Fatalf("records=%d rejects=%d, want 3/4", res.Records, res.Rejects)
	}
	s := readStore(t, dir)
	if len(s.Keys) != 3 {
		t.Fatalf("published keys = %v", s.Keys)
	}
	rej := rejectLines(t, dir)
	if len(rej) != 4 {
		t.Fatalf("rejects.log has %d lines, want 4: %v", len(rej), rej)
	}
	for _, l := range rej {
		var e rejectEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("bad reject entry %q: %v", l, err)
		}
		if e.Reason == "" || e.Raw == "" {
			t.Fatalf("reject entry missing reason/raw: %q", l)
		}
	}
}

func TestTruncatedLastLineIsNotCommitted(t *testing.T) {
	dir := t.TempDir()
	complete := rec("a", "1") + "\n"
	tail := `{"k":"b","v":"2` // no closing brace, no newline
	res, err := Import(strings.NewReader(complete+tail), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 1 || res.Rejects != 1 {
		t.Fatalf("records=%d rejects=%d, want 1/1", res.Records, res.Rejects)
	}
	s := readStore(t, dir)
	if _, ok := s.Keys["b"]; ok {
		t.Fatal("truncated record must not be committed")
	}
	if s.Keys["a"].Value != "1" {
		t.Fatalf("key a = %+v", s.Keys["a"])
	}
	rej := rejectLines(t, dir)
	if len(rej) != 1 || !strings.Contains(rej[0], "truncated") {
		t.Fatalf("rejects = %v, want one truncated entry", rej)
	}
	// The tail bytes are consumed: offset covers the whole input, so a
	// resume will not re-process them.
	if res.Offset != int64(len(complete)+len(tail)) {
		t.Fatalf("offset %d, want %d", res.Offset, len(complete)+len(tail))
	}
	// Resume with the same input: no duplicate reject, same snapshot.
	before := storeBytes(t, dir)
	res2, err := Import(strings.NewReader(complete+tail), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Rejects != 1 {
		t.Fatalf("after resume rejects=%d, want 1 (no duplicate)", res2.Rejects)
	}
	if got := storeBytes(t, dir); string(got) != string(before) {
		t.Fatal("snapshot changed after idempotent resume")
	}
}

// Crash after staging fsync but before the checkpoint: the unconfirmed
// batch must be discarded and replayed without growing key versions.
func TestCrashBeforeCheckpointReplayHasNoVersionGrowth(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("a", "2"), rec("a", "3"), rec("b", "1"), rec("a", "4"))

	baseDir := t.TempDir()
	if _, err := Import(strings.NewReader(in), Options{Dir: baseDir, BatchSize: 2}); err != nil {
		t.Fatal(err)
	}
	want := storeBytes(t, baseDir)

	dir := t.TempDir()
	// Batch 1 confirmed; crash while committing batch 2, after data sync.
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 2,
		Inject: failAt(PointAfterDataSync, 2),
	})
	if err == nil || !strings.Contains(err.Error(), "afterDataSync") {
		t.Fatalf("want injected crash, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, storeFileName)); !os.IsNotExist(err) {
		t.Fatal("store.json must not exist before publish")
	}

	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed {
		t.Fatal("second run must resume from checkpoint")
	}
	if got := storeBytes(t, dir); string(got) != string(want) {
		t.Fatalf("snapshot after crash+resume differs from clean run:\n%s\nwant:\n%s", got, want)
	}
	s := readStore(t, dir)
	if s.Keys["a"].Version != 4 {
		t.Fatalf("key a version = %d, want 4 (replay must not grow versions)", s.Keys["a"].Version)
	}
}

// Crash after the checkpoint: the confirmed batch survives and is not
// re-applied.
func TestCrashAfterCheckpointKeepsConfirmedBatch(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("a", "2"), rec("b", "1"), rec("a", "3"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 2,
		Inject: failAt(PointAfterCheckpoint, 1),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 4 {
		t.Fatalf("records=%d, want 4", res.Records)
	}
	s := readStore(t, dir)
	if s.Keys["a"].Version != 3 || s.Keys["a"].Value != "3" {
		t.Fatalf("key a = %+v, want value=3 version=3", s.Keys["a"])
	}
}

func TestResumeRejectsChangedInput(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("b", "2"), rec("c", "3"), rec("d", "4"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 2,
		Inject: failAt(PointAfterCheckpoint, 1),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}

	// Same length, one byte different inside the confirmed prefix.
	changed := strings.Replace(in, `"1"`, `"9"`, 1)
	if _, err := Import(strings.NewReader(changed), Options{Dir: dir, BatchSize: 2}); err == nil ||
		!strings.Contains(err.Error(), "input changed") {
		t.Fatalf("changed input must be rejected, got %v", err)
	}

	// Shorter input must also be rejected.
	short := inputOf(rec("a", "1"))
	if _, err := Import(strings.NewReader(short), Options{Dir: dir, BatchSize: 2}); err == nil ||
		!strings.Contains(err.Error(), "input changed") {
		t.Fatalf("short input must be rejected, got %v", err)
	}

	// Unchanged input still resumes fine.
	if _, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2}); err != nil {
		t.Fatalf("unchanged input must resume: %v", err)
	}
}

func TestUnpublishedBatchIsInvisible(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("b", "2"), rec("c", "3"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 1,
		Inject: failAt(PointBeforePublish, 1),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}
	// All batches are checkpointed, but nothing is published.
	if _, err := os.Stat(filepath.Join(dir, storeFileName)); !os.IsNotExist(err) {
		t.Fatal("store.json must not exist before final publish")
	}
	if _, err := os.Stat(filepath.Join(dir, checkpointFileName)); err != nil {
		t.Fatal("checkpoint must exist")
	}
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 3 {
		t.Fatalf("records=%d, want 3", res.Records)
	}
	if _, err := os.Stat(filepath.Join(dir, storeFileName)); err != nil {
		t.Fatal("store.json must exist after resume")
	}
}

func TestCorruptCheckpointIsRejected(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("b", "2"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 1,
		Inject: failAt(PointAfterCheckpoint, 1),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}
	cpPath := filepath.Join(dir, checkpointFileName)
	b, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	// Rotate a digit in the offset field without fixing the checksum.
	i := strings.Index(string(b), `"offset"`) + len(`"offset":`)
	b[i] = (b[i]-'0'+1)%10 + '0'
	if err := os.WriteFile(cpPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 1}); err == nil ||
		!strings.Contains(err.Error(), "checkpoint corrupt") {
		t.Fatalf("corrupt checkpoint must be rejected, got %v", err)
	}
}

// A bad record inside the unconfirmed tail must be quarantined exactly once
// after replay, not duplicated.
func TestRejectsAreNotDuplicatedOnReplay(t *testing.T) {
	in := inputOf(rec("g1", "1"), rec("g2", "2"), `not-json`, rec("g3", "3"), rec("g4", "4"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir: dir, BatchSize: 2,
		Inject: failAt(PointAfterDataSync, 2),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejects != 1 {
		t.Fatalf("rejects=%d, want 1", res.Rejects)
	}
	if rej := rejectLines(t, dir); len(rej) != 1 {
		t.Fatalf("rejects.log has %d lines, want 1: %v", len(rej), rej)
	}
}

func TestRerunCompletedImportIsIdempotent(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("b", "2"), rec("a", "3"))
	dir := t.TempDir()
	if _, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	first := storeBytes(t, dir)
	res, err := Import(strings.NewReader(in), Options{Dir: dir, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed {
		t.Fatal("second run must be a resume")
	}
	if got := storeBytes(t, dir); string(got) != string(first) {
		t.Fatal("re-running a completed import changed the snapshot")
	}
}

func TestEmptyInput(t *testing.T) {
	dir := t.TempDir()
	res, err := Import(strings.NewReader(""), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 0 || res.Keys != 0 || res.Offset != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	s := readStore(t, dir)
	if len(s.Keys) != 0 || s.Meta.Records != 0 {
		t.Fatalf("unexpected snapshot: %+v", s.Meta)
	}
}

func TestCrashAfterPublishRenameIsConsistent(t *testing.T) {
	in := inputOf(rec("a", "1"), rec("b", "2"))
	dir := t.TempDir()
	_, err := Import(strings.NewReader(in), Options{
		Dir:    dir,
		Inject: failAt(PointAfterPublishRename, 1),
	})
	if err == nil {
		t.Fatal("want injected crash")
	}
	// The rename already happened: the published snapshot is complete.
	s := readStore(t, dir)
	if len(s.Keys) != 2 || s.Meta.Records != 2 {
		t.Fatalf("published snapshot incomplete: %+v", s.Meta)
	}
	// Resume finishes cleanly and republishes identical bytes.
	before := storeBytes(t, dir)
	if _, err := Import(strings.NewReader(in), Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := storeBytes(t, dir); string(got) != string(before) {
		t.Fatal("snapshot changed after resume past publish")
	}
}
