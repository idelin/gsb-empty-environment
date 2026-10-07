package manifest

import (
	"strings"
	"testing"
)

// seed builds a VTree from a manifest plus file contents.
func seed(t *testing.T, m Manifest, content map[string][]byte) *VTree {
	t.Helper()
	tree := NewVTree()
	if err := tree.MkdirAll("/tree"); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	full := m.withImplicitDirs()
	for _, p := range sortedPaths(full) {
		e := full[p]
		abs := "/tree/" + p
		switch e.Type {
		case Dir:
			if err := tree.MkdirAll(abs); err != nil {
				t.Fatalf("seed mkdir %s: %v", p, err)
			}
		case File:
			data, ok := content[p]
			if !ok {
				t.Fatalf("seed: no content for %s", p)
			}
			if e.Digest != "" && DigestBytes(data) != e.Digest {
				t.Fatalf("seed: content for %s does not match manifest digest", p)
			}
			if err := tree.Write(abs, data); err != nil {
				t.Fatalf("seed write %s: %v", p, err)
			}
		case Symlink:
			if err := tree.Symlink(e.Link, abs); err != nil {
				t.Fatalf("seed symlink %s: %v", p, err)
			}
		}
	}
	return tree
}

func executor(tree *VTree) Executor {
	return Executor{FS: tree, Root: "/tree", Quarantine: "/quarantine"}
}

func staticContent(files map[string][]byte) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) { return files[p], nil }
}

func TestBuildPlanIdenticalManifestsIsEmpty(t *testing.T) {
	m := Manifest{
		"a.txt":     FileEntry([]byte("hello")),
		"dir/b.txt": FileEntry([]byte("world")),
		"link":      LinkEntry("a.txt"),
	}
	plan, err := BuildPlan(m, m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ops) != 0 {
		t.Fatalf("expected empty plan, got:\n%s", plan)
	}
}

func TestFileToDirectoryMovesAncestorAsideFirst(t *testing.T) {
	old := Manifest{"conflict": FileEntry([]byte("precious"))}
	want := Manifest{
		"conflict/inner/a.txt": FileEntry([]byte("a")),
		"conflict/inner/b.txt": FileEntry([]byte("b")),
	}
	content := map[string][]byte{
		"conflict/inner/a.txt": []byte("a"),
		"conflict/inner/b.txt": []byte("b"),
	}
	plan, err := BuildPlan(old, want, Options{Content: staticContent(content)})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ops[0].Kind != OpMoveAside || plan.Ops[0].Path != "conflict" {
		t.Fatalf("first op must move the conflicting file aside, got:\n%s", plan)
	}

	tree := seed(t, old, map[string][]byte{"conflict": []byte("precious")})
	ex := executor(tree)
	if err := ex.Execute(plan); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tree, "/tree", want); err != nil {
		t.Fatal(err)
	}
	// The old file content is preserved in quarantine, not deleted.
	got, ok := tree.Read("/quarantine/conflict")
	if !ok || string(got) != "precious" {
		t.Fatalf("quarantined content lost: %q ok=%v", got, ok)
	}
}

func TestDirectoryToFileRemovesDescendantsFirst(t *testing.T) {
	old := Manifest{
		"app":            DirEntry(),
		"app/bin/run":    FileEntry([]byte("bin")),
		"app/etc/cfg":    FileEntry([]byte("cfg")),
		"app/etc/nested": DirEntry(),
	}
	want := Manifest{"app": FileEntry([]byte("now-a-file"))}
	content := map[string][]byte{"app": []byte("now-a-file")}
	plan, err := BuildPlan(old, want, Options{Content: staticContent(content)})
	if err != nil {
		t.Fatal(err)
	}

	// Every removal must precede the write, and deeper paths must be
	// removed before their parents.
	writeIdx := -1
	seen := map[string]int{}
	for i, op := range plan.Ops {
		if op.Kind == OpWriteFile {
			writeIdx = i
		}
		if op.Kind == OpRemoveFile || op.Kind == OpRmdir {
			seen[op.Path] = i
		}
	}
	if writeIdx < 0 {
		t.Fatalf("plan never writes the new file:\n%s", plan)
	}
	for _, p := range []string{"app/bin/run", "app/etc/cfg", "app/etc/nested", "app/etc", "app/bin", "app"} {
		idx, ok := seen[p]
		if !ok {
			t.Fatalf("plan never removes %s:\n%s", p, plan)
		}
		if idx > writeIdx {
			t.Fatalf("removal of %s happens after the write:\n%s", p, plan)
		}
	}
	if seen["app/etc/cfg"] > seen["app/etc"] {
		t.Fatalf("descendant removed after its parent:\n%s", plan)
	}

	tree := seed(t, old, map[string][]byte{
		"app/bin/run": []byte("bin"),
		"app/etc/cfg": []byte("cfg"),
	})
	ex := executor(tree)
	if err := ex.Execute(plan); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tree, "/tree", want); err != nil {
		t.Fatal(err)
	}
}

func TestTypeSwapFileAndSymlink(t *testing.T) {
	old := Manifest{
		"becomes-link": FileEntry([]byte("data")),
		"becomes-file": LinkEntry("/etc/hostname"),
	}
	want := Manifest{
		"becomes-link": LinkEntry("/etc/hostname"),
		"becomes-file": FileEntry([]byte("data")),
	}
	content := map[string][]byte{"becomes-file": []byte("data")}
	plan, err := BuildPlan(old, want, Options{Content: staticContent(content)})
	if err != nil {
		t.Fatal(err)
	}
	tree := seed(t, old, map[string][]byte{"becomes-link": []byte("data")})
	ex := executor(tree)
	if err := ex.Execute(plan); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tree, "/tree", want); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlanRejectsWrongContent(t *testing.T) {
	want := Manifest{"f": FileEntry([]byte("expected"))}
	bad := map[string][]byte{"f": []byte("different")}
	_, err := BuildPlan(nil, want, Options{Content: staticContent(bad)})
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected digest mismatch error, got %v", err)
	}
}

func TestManifestValidation(t *testing.T) {
	if _, err := BuildPlan(Manifest{"/abs": DirEntry()}, nil, Options{}); err == nil {
		t.Fatal("absolute path accepted")
	}
	if _, err := BuildPlan(Manifest{"../up": DirEntry()}, nil, Options{}); err == nil {
		t.Fatal("escaping path accepted")
	}
	inconsistent := Manifest{
		"a":   FileEntry([]byte("x")),
		"a/b": FileEntry([]byte("y")),
	}
	if _, err := BuildPlan(inconsistent, nil, Options{}); err == nil {
		t.Fatal("inconsistent manifest accepted")
	}
}
