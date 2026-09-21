package chunkfetch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chunkfetch"
	"chunkfetch/simsource"
)

func newTestFetcher(t *testing.T, dir string) *chunkfetch.Fetcher {
	t.Helper()
	return chunkfetch.New(chunkfetch.Config{
		Dir:            dir,
		Attempts:       3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     2 * time.Millisecond,
		Sleeper:        func(context.Context, time.Duration) error { return nil },
		Logger:         func(string) {},
	})
}

func testData() []byte {
	b := make([]byte, 1000)
	for i := range b {
		b[i] = byte((i*37 + 5) % 251)
	}
	return b
}

func readOutput(t *testing.T, dir string) []byte {
	t.Helper()
	out, err := os.ReadFile(filepath.Join(dir, "object.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func markerExists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "COMPLETE"))
	return !errors.Is(err, os.ErrNotExist)
}

func TestHappyPath(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	res, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || res.Chunks != 8 {
		t.Fatalf("result=%+v", res)
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output mismatch")
	}
	if !markerExists(t, dir) {
		t.Fatal("marker missing")
	}
	// Every chunk should have succeeded on the first attempt.
	for i := 0; i < 8; i++ {
		if res.Attempts[i] != 1 {
			t.Fatalf("chunk %d attempts=%d want 1", i, res.Attempts[i])
		}
	}
}

func TestEmptyObject(t *testing.T) {
	dir := t.TempDir()
	src := simsource.New("v1", nil, 64)
	res, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunks != 0 || !res.Completed {
		t.Fatalf("result=%+v", res)
	}
	if fi, err := os.Stat(filepath.Join(dir, "object.bin")); err != nil || fi.Size() != 0 {
		t.Fatalf("empty output: %v %v", fi, err)
	}
	if !markerExists(t, dir) {
		t.Fatal("marker missing")
	}
}

// One corrupt attempt must retry only that chunk; others stay at one attempt.
func TestCorruptChunkRetriedOnlyThatChunk(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	src.AddFault(3, simsource.FaultCorrupt, 2)
	res, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts[3] != 3 {
		t.Fatalf("chunk 3 attempts=%d want 3", res.Attempts[3])
	}
	for i := 0; i < 8; i++ {
		if i != 3 && res.Attempts[i] != 1 {
			t.Fatalf("chunk %d attempts=%d want 1", i, res.Attempts[i])
		}
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output mismatch")
	}
}

func TestTruncatedAndOversizeRetried(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	src.AddFault(1, simsource.FaultTruncated, 1)
	src.AddFault(6, simsource.FaultOversize, 1)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output mismatch")
	}
	if src.ReadCalls(1) < 2 || src.ReadCalls(6) < 2 {
		t.Fatalf("expected retries: c1=%d c6=%d", src.ReadCalls(1), src.ReadCalls(6))
	}
}

// A permanently bad chunk exhausts attempts: run fails and no marker exists.
func TestPersistentFailureNoMarker(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	src.AddFault(1, simsource.FaultCorrupt, 0)
	_, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err == nil || !strings.Contains(err.Error(), "failed after 3 attempts") {
		t.Fatalf("want exhaustion error, got %v", err)
	}
	if markerExists(t, dir) {
		t.Fatal("marker must not be written when the run fails")
	}
	// Good chunks before/after the failure were committed per-chunk.
	bm, berr := os.ReadFile(filepath.Join(dir, "progress.json"))
	if berr != nil {
		t.Fatal(berr)
	}
	if !strings.Contains(string(bm), `"version": "v1"`) {
		t.Fatalf("progress file not written: %s", bm)
	}
}

// Resume after a failure must only re-fetch chunks not marked in the bitmap.
func TestResumeFetchesOnlyMissingChunks(t *testing.T) {
	dir := t.TempDir()
	data := testData()

	bad := simsource.New("v1", data, 128)
	bad.AddFault(7, simsource.FaultCorrupt, 0)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), bad); err == nil {
		t.Fatal("expected first run to fail")
	}

	good := simsource.New("v1", data, 128)
	res, err := newTestFetcher(t, dir).Run(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if res.Attempts[i] != 0 {
			t.Fatalf("resume re-fetched chunk %d (attempts=%d)", i, res.Attempts[i])
		}
	}
	if res.Attempts[7] != 1 {
		t.Fatalf("chunk 7 attempts=%d want 1", res.Attempts[7])
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output mismatch")
	}
}

// A stored part that is corrupt on disk must be re-fetched on resume.
func TestCorruptStoredPartRefetched(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	// Tamper with chunk part 2 and drop its bit from the bitmap case is
	// covered separately; here keep the bit set but corrupt the file bytes.
	parts, _ := filepath.Glob(filepath.Join(dir, "chunks", "*", "chunk-000002.part"))
	if len(parts) != 1 {
		t.Fatalf("parts=%v", parts)
	}
	if err := os.WriteFile(parts[0], []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	src2 := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src2); err != nil {
		t.Fatal(err)
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output mismatch after corrupt part recovery")
	}
}

// Version change before Run: progress from v1 must be wiped, v2 fetched.
func TestVersionChangeWipesOldProgress(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	newData := append([]byte(nil), data...)
	newData[0] ^= 0xFF
	src.Replace("v2", newData, 128)
	res, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "v2" {
		t.Fatalf("version=%s want v2", res.Version)
	}
	if string(readOutput(t, dir)) != string(newData) {
		t.Fatal("new version output mismatch")
	}
	for i := 0; i < 8; i++ {
		if res.Attempts[i] != 1 {
			t.Fatalf("chunk %d not freshly fetched: attempts=%d", i, res.Attempts[i])
		}
	}
}

// A version mismatch detected mid-run aborts and wipes old progress; a
// following run fetches the new object fully.
func TestMidRunVersionFlip(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	newData := append([]byte(nil), data...)
	for i := range newData {
		newData[i] = byte(i % 7)
	}
	src.FlipAfter(5, "v2", newData, 128)
	_, err := newTestFetcher(t, dir).Run(context.Background(), src)
	var vmErr *chunkfetch.VersionMismatchError
	if !errors.As(err, &vmErr) {
		t.Fatalf("want VersionMismatchError, got %v", err)
	}
	if markerExists(t, dir) {
		t.Fatal("no marker after a version flip")
	}
	// Old progress was wiped on the mismatch.
	if _, statErr := os.Stat(filepath.Join(dir, "progress.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("progress.json should be removed: %v", statErr)
	}

	res, err := newTestFetcher(t, dir).Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "v2" || string(readOutput(t, dir)) != string(newData) {
		t.Fatal("resumed run did not produce v2")
	}
}

// Old-version parts must not be assembled into a new version.
func TestOldVersionPartsNeverMixed(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	// A leftover part dir for v1 must disappear when v2 starts.
	newData := append([]byte(nil), data...)
	newData[10]++
	src.Replace("v2", newData, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one version part dir, got %v", entries)
	}
	if string(readOutput(t, dir)) != string(newData) {
		t.Fatal("output is not the new version")
	}
}

// Completing twice is a cheap no-op once the marker and output verify.
func TestMarkerShortCircuit(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	f := newTestFetcher(t, dir)
	if _, err := f.Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	res, err := f.Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed {
		t.Fatal("second run should report no new completion")
	}
	if len(res.Attempts) != 0 {
		t.Fatalf("second run fetched chunks: %+v", res.Attempts)
	}
}

// A marker whose output was tampered with is distrusted; the object re-fetches.
func TestTamperedOutputInvalidatesMarker(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "object.bin"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if string(readOutput(t, dir)) != string(data) {
		t.Fatal("output not repaired")
	}
}

// A marker for a different version must never be accepted.
func TestMarkerVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	data := testData()
	src := simsource.New("v1", data, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	newData := append([]byte(nil), data...)
	newData[0]++
	src.Replace("v2", newData, 128)
	if _, err := newTestFetcher(t, dir).Run(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if string(readOutput(t, dir)) != string(newData) {
		t.Fatal("stale v1 marker accepted for v2")
	}
}
