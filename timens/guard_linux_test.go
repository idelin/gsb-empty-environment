//go:build linux

package timens

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeProc stages a minimal /proc tree:
//
//	<root>/<pid>/ns/time -> "time:[<inode>]"
//	<root>/self/status    -> CapEff line
func fakeProc(t *testing.T, tasks map[int]uint64, capEff string) string {
	t.Helper()
	root := t.TempDir()
	for pid, inode := range tasks {
		nsDir := filepath.Join(root, fmt.Sprint(pid), "ns")
		if err := os.MkdirAll(nsDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		link := filepath.Join(nsDir, "time")
		if err := os.Symlink(fmt.Sprintf("time:[%d]", inode), link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}

	selfDir := filepath.Join(root, "self")
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatalf("mkdir self: %v", err)
	}
	status := fmt.Sprintf("Name:\tinit\nCapInh:\t0000000000000000\nCapEff:\t%s\n", capEff)
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o600); err != nil {
		t.Fatalf("write status: %v", err)
	}
	return root
}

func TestProcfsGuardEmptyNamespace(t *testing.T) {
	root := fakeProc(t, map[int]uint64{
		1: 4000, // holder in its own fresh namespace
		2: 5000, // unrelated process in the host namespace
	}, "0000000000000000")
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })

	guard := NewProcfsGuard()
	if err := guard.Empty(filepath.Join(root, "1/timens_offsets")); err != nil {
		t.Fatalf("empty namespace should pass: %v", err)
	}
}

func TestProcfsGuardRejectsNamespaceWithTasks(t *testing.T) {
	root := fakeProc(t, map[int]uint64{
		1: 4000, // holder
		7: 4000, // second member: offsets were configured too late
		8: 5000,
	}, "0000000000000000")
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })

	err := NewProcfsGuard().Empty(filepath.Join(root, "1/timens_offsets"))
	if !errors.Is(err, ErrNamespaceNotWritable) {
		t.Fatalf("expected ErrNamespaceNotWritable, got %v", err)
	}
}

func TestHolderPID(t *testing.T) {
	pid, err := holderPID("/proc/42/timens_offsets")
	if err != nil || pid != 42 {
		t.Fatalf("holderPID = (%d, %v), want 42", pid, err)
	}
	if _, err := holderPID("/proc/42/cmdline"); err == nil {
		t.Fatal("expected error for non-timens_offsets path")
	}
}

func TestStatusCapChecker(t *testing.T) {
	root := fakeProc(t, map[int]uint64{1: 1}, fmt.Sprintf("%016x", 1<<capSysTime))
	checker := &StatusCapChecker{procSelfStatus: filepath.Join(root, "self/status")}
	has, err := checker.HasSysTime()
	if err != nil || !has {
		t.Fatalf("HasSysTime = (%v, %v), want true", has, err)
	}

	rootNoCap := fakeProc(t, map[int]uint64{1: 1}, "0000000000000000")
	checkerNoCap := &StatusCapChecker{procSelfStatus: filepath.Join(rootNoCap, "self/status")}
	has, err = checkerNoCap.HasSysTime()
	if err != nil || has {
		t.Fatalf("HasSysTime = (%v, %v), want false", has, err)
	}
}
