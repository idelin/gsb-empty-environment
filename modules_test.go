package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestModuleIsolation(t *testing.T) {
	base := t.TempDir()
	aDir := filepath.Join(base, "modules", "a")
	bDir := filepath.Join(base, "modules", "b")
	requireNoError(t, os.MkdirAll(aDir, 0o755))
	requireNoError(t, os.MkdirAll(bDir, 0o755))
	requireNoError(t, os.WriteFile(filepath.Join(aDir, "only-a.txt"), []byte("a"), 0o644))
	requireNoError(t, os.WriteFile(filepath.Join(bDir, "only-b.txt"), []byte("b"), 0o644))

	modules, err := NewModules(map[string]string{"a": aDir, "b": bDir})
	requireNoError(t, err)
	defer modules.Close()

	got, err := modules.ReadFile("a", "only-a.txt")
	requireNoError(t, err)
	if string(got) != "a" {
		t.Fatalf("module a read: %q", got)
	}
	got, err = modules.ReadFile("b", "only-b.txt")
	requireNoError(t, err)
	if string(got) != "b" {
		t.Fatalf("module b read: %q", got)
	}

	// A request cannot select another root by including traversal or another
	// module name in the path.
	if _, err := modules.ReadFile("a", "../b/only-b.txt"); err == nil {
		t.Fatal("cross-module traversal succeeded")
	}
	if _, err := modules.ReadFile("a", bDir+"/only-b.txt"); err == nil {
		t.Fatal("absolute cross-module read succeeded")
	}
	if err := modules.WriteFile("a", "../b/only-b.txt", []byte("pwned"), 0o644); err == nil {
		t.Fatal("cross-module write succeeded")
	}
	if err := modules.Rename("a", "only-a.txt", "../b/stolen.txt"); err == nil {
		t.Fatal("cross-module rename succeeded")
	}
	if err := modules.Remove("a", "../b/only-b.txt"); err == nil {
		t.Fatal("cross-module remove succeeded")
	}
	if _, err := modules.Root("missing"); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("unknown module: %v", err)
	}
	if _, err := modules.Root("../b"); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("traversal-shaped module name: %v", err)
	}

	// The module name is a map key, never a path prefix. A module named
	// "../b" must not be accepted from configuration.
	if _, err := NewModules(map[string]string{"../b": bDir}); err == nil {
		t.Fatal("invalid module name was accepted")
	}

	// A symlink inside module a that points to module b is outside a's root
	// and must be rejected.
	requireNoError(t, os.Symlink("../b", filepath.Join(aDir, "to-b")))
	if _, err := modules.ReadFile("a", "to-b/only-b.txt"); err == nil {
		t.Fatal("read through cross-module symlink succeeded")
	}
	if err := modules.WriteFile("a", "to-b/pwned.txt", []byte("pwned"), 0o644); err == nil {
		t.Fatal("write through cross-module symlink succeeded")
	}

	got, err = os.ReadFile(filepath.Join(bDir, "only-b.txt"))
	requireNoError(t, err)
	if string(got) != "b" {
		t.Fatalf("module b was modified: %q", got)
	}
	if _, err := os.Stat(filepath.Join(bDir, "pwned.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("module b was written through symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bDir, "stolen.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("module b received a rename: %v", err)
	}
}

func TestModuleConfigurationRejectsPathSeparators(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	requireNoError(t, os.MkdirAll(root, 0o755))
	badNames := []string{"", ".", "..", "a/b", `a\b`, `C:\module`, "a\x00b"}
	for _, name := range badNames {
		t.Run(name, func(t *testing.T) {
			if _, err := NewModules(map[string]string{name: root}); err == nil {
				t.Fatalf("module name %q was accepted", name)
			}
		})
	}
}

func TestModuleConfigurationRejectsOverlappingRoots(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "modules")
	child := filepath.Join(parent, "child")
	requireNoError(t, os.MkdirAll(child, 0o755))

	configs := []map[string]string{
		{"a": parent, "b": child},
		{"a": child, "b": parent},
		{"a": parent, "b": parent},
	}
	for _, config := range configs {
		if _, err := NewModules(config); err == nil {
			t.Fatalf("overlapping module configuration was accepted: %v", config)
		}
	}
}
