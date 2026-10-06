package cleaner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLogTree builds /app/logs with two files in a fresh MemFS.
func newLogTree(t *testing.T) *MemFS {
	t.Helper()
	m := NewMemFS()
	if err := m.MkdirAll("/app/logs"); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/app/logs/a.log", []byte("aaa")); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/app/logs/b.log", []byte("bb")); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustPlan(t *testing.T, fsys FS, roots ...string) *Plan {
	t.Helper()
	plan, err := Planner{FS: fsys}.BuildPlan(roots...)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return plan
}

func findSkip(rep *Report, path string) (SkipReason, bool) {
	for _, s := range rep.Skipped {
		if s.Path == path {
			return s.Reason, true
		}
	}
	return "", false
}

func findFailure(rep *Report, path string) bool {
	for _, f := range rep.Failed {
		if f.Path == path {
			return true
		}
	}
	return false
}

func isDeleted(rep *Report, path string) bool {
	for _, p := range rep.Deleted {
		if p == path {
			return true
		}
	}
	return false
}

func mustNotExist(t *testing.T, fsys FS, path string) {
	t.Helper()
	if _, err := fsys.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected %s to be gone, stat err=%v", path, err)
	}
}

func mustExist(t *testing.T, fsys FS, path string) FileInfo {
	t.Helper()
	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	return info
}

func TestPreviewFreezesPlan(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	preview := plan.Preview()
	for _, want := range []string{"/app/logs", "/app/logs/a.log", "/app/logs/b.log"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %s:\n%s", want, preview)
		}
	}
	if len(plan.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(plan.Entries))
	}
	// Children must come before their parent directory.
	if plan.Entries[len(plan.Entries)-1].Path != "/app/logs" {
		t.Errorf("parent dir should be planned last, got %v", plan.Entries)
	}
}

func TestExecuteUnchangedPlan(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	rep := Executor{FS: m}.Execute(plan)
	if !rep.OK() {
		t.Fatalf("expected clean run, got:\n%s", rep)
	}
	if len(rep.Deleted) != 3 {
		t.Fatalf("expected 3 deletions, got %v", rep.Deleted)
	}
	mustNotExist(t, m, "/app/logs")
	mustExist(t, m, "/app")
}

func TestPreviewThenReplace(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	// The file is rewritten after the plan was frozen.
	if err := m.WriteFile("/app/logs/a.log", []byte("replaced-content")); err != nil {
		t.Fatal(err)
	}

	rep := Executor{FS: m}.Execute(plan)

	if reason, ok := findSkip(rep, "/app/logs/a.log"); !ok || reason != ReasonChanged {
		t.Errorf("a.log should be skipped as changed, got %v %v", reason, ok)
	}
	if !isDeleted(rep, "/app/logs/b.log") {
		t.Errorf("b.log should be deleted")
	}
	if reason, ok := findSkip(rep, "/app/logs"); !ok || reason != ReasonNotEmpty {
		t.Errorf("logs dir should be skipped as not-empty, got %v %v", reason, ok)
	}
	data, err := m.ReadFile("/app/logs/a.log")
	if err != nil || string(data) != "replaced-content" {
		t.Errorf("replaced file must survive, got %q err=%v", data, err)
	}
}

func TestMoveAndStalePath(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	// Move a planned file away, then drop a brand-new file at its old path.
	if err := m.MkdirAll("/archive"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/app/logs/a.log", "/archive/a.log"); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/app/logs/a.log", []byte("impostor")); err != nil {
		t.Fatal(err)
	}

	rep := Executor{FS: m}.Execute(plan)

	// The new file at the stale path must not be deleted.
	if reason, ok := findSkip(rep, "/app/logs/a.log"); !ok || reason != ReasonChanged {
		t.Errorf("stale path should be skipped as changed, got %v %v", reason, ok)
	}
	data, err := m.ReadFile("/app/logs/a.log")
	if err != nil || string(data) != "impostor" {
		t.Errorf("new file at old path must survive, got %q err=%v", data, err)
	}
	// The moved-away file keeps its content and is untouched by the plan.
	data, err = m.ReadFile("/archive/a.log")
	if err != nil || string(data) != "aaa" {
		t.Errorf("moved file must survive at new location, got %q err=%v", data, err)
	}
	if !isDeleted(rep, "/app/logs/b.log") {
		t.Errorf("b.log should be deleted")
	}
	if reason, ok := findSkip(rep, "/app/logs"); !ok || reason != ReasonNotEmpty {
		t.Errorf("logs dir should be skipped as not-empty, got %v %v", reason, ok)
	}
}

func TestMovedAwayFileReportedGone(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	if err := m.MkdirAll("/archive"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/app/logs/a.log", "/archive/a.log"); err != nil {
		t.Fatal(err)
	}

	rep := Executor{FS: m}.Execute(plan)

	if reason, ok := findSkip(rep, "/app/logs/a.log"); !ok || reason != ReasonGone {
		t.Errorf("moved-away file should be reported gone, got %v %v", reason, ok)
	}
	// b.log deleted, dir ends up empty and is removed too.
	if !isDeleted(rep, "/app/logs") {
		t.Errorf("empty logs dir should be deleted")
	}
	mustNotExist(t, m, "/app/logs")
}

func TestPartialFailure(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	boom := errors.New("simulated I/O error")
	m.FailRemove("/app/logs/a.log", boom)

	rep := Executor{FS: m}.Execute(plan)

	if !findFailure(rep, "/app/logs/a.log") {
		t.Errorf("a.log should be reported as failed, got:\n%s", rep)
	}
	if !isDeleted(rep, "/app/logs/b.log") {
		t.Errorf("b.log should still be deleted despite sibling failure")
	}
	// The failed file remains, so the directory must survive as not-empty.
	if reason, ok := findSkip(rep, "/app/logs"); !ok || reason != ReasonNotEmpty {
		t.Errorf("logs dir should be skipped as not-empty, got %v %v", reason, ok)
	}
	mustExist(t, m, "/app/logs/a.log")
	mustExist(t, m, "/app/logs")
}

func TestFrozenPlanIgnoresNewFiles(t *testing.T) {
	m := newLogTree(t)
	plan := mustPlan(t, m, "/app/logs")

	// A new file appears inside a planned directory after the freeze.
	if err := m.WriteFile("/app/logs/new.log", []byte("fresh")); err != nil {
		t.Fatal(err)
	}

	rep := Executor{FS: m}.Execute(plan)

	if !isDeleted(rep, "/app/logs/a.log") || !isDeleted(rep, "/app/logs/b.log") {
		t.Errorf("planned files should be deleted, got %v", rep.Deleted)
	}
	data, err := m.ReadFile("/app/logs/new.log")
	if err != nil || string(data) != "fresh" {
		t.Errorf("unplanned new file must survive, got %q err=%v", data, err)
	}
	if reason, ok := findSkip(rep, "/app/logs"); !ok || reason != ReasonNotEmpty {
		t.Errorf("logs dir should be skipped as not-empty, got %v %v", reason, ok)
	}
}

func TestOSFSRoundTrip(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cache")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "old.tmp"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys := OSFS{}
	plan := mustPlan(t, fsys, sub)

	// Races after the freeze: rewrite the planned file, add a new one.
	if err := os.WriteFile(filepath.Join(sub, "old.tmp"), []byte("rewritten!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "new.tmp"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := Executor{FS: fsys}.Execute(plan)

	if reason, ok := findSkip(rep, filepath.Join(sub, "old.tmp")); !ok || reason != ReasonChanged {
		t.Errorf("rewritten file should be skipped as changed, got %v %v", reason, ok)
	}
	if reason, ok := findSkip(rep, sub); !ok || reason != ReasonNotEmpty {
		t.Errorf("dir should be skipped as not-empty, got %v %v", reason, ok)
	}
	for _, name := range []string{"old.tmp", "new.tmp"} {
		if _, err := os.Stat(filepath.Join(sub, name)); err != nil {
			t.Errorf("%s must survive: %v", name, err)
		}
	}
}

func TestOSFSUnchangedDeletesAll(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cache")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested", "f.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys := OSFS{}
	plan := mustPlan(t, fsys, sub)
	rep := Executor{FS: fsys}.Execute(plan)

	if !rep.OK() {
		t.Fatalf("expected clean run, got:\n%s", rep)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("cache dir should be gone, err=%v", err)
	}
}
