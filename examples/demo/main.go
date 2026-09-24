// Command demo exercises the hostlock protocol end to end on one host:
// a worker process crashes while holding the lock, the parent observes
// lost -> stale -> takeover, a revived old holder is fenced out, and
// releasing someone else's lock is refused.
//
// Usage: go run ./examples/demo
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hostlock"
)

const (
	lockName = "demo-resource"
	lease    = 1200 * time.Millisecond
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "child":
			runChild(os.Args[2])
		case "zombie":
			runZombie(os.Args[2], os.Args[3])
		}
		return
	}
	runDemo()
}

// runChild acquires the lock, commits once with its fencing token, then
// crashes (exit 2) without releasing.
func runChild(dir string) {
	l := mustLock(dir)
	h, err := l.TryAcquire()
	must(err)
	fmt.Printf("token=%d pid=%d\n", h.Token(), os.Getpid())
	must(hostlock.NewFileFence(fencePath(dir)).Check(h.Token()))
	os.Exit(2) // crash: no Release
}

// runZombie simulates the crashed holder waking up and trying to commit
// with its old, superseded token.
func runZombie(dir, token string) {
	var tok uint64
	fmt.Sscanf(token, "%d", &tok)
	err := hostlock.NewFileFence(fencePath(dir)).Check(tok)
	if errors.Is(err, hostlock.ErrStaleToken) {
		fmt.Printf("zombie commit with old token %d: REJECTED (%v)\n", tok, err)
		return
	}
	fmt.Printf("zombie commit with old token %d: unexpectedly accepted!\n", tok)
	os.Exit(1)
}

func runDemo() {
	dir := filepath.Join(os.TempDir(), "hostlock-demo")
	os.RemoveAll(dir)
	must(os.MkdirAll(dir, 0o755))
	fmt.Printf("lock dir: %s (lease %s)\n\n", dir, lease)

	step("1. worker process acquires the lock, commits, then crashes without releasing")
	childToken := spawn(dir, "child", dir)

	l := mustLock(dir)
	step("2. parent inspects: lease still valid but owner pid is dead -> lost")
	rec, status, _ := l.Inspect()
	fmt.Printf("   status=%s owner=%+v\n", status, *rec)
	_, err := l.TryAcquire()
	fmt.Printf("   TryAcquire while lost: %v\n", err)

	step("3. parent waits out the lease, then takes over the stale lock")
	time.Sleep(lease + 200*time.Millisecond)
	_, status, _ = l.Inspect()
	fmt.Printf("   status after expiry: %s\n", status)
	h, err := l.TryAcquire()
	must(err)
	fmt.Printf("   acquired with fencing token %d (was %d)\n", h.Token(), childToken)
	must(hostlock.NewFileFence(fencePath(dir)).Check(h.Token()))
	fmt.Printf("   commit with token %d: accepted\n", h.Token())

	step("4. crashed worker 'wakes up' and replays its old token")
	spawn(dir, "zombie", dir, fmt.Sprint(childToken))

	step("5. old handle cannot release or commit after takeover")
	must(h.Release())
	h3, err := l.TryAcquire() // another holder generation
	must(err)
	fmt.Printf("   new generation acquired with token %d\n", h3.Token())
	must(hostlock.NewFileFence(fencePath(dir)).Check(h3.Token()))
	fmt.Printf("   old handle Release: %v\n", h.Release())
	fmt.Printf("   old token %d commit: %v\n", h.Token(), hostlock.NewFileFence(fencePath(dir)).Check(h.Token()))
	must(h3.Release())
	_, status, _ = l.Inspect()
	fmt.Printf("\nfinal status: %s\n", status)
}

func step(msg string) { fmt.Printf("\n== %s\n", msg) }

func fencePath(dir string) string { return filepath.Join(dir, "commits.fence") }

func mustLock(dir string) *hostlock.Lock {
	l, err := hostlock.New(hostlock.Options{Dir: dir, Name: lockName, Lease: lease})
	must(err)
	return l
}

func spawn(dir string, args ...string) uint64 {
	cmd := exec.Command(os.Args[0], args...)
	out, err := cmd.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fmt.Printf("   [child] %s\n", line)
	}
	var token uint64
	fmt.Sscanf(string(out), "token=%d", &token)
	if exitErr, ok := err.(*exec.ExitError); ok {
		fmt.Printf("   [child] exited with code %d (crash)\n", exitErr.ExitCode())
	} else if err != nil {
		fmt.Printf("   [child] error: %v\n", err)
	}
	return token
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
