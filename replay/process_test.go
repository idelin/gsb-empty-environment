package replay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	envReplayWorker = "REPLAY_TEST_WORKER"
	envReplayLog    = "REPLAY_TEST_LOG"
	envReplayRole   = "REPLAY_TEST_ROLE"
	envReplayTime   = "REPLAY_TEST_TIME"
)

// TestReexecProcessAcceptance is the re-exec entry point. Every spawned test
// binary process runs this first and exits without running the parent suite.
func TestReexecProcessAcceptance(t *testing.T) {
	if os.Getenv(envReplayWorker) == "" {
		t.Skip("worker mode only")
	}

	logPath := os.Getenv(envReplayLog)
	role := os.Getenv(envReplayRole)
	clientUnixNano, err := strconv.ParseInt(os.Getenv(envReplayTime), 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad client time: %v\n", err)
		os.Exit(2)
	}
	clientTime := time.Unix(0, clientUnixNano)

	ctx := context.Background()
	storage, err := OpenFileStorage(FileConfig{Path: logPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open storage: %v\n", err)
		os.Exit(2)
	}
	cache, err := New(Config{
		Storage:         storage,
		Clock:           WallClock{},
		PastWindow:      time.Hour,
		FutureTolerance: time.Hour,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "new cache: %v\n", err)
		os.Exit(2)
	}

	switch role {
	case "shared":
		// All processes present the identical key: exactly one may succeed.
		key := Key{
			ClientPrincipal:  "alice@REALM",
			ServicePrincipal: "host/server@REALM",
			TicketDigest:     []byte{0x10, 0x20, 0x30, 0x40},
			ClientTime:       clientTime,
			Sequence:         1,
		}
		switch err := cache.Accept(ctx, key); {
		case err == nil:
			fmt.Println("ACCEPTED")
		case errors.Is(err, ErrReplay):
			fmt.Println("REPLAY")
		default:
			fmt.Fprintf(os.Stderr, "unexpected accept error: %v\n", err)
			os.Exit(2)
		}

	case "unique":
		// Each process presents a distinct key: every key must be accepted
		// exactly once, proving the log serializes writers without dropping or
		// duplicating records.
		pid := os.Getpid()
		key := Key{
			ClientPrincipal:  fmt.Sprintf("worker-%d@REALM", pid),
			ServicePrincipal: "host/server@REALM",
			TicketDigest:     []byte{0x50, 0x60, 0x70, byte(pid), byte(pid >> 8)},
			ClientTime:       clientTime,
			Sequence:         uint32(pid),
		}
		if err := cache.Accept(ctx, key); err != nil {
			fmt.Fprintf(os.Stderr, "unique key rejected: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("ACCEPTED %s\n", key.ID())

	default:
		fmt.Fprintf(os.Stderr, "unknown role %q\n", role)
		os.Exit(2)
	}

	if err := cache.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close: %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

func spawnWorkers(t *testing.T, logPath, role string, workers int) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("multi-process test skipped in -short mode")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	clientTime := strconv.FormatInt(time.Now().UnixNano(), 10)

	type outcome struct {
		pid    int
		output string
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(exe, "-test.run", "TestReexecProcessAcceptance", "-test.v")
			cmd.Env = append(os.Environ(),
				envReplayWorker+"=1",
				envReplayLog+"="+logPath,
				envReplayRole+"="+role,
				envReplayTime+"="+clientTime,
			)
			<-start
			output, err := cmd.CombinedOutput()
			results <- outcome{output: string(output), err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var lines []string
	for result := range results {
		if result.err != nil {
			t.Fatalf("worker failed: %v\noutput:\n%s", result.err, result.output)
		}
		for _, line := range strings.Split(result.output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ACCEPTED") || line == "REPLAY" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func TestConcurrentProcessesSharedKey(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "shared-replay.log")
	const workers = 8

	outcomes := spawnWorkers(t, logPath, "shared", workers)
	if len(outcomes) != workers {
		t.Fatalf("got %d worker outcomes, want %d", len(outcomes), workers)
	}

	var accepted, replay int
	for _, outcome := range outcomes {
		switch outcome {
		case "ACCEPTED":
			accepted++
		case "REPLAY":
			replay++
		default:
			t.Fatalf("unexpected worker outcome %q", outcome)
		}
	}
	if accepted != 1 || replay != workers-1 {
		t.Fatalf("accepted=%d replay=%d, want 1 and %d", accepted, replay, workers-1)
	}

	storage, err := OpenFileStorage(FileConfig{Path: logPath})
	if err != nil {
		t.Fatalf("reopen log: %v", err)
	}
	defer storage.Close()
	ctx := context.Background()
	if err := storage.Lock(ctx); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	records, err := storage.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if entries, watermarks := countEntries(records); entries != 1 || watermarks != 1 {
		t.Fatalf("durable records = %d entries / %d watermarks, want 1/1", entries, watermarks)
	}
}

func TestConcurrentProcessesUniqueKeys(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "unique-replay.log")
	const workers = 8

	outcomes := spawnWorkers(t, logPath, "unique", workers)
	if len(outcomes) != workers {
		t.Fatalf("got %d worker outcomes, want %d", len(outcomes), workers)
	}

	acceptedIDs := make(map[string]bool, workers)
	for _, outcome := range outcomes {
		if !strings.HasPrefix(outcome, "ACCEPTED ") {
			t.Fatalf("unexpected worker outcome %q", outcome)
		}
		id := strings.TrimPrefix(outcome, "ACCEPTED ")
		if acceptedIDs[id] {
			t.Fatalf("key %s accepted by more than one process", id)
		}
		acceptedIDs[id] = true
	}
	if len(acceptedIDs) != workers {
		t.Fatalf("unique accepted keys = %d, want %d", len(acceptedIDs), workers)
	}

	storage, err := OpenFileStorage(FileConfig{Path: logPath})
	if err != nil {
		t.Fatalf("reopen log: %v", err)
	}
	defer storage.Close()
	ctx := context.Background()
	if err := storage.Lock(ctx); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	records, err := storage.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if entries, watermarks := countEntries(records); entries != workers || watermarks != workers {
		t.Fatalf("durable records = %d entries / %d watermarks, want %d/%d",
			entries, watermarks, workers, workers)
	}

	// Every watermark in the shared log must be non-decreasing even though the
	// appends came from independent processes.
	var maxBound int64
	for _, rec := range records {
		if rec.Kind == KindWatermark {
			if rec.Watermark.LowerBoundUnixNano < maxBound {
				t.Fatal("watermark regressed across processes")
			}
			maxBound = rec.Watermark.LowerBoundUnixNano
		}
	}
}

func TestFileStorageReopensAndRejectsReplay(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	logPath := filepath.Join(t.TempDir(), "restart.log")

	first, err := OpenFileStorage(FileConfig{Path: logPath})
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	cache := newTestCache(t, first, clock)
	key := testKey(clock, 1)
	if err := cache.Accept(ctx, key); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second, err := OpenFileStorage(FileConfig{Path: logPath})
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	restarted := newTestCache(t, second, clock)
	if err := restarted.Accept(ctx, key); !errors.Is(err, ErrReplay) {
		t.Fatalf("restart Accept = %v, want ErrReplay", err)
	}
}
