package netsec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testLayout struct {
	base    string
	rootDir string
	outside string
	root    *Root
}

func newTestLayout(t *testing.T) testLayout {
	t.Helper()
	base := t.TempDir()
	rootDir := filepath.Join(base, "module")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("create module root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside root: %v", err)
	}
	root, err := NewRoot(rootDir)
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return testLayout{base: base, rootDir: rootDir, outside: outside, root: root}
}

func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRejectsPathTraversalAndAbsolutePaths(t *testing.T) {
	layout := newTestLayout(t)
	outsideFile := filepath.Join(layout.outside, "secret.txt")
	requireNoError(t, os.WriteFile(outsideFile, []byte("outside-secret"), 0o644))

	badPaths := []string{
		"../outside/secret.txt",
		"a/../../outside/secret.txt",
		"..",
		"../",
		"/etc/passwd",
		"//etc/passwd",
		`\etc\passwd`,
		`..\outside\secret.txt`,
		`C:\Windows\System32\drivers\etc\hosts`,
		`c:/Windows/System32/drivers/etc/hosts`,
		"safe/../../outside/secret.txt",
		"safe/../../../outside/secret.txt",
		"%2e%2e%2foutside%2fsecret.txt",
		"..%2foutside%2fsecret.txt",
		"%2Fetc%2Fpasswd",
		"%252e%252e%252foutside",
		"%5c..%5cetc%5cpasswd",
		"bad\x00name",
		"",
	}

	for _, name := range badPaths {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if _, err := layout.root.Resolve(name); err == nil {
				t.Errorf("Resolve(%q) succeeded", name)
			}
			if _, err := layout.root.ReadFile(name); err == nil {
				t.Errorf("ReadFile(%q) succeeded", name)
			}
			if err := layout.root.WriteFile(name, []byte("overwritten"), 0o644); err == nil {
				t.Errorf("WriteFile(%q) succeeded", name)
			}
			if err := layout.root.Remove(name); err == nil {
				t.Errorf("Remove(%q) succeeded", name)
			}
			if err := layout.root.Rename(name, "moved"); err == nil {
				t.Errorf("Rename(%q) succeeded", name)
			}
			if err := layout.root.Rename("legal", name); err == nil {
				t.Errorf("Rename(..., %q) succeeded", name)
			}
		})
	}

	got, err := os.ReadFile(outsideFile)
	requireNoError(t, err)
	if string(got) != "outside-secret" {
		t.Fatalf("outside file changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(layout.outside, "overwritten")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outside path was created: %v", err)
	}
	if _, err := layout.root.ReadFile("../outside/secret.txt"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("traversal error = %v, want ErrPathEscape", err)
	}
	if _, err := layout.root.ReadFile("/etc/passwd"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("absolute-path error = %v, want ErrInvalidPath", err)
	}
}

func TestSymlinkEscapeRejectedForEveryOperation(t *testing.T) {
	layout := newTestLayout(t)
	secret := filepath.Join(layout.outside, "secret.txt")
	requireNoError(t, os.WriteFile(secret, []byte("outside-secret"), 0o644))
	requireNoError(t, os.MkdirAll(filepath.Join(layout.outside, "dir"), 0o755))
	requireNoError(t, os.WriteFile(filepath.Join(layout.outside, "dir", "target.txt"), []byte("target"), 0o644))

	requireNoError(t, os.Symlink(secret, filepath.Join(layout.rootDir, "escape-file")))
	requireNoError(t, os.Symlink(filepath.Join(layout.outside, "dir"), filepath.Join(layout.rootDir, "escape-dir")))
	requireNoError(t, os.Symlink("../outside/secret.txt", filepath.Join(layout.rootDir, "relative-escape")))
	requireNoError(t, os.Symlink("escape-dir", filepath.Join(layout.rootDir, "escape-chain")))
	requireNoError(t, os.WriteFile(filepath.Join(layout.rootDir, "local.txt"), []byte("local"), 0o644))

	checks := []struct {
		name string
		run  func() error
	}{
		{"read file link", func() error { _, err := layout.root.ReadFile("escape-file"); return err }},
		{"read relative link", func() error { _, err := layout.root.ReadFile("relative-escape"); return err }},
		{"read dir link", func() error { _, err := layout.root.ReadFile("escape-dir/target.txt"); return err }},
		{"read chain", func() error { _, err := layout.root.ReadFile("escape-chain/target.txt"); return err }},
		{"write file link", func() error {
			return layout.root.WriteFile("escape-file", []byte("pwned"), 0o644)
		}},
		{"write through dir link", func() error {
			return layout.root.WriteFile("escape-dir/new.txt", []byte("pwned"), 0o644)
		}},
		{"rename source link", func() error {
			return layout.root.Rename("escape-file", "renamed")
		}},
		{"rename into dir link", func() error {
			return layout.root.Rename("local.txt", "escape-dir/moved.txt")
		}},
		{"remove link", func() error { return layout.root.Remove("escape-file") }},
		{"mkdir through link", func() error { return layout.root.MkdirAll("escape-dir/sub", 0o755) }},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			requireError(t, check.run())
		})
	}

	got, err := os.ReadFile(secret)
	requireNoError(t, err)
	if string(got) != "outside-secret" {
		t.Fatalf("outside file changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(layout.outside, "dir", "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.outside, "dir", "moved.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outside file was moved: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(layout.rootDir, "escape-file")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("escape symlink should not have been removed: %v", err)
	}
	if _, err := layout.root.ReadFile("escape-file"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("symlink escape error = %v, want ErrPathEscape", err)
	}
}

func TestSymlinkInsideRootIsResolvedAndAllowedForReads(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.MkdirAll("data", 0o755))
	requireNoError(t, layout.root.WriteFile("data/real.txt", []byte("real-content"), 0o644))
	requireNoError(t, os.Symlink("real.txt", filepath.Join(layout.rootDir, "data", "link.txt")))

	got, err := layout.root.ReadFile("data/link.txt")
	requireNoError(t, err)
	if string(got) != "real-content" {
		t.Fatalf("read through internal symlink: got %q", got)
	}

	resolved, err := layout.root.Resolve("data/link.txt")
	requireNoError(t, err)
	if !withinRoot(layout.root.Name(), resolved) {
		t.Fatalf("resolved path escaped root: %s", resolved)
	}
	if filepath.Base(resolved) != "real.txt" {
		t.Fatalf("expected link to resolve to real.txt, got %s", resolved)
	}

	// A write must not silently replace the link or follow it to an
	// unverified destination.
	if err := layout.root.WriteFile("data/link.txt", []byte("new"), 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("WriteFile through internal symlink: got %v, want ErrSymlink", err)
	}
	got, err = layout.root.ReadFile("data/real.txt")
	requireNoError(t, err)
	if string(got) != "real-content" {
		t.Fatalf("write through symlink changed target: %q", got)
	}

	requireNoError(t, layout.root.Remove("data/link.txt"))
	if _, err := os.Lstat(filepath.Join(layout.rootDir, "data", "link.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("link was not removed: %v", err)
	}
	got, err = layout.root.ReadFile("data/real.txt")
	requireNoError(t, err)
	if string(got) != "real-content" {
		t.Fatalf("removing a link changed its target: %q", got)
	}
}

func TestRootUsesCanonicalConfiguredDirectory(t *testing.T) {
	base := t.TempDir()
	actual := filepath.Join(base, "actual")
	link := filepath.Join(base, "configured-link")
	requireNoError(t, os.MkdirAll(actual, 0o755))
	requireNoError(t, os.Symlink(actual, link))

	root, err := NewRoot(link)
	requireNoError(t, err)
	defer root.Close()

	canonical, err := filepath.EvalSymlinks(actual)
	requireNoError(t, err)
	if root.Name() != canonical {
		t.Fatalf("root name = %q, want %q", root.Name(), canonical)
	}
	requireNoError(t, root.WriteFile("file.txt", []byte("ok"), 0o644))
}

func TestDanglingSymlinkTargetsAreCheckedWithoutFollowingForDelete(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, os.Symlink("missing-inside.txt", filepath.Join(layout.rootDir, "inside-link")))
	requireNoError(t, os.Symlink("../outside/missing-outside.txt", filepath.Join(layout.rootDir, "outside-link")))

	if err := layout.root.WriteFile("inside-link", []byte("x"), 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("write to dangling inside link: %v, want ErrSymlink", err)
	}
	if _, err := layout.root.ReadFile("inside-link"); err == nil {
		t.Fatal("read through dangling inside link succeeded")
	}
	if err := layout.root.Remove("outside-link"); err == nil {
		t.Fatal("remove of dangling outside link succeeded")
	}
	if info, err := os.Lstat(filepath.Join(layout.rootDir, "outside-link")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("dangling outside link was removed: %v", err)
	}

	requireNoError(t, layout.root.Rename("inside-link", "renamed-link"))
	if _, err := os.Lstat(filepath.Join(layout.rootDir, "renamed-link")); err != nil {
		t.Fatalf("dangling inside link was not renamed: %v", err)
	}
	requireNoError(t, layout.root.Remove("renamed-link"))
}

func TestInternalDirectorySymlinkCanBeUsedForWrites(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.MkdirAll("real-dir", 0o755))
	requireNoError(t, os.Symlink("real-dir", filepath.Join(layout.rootDir, "alias-dir")))

	requireNoError(t, layout.root.WriteFile("alias-dir/file.txt", []byte("through-alias"), 0o644))
	got, err := layout.root.ReadFile("real-dir/file.txt")
	requireNoError(t, err)
	if string(got) != "through-alias" {
		t.Fatalf("write through internal directory symlink: %q", got)
	}
	if _, err := layout.root.Resolve("alias-dir/file.txt"); err != nil {
		t.Fatalf("resolve internal directory symlink: %v", err)
	}
}
