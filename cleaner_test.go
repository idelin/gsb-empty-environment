package cleaner

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func skipReason(rep Report, path string) (SkipReason, bool) {
	for _, s := range rep.Skipped {
		if s.Path == path {
			return s.Reason, true
		}
	}
	return "", false
}

func TestExecuteRemovesEverythingUnchanged(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root/sub")
	m.WriteFile("/root/a.txt", []byte("a"))
	m.WriteFile("/root/sub/b.txt", []byte("bb"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	rep := Execute(m, plan)

	if len(rep.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", rep.Skipped)
	}
	want := []string{"/root/a.txt", "/root/sub/b.txt", "/root/sub", "/root"}
	if !slices.Equal(rep.Removed, want) {
		t.Fatalf("removed = %v, want %v", rep.Removed, want)
	}
	if m.Exists("/root") {
		t.Fatal("/root should be gone")
	}
}

// Preview-then-replace: a file swapped for a new inode after planning must
// not be deleted by its old path, and its parent directory must survive
// because it is no longer empty.
func TestPreviewThenReplace(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.WriteFile("/root/a.txt", []byte("old"))
	m.WriteFile("/root/b.txt", []byte("keep"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.ReplaceFile("/root/a.txt", []byte("brand new content"))

	rep := Execute(m, plan)

	if reason, ok := skipReason(rep, "/root/a.txt"); !ok || reason != ReasonChanged {
		t.Fatalf("a.txt skip = %v %v, want changed", reason, ok)
	}
	if reason, ok := skipReason(rep, "/root"); !ok || reason != ReasonNotEmpty {
		t.Fatalf("/root skip = %v %v, want not-empty", reason, ok)
	}
	data, err := m.Read("/root/a.txt")
	if err != nil || string(data) != "brand new content" {
		t.Fatalf("replaced file lost or corrupted: %q %v", data, err)
	}
	if !slices.Contains(rep.Removed, "/root/b.txt") {
		t.Fatal("unchanged b.txt should be removed")
	}
}

// Move: a planned file renamed away leaves its old path missing; the moved
// file itself must survive because the plan only refers to the old path.
func TestMove(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.MkdirAll("/elsewhere")
	m.WriteFile("/root/a.txt", []byte("data"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.Move("/root/a.txt", "/elsewhere/a.txt")

	rep := Execute(m, plan)

	if reason, ok := skipReason(rep, "/root/a.txt"); !ok || reason != ReasonMissing {
		t.Fatalf("a.txt skip = %v %v, want missing", reason, ok)
	}
	if !slices.Contains(rep.Removed, "/root") {
		t.Fatal("/root should be removed once empty")
	}
	data, err := m.Read("/elsewhere/a.txt")
	if err != nil || string(data) != "data" {
		t.Fatalf("moved file lost or corrupted: %q %v", data, err)
	}
}

// Move-then-recreate: old file moved away and a different file created at
// the planned path; the new file must not be deleted.
func TestMoveThenNewFileAtOldPath(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.WriteFile("/root/a.txt", []byte("data"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.Move("/root/a.txt", "/root/a.txt.bak")
	m.WriteFile("/root/a.txt", []byte("data")) // same size, different inode

	rep := Execute(m, plan)

	if reason, ok := skipReason(rep, "/root/a.txt"); !ok || reason != ReasonChanged {
		t.Fatalf("a.txt skip = %v %v, want changed", reason, ok)
	}
	if !m.Exists("/root/a.txt") || !m.Exists("/root/a.txt.bak") {
		t.Fatal("both the new file and the moved file must survive")
	}
}

// In-place rewrite: same inode but bumped version/size must also be skipped.
func TestInPlaceModification(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.WriteFile("/root/a.txt", []byte("v1"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.WriteFile("/root/a.txt", []byte("v2-longer"))

	rep := Execute(m, plan)

	if reason, ok := skipReason(rep, "/root/a.txt"); !ok || reason != ReasonChanged {
		t.Fatalf("a.txt skip = %v %v, want changed", reason, ok)
	}
	data, _ := m.Read("/root/a.txt")
	if string(data) != "v2-longer" {
		t.Fatalf("modified file lost: %q", data)
	}
}

// Partial failure: one removal fails, the rest of the plan still executes
// and the failure is reported.
func TestPartialFailure(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.WriteFile("/root/a.txt", []byte("a"))
	m.WriteFile("/root/b.txt", []byte("b"))
	m.WriteFile("/root/c.txt", []byte("c"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.FailOps = map[string]error{"/root/b.txt": errors.New("permission denied")}

	rep := Execute(m, plan)

	for _, p := range []string{"/root/a.txt", "/root/c.txt"} {
		if !slices.Contains(rep.Removed, p) {
			t.Fatalf("%s should be removed", p)
		}
	}
	if reason, ok := skipReason(rep, "/root/b.txt"); !ok || reason != ReasonError {
		t.Fatalf("b.txt skip = %v %v, want error", reason, ok)
	}
	if reason, ok := skipReason(rep, "/root"); !ok || reason != ReasonNotEmpty {
		t.Fatalf("/root skip = %v %v, want not-empty", reason, ok)
	}
	if !m.Exists("/root/b.txt") {
		t.Fatal("failed b.txt must survive")
	}
}

// A brand-new file appearing in a planned directory after planning keeps
// the directory alive.
func TestNewFileInPlannedDir(t *testing.T) {
	m := NewMemFS()
	m.MkdirAll("/root")
	m.WriteFile("/root/a.txt", []byte("a"))

	plan, err := BuildPlan(m, "/root")
	if err != nil {
		t.Fatal(err)
	}
	m.WriteFile("/root/new.txt", []byte("surprise"))

	rep := Execute(m, plan)

	if !slices.Contains(rep.Removed, "/root/a.txt") {
		t.Fatal("a.txt should be removed")
	}
	if reason, ok := skipReason(rep, "/root"); !ok || reason != ReasonNotEmpty {
		t.Fatalf("/root skip = %v %v, want not-empty", reason, ok)
	}
	if !m.Exists("/root/new.txt") {
		t.Fatal("unplanned new.txt must survive")
	}
}

// OSFS integration: plan, replace one file on the real filesystem, execute.
func TestOSFSReplace(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "sub", "victim.txt")
	if err := os.WriteFile(victim, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys := OSFS{}
	plan, err := BuildPlan(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	// Atomic replace: new inode at the planned path.
	tmp := victim + ".tmp"
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, victim); err != nil {
		t.Fatal(err)
	}

	rep := Execute(fsys, plan)

	if reason, ok := skipReason(rep, victim); !ok || reason != ReasonChanged {
		t.Fatalf("victim skip = %v %v, want changed", reason, ok)
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "new" {
		t.Fatalf("replaced file lost or corrupted: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); !os.IsNotExist(err) {
		t.Fatal("unchanged keep.txt should be removed")
	}
	// root still holds sub/victim.txt, so it must survive.
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Fatal("root must survive while replaced file remains")
	}
}
