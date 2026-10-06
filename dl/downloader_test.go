package dl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	testChunkSize = 64
	testChunks    = 9
	testSize      = testChunkSize*(testChunks-1) + 13 // 525 bytes, ragged tail
)

func makeContent() []byte {
	b := make([]byte, testSize)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

// writeFaultFile serializes f as a JSON fault file and returns its path.
func writeFaultFile(t *testing.T, f *Faults) string {
	t.Helper()
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "faults.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadFaults(t *testing.T, f *Faults) *Faults {
	t.Helper()
	loaded, err := LoadFaults(writeFaultFile(t, f))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

type fixture struct {
	server  *Server
	faults  *Faults
	dest    string
	ckpt    string
	content []byte
}

func newFixture(t *testing.T, f *Faults) *fixture {
	t.Helper()
	dir := t.TempDir()
	faults := loadFaults(t, f)
	content := makeContent()
	return &fixture{
		server:  NewServer(content, testChunkSize, faults),
		faults:  faults,
		dest:    filepath.Join(dir, "out.bin"),
		ckpt:    filepath.Join(dir, "out.ckpt"),
		content: content,
	}
}

func (fx *fixture) downloader(workers int) *Downloader {
	return NewDownloader(fx.server, fx.dest, fx.ckpt, workers, fx.faults)
}

func (fx *fixture) destBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(fx.dest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (fx *fixture) requireDestEqualsServer(t *testing.T) {
	t.Helper()
	got := fx.destBytes(t)
	want := fx.server.Content()
	if len(got) != len(want) {
		t.Fatalf("dest size %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dest differs at byte %d", i)
		}
	}
}

func TestBasicParallel(t *testing.T) {
	fx := newFixture(t, &Faults{})
	if err := fx.downloader(4).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
	if _, err := os.Stat(fx.ckpt); err != nil {
		t.Fatalf("checkpoint missing: %v", err)
	}
}

func TestShortWriteRetried(t *testing.T) {
	fx := newFixture(t, &Faults{ShortWriteOnRequests: []int{2}})
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
	if got := fx.server.Requests(); got != testChunks+1 {
		t.Fatalf("requests = %d, want %d (one retry)", got, testChunks+1)
	}
}

func TestOverlapMergeConsistent(t *testing.T) {
	// Request 2 answers with the whole file: every chunk must be confirmed
	// by the merge, so no further requests are needed.
	fx := newFixture(t, &Faults{ExpandRangeOnRequests: []int{2}})
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
	if got := fx.server.Requests(); got != 2 {
		t.Fatalf("requests = %d, want 2 (merge confirmed the rest)", got)
	}
}

func TestOverlapMismatchFatal(t *testing.T) {
	// Chunk 0 is confirmed by request 1; request 2 expands over it with a
	// flipped first byte: overlapping data disagrees -> fatal.
	fx := newFixture(t, &Faults{
		ExpandRangeOnRequests: []int{2},
		CorruptOnRequests:     []int{2},
	})
	err := fx.downloader(1).Run(context.Background())
	if !errors.Is(err, ErrInconsistentOverlap) {
		t.Fatalf("err = %v, want ErrInconsistentOverlap", err)
	}
}

func TestVersionChangeInvalidatesRound(t *testing.T) {
	fx := newFixture(t, &Faults{VersionChangeAfterRequests: 2})
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t) // equals the NEW entity version
	if got := fx.server.Requests(); got <= testChunks {
		t.Fatalf("requests = %d, want > %d (round restarted)", got, testChunks)
	}
}

func TestStaleLateResponseIgnored(t *testing.T) {
	fx := newFixture(t, &Faults{
		VersionChangeAfterRequests: 2,
		StaleReplayAfterRequests:   5,
	})
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
}

func TestCheckpointCrashBeforeRename(t *testing.T) {
	fx := newFixture(t, &Faults{
		CrashPoint:        "before_rename",
		CrashOnCheckpoint: 2,
	})
	if err := fx.downloader(1).Run(context.Background()); !errors.Is(err, ErrCrash) {
		t.Fatalf("err = %v, want ErrCrash", err)
	}
	// The previous checkpoint (1 confirmed chunk) survived the crash.
	data, err := os.ReadFile(fx.ckpt)
	if err != nil {
		t.Fatalf("checkpoint lost: %v", err)
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("checkpoint corrupted: %v", err)
	}
	if len(cp.Confirmed) != 1 {
		t.Fatalf("checkpoint has %d confirmed chunks, want 1", len(cp.Confirmed))
	}
	// Restart with a fresh downloader on the same paths.
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
	if _, err := os.Stat(fx.ckpt + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp checkpoint left behind")
	}
}

func TestCheckpointCrashAfterRename(t *testing.T) {
	fx := newFixture(t, &Faults{
		CrashPoint:        "after_rename",
		CrashOnCheckpoint: 1,
	})
	if err := fx.downloader(1).Run(context.Background()); !errors.Is(err, ErrCrash) {
		t.Fatalf("err = %v, want ErrCrash", err)
	}
	// The rename already happened: the new checkpoint is fully intact.
	data, err := os.ReadFile(fx.ckpt)
	if err != nil {
		t.Fatalf("checkpoint lost: %v", err)
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("checkpoint corrupted: %v", err)
	}
	if len(cp.Confirmed) != 1 {
		t.Fatalf("checkpoint has %d confirmed chunks, want 1", len(cp.Confirmed))
	}
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
}

func TestResumeRejectsCorruptedChunk(t *testing.T) {
	fx := newFixture(t, &Faults{})
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Corrupt chunk 1 on disk behind the checkpoint's back.
	f, err := os.OpenFile(fx.dest, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xDE, 0xAD, 0xBE, 0xEF}, testChunkSize); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Restart: verification must drop chunk 1 and refetch exactly it.
	if err := fx.downloader(1).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.requireDestEqualsServer(t)
	if got := fx.server.Requests(); got != testChunks+1 {
		t.Fatalf("requests = %d, want %d (one refetch)", got, testChunks+1)
	}
}
