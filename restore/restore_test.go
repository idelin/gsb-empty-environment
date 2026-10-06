package restore

import (
	"reflect"
	"testing"
)

func mustStage(t *testing.T, r *Restorer, e Entry) {
	t.Helper()
	if err := r.Stage(e); err != nil {
		t.Fatalf("Stage(%s) failed: %v", e.Path, err)
	}
}

func stageErr(t *testing.T, r *Restorer, e Entry) error {
	t.Helper()
	err := r.Stage(e)
	if err == nil {
		t.Fatalf("Stage(%s) succeeded, want error", e.Path)
	}
	return err
}

func asPathError(t *testing.T, err error) *PathError {
	t.Helper()
	pe, ok := err.(*PathError)
	if !ok {
		t.Fatalf("error %v (%T) is not a *PathError", err, err)
	}
	return pe
}

func commitErr(t *testing.T, r *Restorer) error {
	t.Helper()
	snap, err := r.Commit()
	if err == nil {
		t.Fatalf("Commit succeeded with %d nodes, want error", snap.Len())
	}
	if snap != nil {
		t.Fatalf("failed Commit published a snapshot with %d nodes", snap.Len())
	}
	return err
}

// A hardlink may be staged before its target appears in the archive; both
// resolve to one shared inode at commit time.
func TestForwardReference(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "bin/tool", Kind: KindHardlink, Target: "lib/real"})
	mustStage(t, r, Entry{Path: "lib/real", Kind: KindFile, Content: []byte("payload")})

	snap, err := r.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	link, ok := snap.Node("bin/tool")
	if !ok {
		t.Fatal("bin/tool missing from snapshot")
	}
	base, ok := snap.Node("lib/real")
	if !ok {
		t.Fatal("lib/real missing from snapshot")
	}
	if link.Inode != base.Inode {
		t.Fatalf("inodes differ: link=%d base=%d", link.Inode, base.Inode)
	}
	if !snap.Shared(link.Inode) {
		t.Fatal("inode not marked shared")
	}
	if got := snap.InodePaths(link.Inode); !reflect.DeepEqual(got, []string{"bin/tool", "lib/real"}) {
		t.Fatalf("InodePaths = %v", got)
	}
	if string(link.Content) != "payload" {
		t.Fatalf("link content = %q", link.Content)
	}
	if link.Kind != KindFile || link.LinkFrom != "lib/real" {
		t.Fatalf("resolved link node = %+v", link)
	}
}

// Hardlink chains resolve transitively to a single shared inode.
func TestHardlinkChain(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "a", Kind: KindHardlink, Target: "b"})
	mustStage(t, r, Entry{Path: "b", Kind: KindHardlink, Target: "c"})
	mustStage(t, r, Entry{Path: "c", Kind: KindFile, Content: []byte("x")})

	snap, err := r.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	a, _ := snap.Node("a")
	b, _ := snap.Node("b")
	c, _ := snap.Node("c")
	if a.Inode != c.Inode || b.Inode != c.Inode {
		t.Fatalf("chain not shared: a=%d b=%d c=%d", a.Inode, b.Inode, c.Inode)
	}
	if got := snap.InodePaths(c.Inode); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("InodePaths = %v", got)
	}
}

// Hardlink cycles are rejected and no partial tree is published.
func TestHardlinkCycleRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []Entry
	}{
		{"self", []Entry{{Path: "a", Kind: KindHardlink, Target: "a"}}},
		{"pair", []Entry{
			{Path: "a", Kind: KindHardlink, Target: "b"},
			{Path: "b", Kind: KindHardlink, Target: "a"},
		}},
		{"triangle", []Entry{
			{Path: "a", Kind: KindHardlink, Target: "b"},
			{Path: "b", Kind: KindHardlink, Target: "c"},
			{Path: "c", Kind: KindHardlink, Target: "a"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			for _, e := range tc.entries {
				mustStage(t, r, e)
			}
			if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrHardlinkCycle {
				t.Fatalf("err = %v, want ErrHardlinkCycle", pe.Err)
			}
		})
	}
}

// A hardlink whose target is a directory is rejected, even when the directory
// is only implied by another entry's path.
func TestHardlinkToDirectoryRejected(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "dir/file", Kind: KindFile, Content: []byte("x")})
	mustStage(t, r, Entry{Path: "link", Kind: KindHardlink, Target: "dir"})
	if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrHardlinkToDir {
		t.Fatalf("err = %v, want ErrHardlinkToDir", pe.Err)
	}

	r.Reset()
	mustStage(t, r, Entry{Path: "dir", Kind: KindDir})
	mustStage(t, r, Entry{Path: "link", Kind: KindHardlink, Target: "dir"})
	if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrHardlinkToDir {
		t.Fatalf("explicit dir: err = %v, want ErrHardlinkToDir", pe.Err)
	}
}

// Symlinks are independent content nodes: a hardlink cannot adopt one.
func TestHardlinkToSymlinkRejected(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "s", Kind: KindSymlink, Target: "/etc/hostname"})
	mustStage(t, r, Entry{Path: "h", Kind: KindHardlink, Target: "s"})
	if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrHardlinkToSymlink {
		t.Fatalf("err = %v, want ErrHardlinkToSymlink", pe.Err)
	}
}

func TestDanglingTargetRejected(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "h", Kind: KindHardlink, Target: "missing"})
	if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrDanglingTarget {
		t.Fatalf("err = %v, want ErrDanglingTarget", pe.Err)
	}
}

// A failed commit publishes nothing and leaves staged entries intact, so the
// caller can amend the set and retry.
func TestFailureRollback(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "ok/file", Kind: KindFile, Content: []byte("good")})
	mustStage(t, r, Entry{Path: "ok/alias", Kind: KindHardlink, Target: "later/file"})

	before := r.Len()
	if pe := asPathError(t, commitErr(t, r)); pe.Err != ErrDanglingTarget {
		t.Fatalf("err = %v, want ErrDanglingTarget", pe.Err)
	}
	if r.Len() != before {
		t.Fatalf("staged entries lost on rollback: %d -> %d", before, r.Len())
	}

	// Amend with the previously missing target; the retry succeeds and the
	// forward reference resolves.
	mustStage(t, r, Entry{Path: "later/file", Kind: KindFile, Content: []byte("late")})
	snap, err := r.Commit()
	if err != nil {
		t.Fatalf("retry Commit failed: %v", err)
	}
	alias, _ := snap.Node("ok/alias")
	base, _ := snap.Node("later/file")
	if alias.Inode != base.Inode {
		t.Fatalf("forward reference not resolved: alias=%d base=%d", alias.Inode, base.Inode)
	}
}

func TestDuplicatePathConflict(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "a", Kind: KindFile})
	if pe := asPathError(t, stageErr(t, r, Entry{Path: "a", Kind: KindFile})); pe.Err != ErrDuplicatePath {
		t.Fatalf("err = %v, want ErrDuplicatePath", pe.Err)
	}
	// Cleaned duplicates are also caught.
	if pe := asPathError(t, stageErr(t, r, Entry{Path: "./a", Kind: KindDir})); pe.Err != ErrDuplicatePath {
		t.Fatalf("err = %v, want ErrDuplicatePath", pe.Err)
	}
}

func TestPrefixConflict(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "f", Kind: KindFile})
	if pe := asPathError(t, stageErr(t, r, Entry{Path: "f/child", Kind: KindFile})); pe.Err != ErrPathConflict {
		t.Fatalf("err = %v, want ErrPathConflict", pe.Err)
	}

	r.Reset()
	mustStage(t, r, Entry{Path: "d/child", Kind: KindFile}) // implies dir "d"
	if pe := asPathError(t, stageErr(t, r, Entry{Path: "d", Kind: KindFile})); pe.Err != ErrPathConflict {
		t.Fatalf("err = %v, want ErrPathConflict", pe.Err)
	}
	// Upgrading the implicit dir to an explicit one is fine.
	mustStage(t, r, Entry{Path: "d", Kind: KindDir})
}

func TestInvalidPathsRejected(t *testing.T) {
	for _, p := range []string{"", "/abs", "../escape", "a/../../escape", "back\\slash", "."} {
		r := New()
		if pe := asPathError(t, stageErr(t, r, Entry{Path: p, Kind: KindFile})); pe.Err != ErrInvalidPath {
			t.Fatalf("path %q: err = %v, want ErrInvalidPath", p, pe.Err)
		}
	}
	r := New()
	if pe := asPathError(t, stageErr(t, r, Entry{Path: "h", Kind: KindHardlink, Target: "/abs"})); pe.Err != ErrInvalidPath {
		t.Fatalf("target: err = %v, want ErrInvalidPath", pe.Err)
	}
}

// Symlinks with identical targets are never merged; each is independent
// content with its own inode.
func TestSymlinksIndependent(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "s1", Kind: KindSymlink, Target: "/etc/hostname"})
	mustStage(t, r, Entry{Path: "s2", Kind: KindSymlink, Target: "/etc/hostname"})
	mustStage(t, r, Entry{Path: "f", Kind: KindFile, Content: []byte("/etc/hostname")})

	snap, err := r.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	s1, _ := snap.Node("s1")
	s2, _ := snap.Node("s2")
	f, _ := snap.Node("f")
	if s1.Inode == s2.Inode || s1.Inode == f.Inode || s2.Inode == f.Inode {
		t.Fatalf("inodes merged: s1=%d s2=%d f=%d", s1.Inode, s2.Inode, f.Inode)
	}
	if s1.Target != "/etc/hostname" || s1.Kind != KindSymlink {
		t.Fatalf("symlink node = %+v", s1)
	}
	for _, ino := range []uint64{s1.Inode, s2.Inode, f.Inode} {
		if snap.Shared(ino) {
			t.Fatalf("inode %d unexpectedly shared", ino)
		}
	}
}

// The inode reference graph reflects forward references, chains, and
// independent nodes across a mixed archive.
func TestInodeReferenceGraph(t *testing.T) {
	r := New()
	mustStage(t, r, Entry{Path: "bin/app", Kind: KindHardlink, Target: "opt/app/real"})
	mustStage(t, r, Entry{Path: "bin/app2", Kind: KindHardlink, Target: "bin/app"})
	mustStage(t, r, Entry{Path: "opt/app/real", Kind: KindFile, Content: []byte("bin")})
	mustStage(t, r, Entry{Path: "etc/conf", Kind: KindSymlink, Target: "/default/conf"})

	snap, err := r.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	graph := snap.Graph()
	var shared, solo int
	for _, paths := range graph {
		if len(paths) > 1 {
			shared++
			if !reflect.DeepEqual(paths, []string{"bin/app", "bin/app2", "opt/app/real"}) {
				t.Fatalf("shared group = %v", paths)
			}
		} else {
			solo++
		}
	}
	if shared != 1 {
		t.Fatalf("shared groups = %d, want 1", shared)
	}
	// Solo inodes: symlink, implicit dirs "bin" and "opt/app" and "opt" and "etc".
	if solo != 5 {
		t.Fatalf("solo inodes = %d, want 5; graph=%v", solo, graph)
	}
	if snap.Len() != 8 {
		t.Fatalf("snapshot nodes = %d, want 8", snap.Len())
	}
}
