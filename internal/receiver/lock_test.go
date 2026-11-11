package receiver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFileLockBlocksSecondProcess verifies the advisory flock: while one
// process holds the receiver lock, a second process trying to Open the same
// directory blocks until the holder exits. The child is the test binary itself
// re-executed in a special helper mode.
func TestFileLockBlocksSecondProcess(t *testing.T) {
	_, pub := newTestLog(t)
	dir := t.TempDir()

	// Holder takes the lock and keeps it for a couple of seconds.
	holder := exec.Command(os.Args[0], "-test.run", "TestLockHolderProcess")
	holder.Env = append(os.Environ(),
		"RECEIVER_LOCK_TEST=holder",
		"RECEIVER_LOCK_DIR="+dir,
		"RECEIVER_LOCK_WAIT=2s",
	)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}

	// Give the holder time to acquire the lock.
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	waiter := exec.Command(os.Args[0], "-test.run", "TestLockHolderProcess")
	waiter.Env = append(os.Environ(),
		"RECEIVER_LOCK_TEST=waiter",
		"RECEIVER_LOCK_DIR="+dir,
		"RECEIVER_LOCK_WAIT=50ms",
	)
	out, err := waiter.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waiter process failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LOCK_HELD") {
		t.Fatalf("waiter did not report holding the lock: %s", out)
	}
	// The waiter must have blocked for roughly the holder's hold duration.
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("waiter acquired lock after %v; expected to block on flock", elapsed)
	}

	if err := holder.Wait(); err != nil {
		t.Fatal(err)
	}

	// After the holder exits, a fresh in-process receiver opens immediately.
	r, err := Open(dir, testOrigin, pub)
	if err != nil {
		t.Fatalf("Open after holder exit: %v", err)
	}
	defer r.Close()
	if !filepath.IsAbs(r.Dir()) {
		t.Fatal("expected absolute state directory")
	}
}

// TestLockHolderProcess is the re-executed helper. It normally skips; it only
// acts when driven by the environment variables set above.
func TestLockHolderProcess(t *testing.T) {
	if os.Getenv("RECEIVER_LOCK_TEST") == "" {
		t.Skip("helper process only")
	}
	_, pub := newTestLog(t)
	dir := os.Getenv("RECEIVER_LOCK_DIR")
	wait, err := time.ParseDuration(os.Getenv("RECEIVER_LOCK_WAIT"))
	if err != nil {
		t.Fatal(err)
	}

	// This Open blocks if another process currently holds the lock.
	r, err := Open(dir, testOrigin, pub)
	if err != nil {
		t.Fatal(err)
	}
	// Marker consumed by the parent's output assertion.
	fmt.Println("LOCK_HELD")
	time.Sleep(wait)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}
