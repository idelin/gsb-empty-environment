package hostlock

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess runs in a child process (re-executing the test binary).
// It acquires the lock and then "crashes" (exit 2) without releasing.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("HOSTLOCK_HELPER") != "1" {
		return
	}
	dir := os.Getenv("HOSTLOCK_DIR")
	lease, err := time.ParseDuration(os.Getenv("HOSTLOCK_LEASE"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad lease:", err)
		os.Exit(1)
	}
	l, err := New(Options{Dir: dir, Name: "mp", Lease: lease})
	if err != nil {
		fmt.Fprintln(os.Stderr, "New:", err)
		os.Exit(1)
	}
	h, err := l.TryAcquire()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acquire:", err)
		os.Exit(1)
	}
	fmt.Printf("token=%d pid=%d\n", h.Token(), os.Getpid())
	os.Exit(2) // crash: no Release, no deferred cleanup
}

// TestCrossProcessCrashRecovery proves that a lock held by a crashed OS
// process is first reported lost, becomes stale at lease expiry, and is then
// taken over with a strictly larger fencing token.
func TestCrossProcessCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	lease := 700 * time.Millisecond

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(),
		"HOSTLOCK_HELPER=1",
		"HOSTLOCK_DIR="+dir,
		"HOSTLOCK_LEASE="+lease.String(),
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("helper should crash with exit 2, got err=%v out=%s", runErr, out.String())
	}
	var childToken uint64
	var childPID int
	if n, _ := fmt.Sscanf(out.String(), "token=%d pid=%d\n", &childToken, &childPID); n != 2 {
		t.Fatalf("cannot parse helper output %q", out.String())
	}

	l, err := New(Options{Dir: dir, Name: "mp", Lease: lease})
	if err != nil {
		t.Fatal(err)
	}

	// Lease still valid, owner process dead: lost, and untakeable.
	rec, status, err := l.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusLost {
		t.Fatalf("status = %v, want lost (crashed pid %d)", status, childPID)
	}
	if rec.PID != childPID || rec.Token != childToken {
		t.Fatalf("owner record = %+v, want pid=%d token=%d", rec, childPID, childToken)
	}
	if _, err := l.TryAcquire(); !errors.Is(err, ErrHeld) {
		t.Fatalf("TryAcquire while lost = %v, want ErrHeld", err)
	}

	// After the lease lapses: stale, then takeover with token+1.
	time.Sleep(lease + 150*time.Millisecond)
	if _, status, _ := l.Inspect(); status != StatusStale {
		t.Fatalf("status after expiry = %v, want stale", status)
	}
	h, err := l.TryAcquire()
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if h.Token() != childToken+1 {
		t.Fatalf("takeover token = %d, want %d", h.Token(), childToken+1)
	}
	defer h.Release()

	// The token file survived the crash and keeps the sequence monotonic.
	data, err := os.ReadFile(filepath.Join(dir, "mp.token"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if persisted != h.Token() {
		t.Fatalf("persisted token = %d, want %d", persisted, h.Token())
	}
}
