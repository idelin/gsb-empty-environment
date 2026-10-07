package manifest

import (
	"fmt"
	"strings"
	"testing"
)

// scenario bundles an old tree, a target tree, and the new file contents.
type scenario struct {
	name    string
	old     Manifest
	oldData map[string][]byte
	want    Manifest
	newData map[string][]byte
}

func scenarios() []scenario {
	return []scenario{
		{
			name: "empty to populated",
			old:  Manifest{},
			want: Manifest{
				"a.txt":      FileEntry([]byte("aaa")),
				"dir/b.txt":  FileEntry([]byte("bbb")),
				"dir/c.link": LinkEntry("../a.txt"),
			},
			newData: map[string][]byte{"a.txt": []byte("aaa"), "dir/b.txt": []byte("bbb")},
		},
		{
			name: "update remove add",
			old: Manifest{
				"keep.txt":    FileEntry([]byte("same")),
				"change.txt":  FileEntry([]byte("v1")),
				"gone.txt":    FileEntry([]byte("bye")),
				"empty-dir":   DirEntry(),
				"old-dir/f":   FileEntry([]byte("x")),
				"old-dir/sub": DirEntry(),
			},
			oldData: map[string][]byte{
				"keep.txt": []byte("same"), "change.txt": []byte("v1"),
				"gone.txt": []byte("bye"), "old-dir/f": []byte("x"),
			},
			want: Manifest{
				"keep.txt":   FileEntry([]byte("same")),
				"change.txt": FileEntry([]byte("v2")),
				"fresh.txt":  FileEntry([]byte("new")),
			},
			newData: map[string][]byte{"change.txt": []byte("v2"), "fresh.txt": []byte("new")},
		},
		{
			name: "file becomes deep directory",
			old:  Manifest{"x": FileEntry([]byte("old-x")), "sib": FileEntry([]byte("s"))},
			oldData: map[string][]byte{
				"x": []byte("old-x"), "sib": []byte("s"),
			},
			want: Manifest{
				"x/y/z/deep.txt": FileEntry([]byte("deep")),
				"sib":            FileEntry([]byte("s")),
			},
			newData: map[string][]byte{"x/y/z/deep.txt": []byte("deep")},
		},
		{
			name: "directory becomes file",
			old: Manifest{
				"d":       DirEntry(),
				"d/a.txt": FileEntry([]byte("a")),
				"d/e/f":   FileEntry([]byte("f")),
			},
			oldData: map[string][]byte{"d/a.txt": []byte("a"), "d/e/f": []byte("f")},
			want:    Manifest{"d": FileEntry([]byte("flat"))},
			newData: map[string][]byte{"d": []byte("flat")},
		},
		{
			name: "directory becomes symlink",
			old: Manifest{
				"etc":        DirEntry(),
				"etc/motd":   FileEntry([]byte("hi")),
				"etc/skel/g": FileEntry([]byte("g")),
			},
			oldData: map[string][]byte{"etc/motd": []byte("hi"), "etc/skel/g": []byte("g")},
			want:    Manifest{"etc": LinkEntry("/usr/share/etc")},
		},
		{
			name: "symlink becomes directory and file becomes symlink",
			old: Manifest{
				"plug": LinkEntry("/elsewhere"),
				"cfg":  FileEntry([]byte("c1")),
			},
			oldData: map[string][]byte{"cfg": []byte("c1")},
			want: Manifest{
				"plug/hook.sh": FileEntry([]byte("hook")),
				"cfg":          LinkEntry("/run/cfg"),
			},
			newData: map[string][]byte{"plug/hook.sh": []byte("hook")},
		},
		{
			name: "swap file and directory between paths",
			old: Manifest{
				"a":       FileEntry([]byte("file-a")),
				"b":       DirEntry(),
				"b/inner": FileEntry([]byte("inner")),
			},
			oldData: map[string][]byte{"a": []byte("file-a"), "b/inner": []byte("inner")},
			want: Manifest{
				"a/inner": FileEntry([]byte("inner")),
				"b":       FileEntry([]byte("file-b")),
			},
			newData: map[string][]byte{"a/inner": []byte("inner"), "b": []byte("file-b")},
		},
	}
}

func TestExecuteAndVerifyExact(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			plan, err := BuildPlan(sc.old, sc.want, Options{Content: staticContent(sc.newData)})
			if err != nil {
				t.Fatal(err)
			}
			tree := seed(t, sc.old, sc.oldData)
			ex := executor(tree)
			if err := ex.Execute(plan); err != nil {
				t.Fatalf("execute: %v\nplan:\n%s", err, plan)
			}
			if err := Verify(tree, "/tree", sc.want); err != nil {
				t.Fatalf("verify after execute: %v", err)
			}
		})
	}
}

// TestReplayAfterCrash simulates a crash after every possible prefix of the
// plan, then re-executes the whole plan. It must always converge to the
// exact target, and content already written by the plan must survive.
func TestReplayAfterCrash(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			plan, err := BuildPlan(sc.old, sc.want, Options{Content: staticContent(sc.newData)})
			if err != nil {
				t.Fatal(err)
			}
			for crash := 0; crash <= len(plan.Ops); crash++ {
				t.Run(fmt.Sprintf("crash_after_op_%d", crash), func(t *testing.T) {
					tree := seed(t, sc.old, sc.oldData)
					ex := executor(tree)
					for i := 0; i < crash; i++ {
						if err := ex.Apply(plan.Ops[i]); err != nil {
							t.Fatalf("apply op %d: %v", i, err)
						}
					}
					// Re-execute the full plan from the top.
					if err := ex.Execute(plan); err != nil {
						t.Fatalf("re-execute after crash at %d: %v\nplan:\n%s", crash, err, plan)
					}
					if err := Verify(tree, "/tree", sc.want); err != nil {
						t.Fatalf("verify after crash at %d: %v", crash, err)
					}
					// New content written before the crash is intact.
					for p, data := range sc.newData {
						if got, ok := tree.Read("/tree/" + p); ok && string(got) != string(data) {
							t.Fatalf("new content at %s corrupted: %q", p, got)
						}
					}
				})
			}
		})
	}
}

// TestDoubleExecution confirms a fully applied plan is a no-op the second
// time and the tree still matches exactly.
func TestDoubleExecution(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			plan, err := BuildPlan(sc.old, sc.want, Options{Content: staticContent(sc.newData)})
			if err != nil {
				t.Fatal(err)
			}
			tree := seed(t, sc.old, sc.oldData)
			ex := executor(tree)
			if err := ex.Execute(plan); err != nil {
				t.Fatal(err)
			}
			if err := ex.Execute(plan); err != nil {
				t.Fatalf("second execute: %v", err)
			}
			if err := Verify(tree, "/tree", sc.want); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRemoveGuardProtectsChangedContent: a file whose content changed after
// the plan was built must not be deleted by the guarded removal.
func TestRemoveGuardProtectsChangedContent(t *testing.T) {
	old := Manifest{"data.txt": FileEntry([]byte("original"))}
	plan, err := BuildPlan(old, Manifest{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	tree := seed(t, old, map[string][]byte{"data.txt": []byte("original")})
	// The user edits the file after the plan was computed.
	if err := tree.Write("/tree/data.txt", []byte("user edits")); err != nil {
		t.Fatal(err)
	}
	ex := executor(tree)
	if err := ex.Execute(plan); err != nil {
		t.Fatal(err)
	}
	got, ok := tree.Read("/tree/data.txt")
	if !ok || string(got) != "user edits" {
		t.Fatalf("guarded removal deleted changed content: %q ok=%v", got, ok)
	}
}

// TestRmdirGuardProtectsUnknownContent: a directory containing files the old
// manifest never knew about is not emptied or removed.
func TestRmdirGuardProtectsUnknownContent(t *testing.T) {
	old := Manifest{"d/known.txt": FileEntry([]byte("k"))}
	plan, err := BuildPlan(old, Manifest{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	tree := seed(t, old, map[string][]byte{"d/known.txt": []byte("k")})
	if err := tree.Write("/tree/d/stray.txt", []byte("stray")); err != nil {
		t.Fatal(err)
	}
	ex := executor(tree)
	err = ex.Execute(plan)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected not-empty error, got %v", err)
	}
	if _, ok := tree.Read("/tree/d/stray.txt"); !ok {
		t.Fatal("unknown content was deleted")
	}
}

func TestVerifyDetectsDivergence(t *testing.T) {
	want := Manifest{
		"a.txt":     FileEntry([]byte("a")),
		"dir/b.txt": FileEntry([]byte("b")),
	}

	// Missing entry.
	tree := seed(t, Manifest{"dir/b.txt": FileEntry([]byte("b"))},
		map[string][]byte{"dir/b.txt": []byte("b")})
	if err := Verify(tree, "/tree", want); err == nil || !strings.Contains(err.Error(), "missing a.txt") {
		t.Fatalf("expected missing-entry error, got %v", err)
	}

	// Extra entry.
	tree = seed(t, Manifest{
		"a.txt":     FileEntry([]byte("a")),
		"dir/b.txt": FileEntry([]byte("b")),
		"extra.txt": FileEntry([]byte("x")),
	}, map[string][]byte{
		"a.txt": []byte("a"), "dir/b.txt": []byte("b"), "extra.txt": []byte("x"),
	})
	if err := Verify(tree, "/tree", want); err == nil || !strings.Contains(err.Error(), "unexpected extra.txt") {
		t.Fatalf("expected extra-entry error, got %v", err)
	}

	// Mismatched content.
	tree = seed(t, Manifest{
		"a.txt":     FileEntry([]byte("tampered")),
		"dir/b.txt": FileEntry([]byte("b")),
	}, map[string][]byte{
		"a.txt": []byte("tampered"), "dir/b.txt": []byte("b"),
	})
	if err := Verify(tree, "/tree", want); err == nil || !strings.Contains(err.Error(), "mismatch a.txt") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

// TestSnapshotRoundTrip: Snapshot of a seeded tree reproduces the manifest
// with implicit directories materialized.
func TestSnapshotRoundTrip(t *testing.T) {
	m := Manifest{
		"a.txt":     FileEntry([]byte("a")),
		"dir/b.txt": FileEntry([]byte("b")),
		"link":      LinkEntry("a.txt"),
	}
	tree := seed(t, m, map[string][]byte{"a.txt": []byte("a"), "dir/b.txt": []byte("b")})
	got, err := Snapshot(tree, "/tree")
	if err != nil {
		t.Fatal(err)
	}
	want := m.withImplicitDirs()
	if len(got) != len(want) {
		t.Fatalf("snapshot size %d, want %d: %v", len(got), len(want), got)
	}
	for p, e := range want {
		if got[p] != e {
			t.Fatalf("snapshot[%s] = %+v, want %+v", p, got[p], e)
		}
	}
}
