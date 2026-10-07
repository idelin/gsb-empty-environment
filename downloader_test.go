package paradl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newRun(t *testing.T, faults *FaultFile, seed, size int64) (*LocalSource, *Downloader, string, string) {
	t.Helper()
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	ckpt := filepath.Join(dir, "out.ckpt.json")
	src := NewLocalSource(seed, size, "v0", faults)
	d := NewDownloader(src, dst, ckpt, faults)
	d.ChunkSize = 4096
	d.Workers = 4
	return src, d, dst, ckpt
}

func requireContent(t *testing.T, dst string, src *LocalSource) {
	t.Helper()
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	want := src.Content()
	if ChecksumBytes(got) != ChecksumBytes(want) {
		t.Fatalf("content mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
}

func TestBasicDownload(t *testing.T) {
	src, d, dst, _ := newRun(t, nil, 7, 1<<18)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	requireContent(t, dst, src)
}

func TestShortWrite(t *testing.T) {
	// Every 3rd response is truncated to 1000 bytes; the downloader must
	// confirm only the actual range and re-fetch the remainder.
	faults := &FaultFile{ShortWrites: []ShortWriteFault{
		{OnRequest: 3, KeepBytes: 1000},
		{OnRequest: 6, KeepBytes: 0},
		{OnRequest: 9, KeepBytes: 2048},
	}}
	src, d, dst, _ := newRun(t, faults, 11, 120000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	requireContent(t, dst, src)
}

func TestStaleResponseArrivingLate(t *testing.T) {
	// Request 2 captures a snapshot; request 4 changes the entity version;
	// request 7 serves the stale snapshot. It must be rejected and the
	// round restarted against the new version.
	faults := &FaultFile{
		StaleResponses: []StaleResponseFault{{OnRequest: 2, DelayRequests: 5}},
		VersionChanges: []VersionChangeFault{{OnRequest: 4, NewSeed: 99, NewVersion: "v1"}},
	}
	src, d, dst, _ := newRun(t, faults, 5, 200000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	requireContent(t, dst, src)
	if src.Version() != "v1" {
		t.Fatalf("expected version v1, got %s", src.Version())
	}
}

func TestVersionChangeInvalidatesRound(t *testing.T) {
	// Version changes mid-download with a different size; all confirmations
	// from the old version are void and the final file is the new entity.
	faults := &FaultFile{
		VersionChanges: []VersionChangeFault{{OnRequest: 5, NewSeed: 77, NewSize: 150000, NewVersion: "v2"}},
	}
	src, d, dst, _ := newRun(t, faults, 3, 200000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	requireContent(t, dst, src)
	if fi, _ := os.Stat(dst); fi.Size() != 150000 {
		t.Fatalf("dst size = %d, want 150000", fi.Size())
	}
}

func TestCheckpointCutPointCrashAndResume(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	ckpt := filepath.Join(dir, "out.ckpt.json")

	faults := &FaultFile{CrashOnCheckpointSave: 3}
	src := NewLocalSource(13, 300000, "v0", faults)
	d := NewDownloader(src, dst, ckpt, faults)
	d.ChunkSize = 4096
	d.Workers = 4

	err := d.Run(context.Background())
	if !errors.Is(err, ErrSimulatedCrash) {
		t.Fatalf("expected simulated crash, got %v", err)
	}

	// The checkpoint must be loadable and consistent: either the previous
	// or the new state, never torn.
	if _, err := LoadCheckpoint(ckpt); err != nil {
		t.Fatalf("checkpoint after crash: %v", err)
	}

	// Resume with a fresh downloader and no faults.
	src2 := NewLocalSource(13, 300000, "v0", nil)
	d2 := NewDownloader(src2, dst, ckpt, nil)
	d2.ChunkSize = 4096
	d2.Workers = 4
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	requireContent(t, dst, src2)
}

func TestTornCheckpointDiscarded(t *testing.T) {
	src, d, dst, ckpt := newRun(t, nil, 17, 100000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Simulate a torn write: garbage in the checkpoint file.
	if err := os.WriteFile(ckpt, []byte(`{"version":"v0","confirmed":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	d2 := NewDownloader(src, dst, ckpt, nil)
	d2.ChunkSize = 4096
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	requireContent(t, dst, src)
}

func TestResumeRejectsCorruptConfirmedBytes(t *testing.T) {
	src, d, dst, ckpt := newRun(t, nil, 23, 120000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Corrupt bytes inside a confirmed range on disk.
	f, err := os.OpenFile(dst, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xde, 0xad, 0xbe, 0xef}, 5000); err != nil {
		t.Fatal(err)
	}
	f.Close()

	d2 := NewDownloader(src, dst, ckpt, nil)
	d2.ChunkSize = 4096
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	requireContent(t, dst, src)
}

func TestCheckpointVersionMismatchRestarts(t *testing.T) {
	_, d, dst, ckpt := newRun(t, nil, 29, 100000)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Same path, but the source now serves a different entity version.
	src2 := NewLocalSource(31, 100000, "v9", nil)
	d2 := NewDownloader(src2, dst, ckpt, nil)
	d2.ChunkSize = 4096
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	requireContent(t, dst, src2)
}

// wideSource returns actual ranges wider than requested, forcing overlaps.
type wideSource struct{ *LocalSource }

func (w wideSource) Fetch(ctx context.Context, r ByteRange) (Response, error) {
	wide := ByteRange{Start: max(r.Start-2048, 0), End: r.End + 2048}
	return w.LocalSource.Fetch(ctx, wide)
}

func TestOverlappingResponsesMerge(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	ckpt := filepath.Join(dir, "out.ckpt.json")
	base := NewLocalSource(37, 100000, "v0", nil)
	d := NewDownloader(wideSource{base}, dst, ckpt, nil)
	d.ChunkSize = 4096
	d.Workers = 4
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	requireContent(t, dst, base)
}

// conflictSource serves bytes that disagree across overlapping responses.
type conflictSource struct {
	size int64
	n    int
}

func (c *conflictSource) Head(ctx context.Context) (int64, string, error) {
	return c.size, "v0", nil
}

func (c *conflictSource) Fetch(ctx context.Context, r ByteRange) (Response, error) {
	c.n++
	data := make([]byte, r.Len())
	for i := range data {
		data[i] = byte(r.Start>>8) & 0xff
	}
	if c.n > 1 {
		data[0] ^= 0xff // overlapping bytes disagree with earlier responses
	}
	return Response{Version: "v0", Range: r, Data: data, Checksum: ChecksumBytes(data)}, nil
}

func TestConflictingOverlapsRejected(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	ckpt := filepath.Join(dir, "out.ckpt.json")
	src := &conflictSource{size: 64 * 1024}
	d := NewDownloader(overlapOnce{src}, dst, ckpt, nil)
	d.ChunkSize = 4096
	d.Workers = 1
	err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

// overlapOnce widens exactly one request so it overlaps a confirmed range.
type overlapOnce struct{ *conflictSource }

func (o overlapOnce) Fetch(ctx context.Context, r ByteRange) (Response, error) {
	if o.n == 1 {
		r = ByteRange{Start: r.Start - 100, End: r.End}
	}
	return o.conflictSource.Fetch(ctx, r)
}

func TestFaultFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "faults.json")
	want := `{"short_writes":[{"on_request":2,"keep_bytes":10}],"crash_on_checkpoint_save":1}`
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFaults(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(f.ShortWrites) != 1 || f.ShortWrites[0].KeepBytes != 10 || f.CrashOnCheckpointSave != 1 {
		t.Fatalf("unexpected faults: %+v", f)
	}
}

func TestCrashOnEverySaveEventuallyCompletes(t *testing.T) {
	// Crash on the first save, then resume repeatedly; each resume keeps
	// the previously confirmed ranges and makes progress.
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	ckpt := filepath.Join(dir, "out.ckpt.json")

	for round := 1; round <= 4; round++ {
		var faults *FaultFile
		if round <= 3 {
			// Each round crashes at its first checkpoint save; the resume
			// must keep everything confirmed before the cut point.
			faults = &FaultFile{CrashOnCheckpointSave: 1}
		}
		src := NewLocalSource(41, 80000, "v0", nil)
		d := NewDownloader(src, dst, ckpt, faults)
		d.ChunkSize = 8192
		d.Workers = 2
		err := d.Run(context.Background())
		if round <= 3 {
			if !errors.Is(err, ErrSimulatedCrash) {
				t.Fatalf("round %d: expected crash, got %v", round, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("final round: %v", err)
		}
		requireContent(t, dst, src)
	}
}

func ExampleFaultFile() {
	f := &FaultFile{
		ShortWrites:           []ShortWriteFault{{OnRequest: 2, KeepBytes: 512}},
		VersionChanges:        []VersionChangeFault{{OnRequest: 5, NewSeed: 1}},
		StaleResponses:        []StaleResponseFault{{OnRequest: 1, DelayRequests: 3}},
		CrashOnCheckpointSave: 4,
	}
	fmt.Println(f.ShortWrites[0].KeepBytes, f.CrashOnCheckpointSave)
	// Output: 512 4
}
