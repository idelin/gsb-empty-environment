package tailfollow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func appendTo(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) collect(ch <-chan string) {
	for line := range ch {
		c.mu.Lock()
		c.lines = append(c.lines, line)
		c.mu.Unlock()
	}
}

func (c *collector) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.lines)
		c.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("timed out waiting for %d lines, got %d: %q", n, len(c.lines), c.lines)
}

func (c *collector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func runFollower(cfg Config) (cancel context.CancelFunc, done chan error, c *collector) {
	fw := New(cfg)
	ctx, cancelFn := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- fw.Run(ctx) }()
	c = &collector{}
	go c.collect(fw.Lines())
	return cancelFn, done, c
}

func stopFollower(t *testing.T, cancel context.CancelFunc, done chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("follower did not stop")
	}
}

// Writes interleaved with rename rotation and copytruncate must deliver
// every line exactly once and in write order.
func TestFollowRotationExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	cfg := Config{
		Path:           logPath,
		CheckpointPath: filepath.Join(dir, "follow.ckpt"),
		PollInterval:   5 * time.Millisecond,
		LineTimeout:    300 * time.Millisecond,
	}
	cancel, done, c := runFollower(cfg)

	// Rename rotation: the follower holds the old file open; lines
	// appended to the rotated file must still be drained before the
	// replacement file is read.
	appendTo(t, logPath, "line-1\nline-2\nline-3\n")
	c.waitFor(t, 3)
	if err := os.Rename(logPath, logPath+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, logPath+".1", "line-3.5\n")
	appendTo(t, logPath, "line-4\nline-5\nline-6\n")
	c.waitFor(t, 7)

	// copytruncate: same inode, content copied aside then truncated.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".2", data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(logPath, 0); err != nil {
		t.Fatal(err)
	}
	// Shorter than the pre-truncate size so the shrink is detectable
	// even if the follower polls only after all writes land.
	appendTo(t, logPath, "l-7\nl-8\nl-9\n")
	c.waitFor(t, 10)

	// Partial line completed before the timeout is delivered whole.
	appendTo(t, logPath, "part-a")
	time.Sleep(50 * time.Millisecond)
	appendTo(t, logPath, "part-b\n")
	c.waitFor(t, 11)

	// A dangling line is flushed after LineTimeout.
	appendTo(t, logPath, "dangling")
	c.waitFor(t, 12)

	stopFollower(t, cancel, done)

	want := []string{
		"line-1", "line-2", "line-3", "line-3.5",
		"line-4", "line-5", "line-6",
		"l-7", "l-8", "l-9",
		"part-apart-b",
		"dangling",
	}
	if got := c.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// A restarted follower resumes from the checkpointed inode and offset:
// no redelivery of emitted lines, and the pending partial line is
// re-read and completed.
func TestRestartResumesFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	cfg := Config{
		Path:           logPath,
		CheckpointPath: filepath.Join(dir, "follow.ckpt"),
		PollInterval:   5 * time.Millisecond,
		LineTimeout:    5 * time.Second,
	}

	cancel1, done1, c1 := runFollower(cfg)
	appendTo(t, logPath, "alpha\nbeta\ngamma-part")
	c1.waitFor(t, 2)
	stopFollower(t, cancel1, done1)

	if got, want := c1.snapshot(), []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase 1 lines: got %q, want %q", got, want)
	}

	ck, err := LoadCheckpoint(cfg.CheckpointPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if ck.ID != fileID(info) {
		t.Fatalf("checkpoint id %+v, want %+v", ck.ID, fileID(info))
	}
	if want := int64(len("alpha\nbeta\n")); ck.Offset != want {
		t.Fatalf("checkpoint offset %d, want %d", ck.Offset, want)
	}

	cancel2, done2, c2 := runFollower(cfg)
	appendTo(t, logPath, "-rest\ndelta\n")
	c2.waitFor(t, 2)
	stopFollower(t, cancel2, done2)

	if got, want := c2.snapshot(), []string{"gamma-part-rest", "delta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase 2 lines: got %q, want %q", got, want)
	}
}

func TestCheckpointSaveIsAtomicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ckpt")
	want := Checkpoint{ID: FileID{Dev: 16777233, Ino: 424242}, Offset: 1234}

	if err := SaveCheckpoint(path, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	got, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	zero, err := LoadCheckpoint(filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if zero != (Checkpoint{}) {
		t.Fatalf("missing checkpoint: got %+v, want zero", zero)
	}
}
