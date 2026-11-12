package netsec

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel string, perm fs.FileMode, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), perm); err != nil {
		t.Fatal(err)
	}
	// Chmod after write so the umask cannot widen or narrow the mode.
	if err := os.Chmod(p, perm); err != nil {
		t.Fatal(err)
	}
}

// newGraph builds the standard test graph:
//
//	app ──require──▶ assets
//	app ──optional──▶ gpu
//	app ──platform(current)──▶ native
//	app ──platform(windows)──▶ winres
func newGraph(t *testing.T) *Graph {
	t.Helper()
	g := &Graph{}
	for name, files := range map[string][]string{
		"app":    {"bin/app"},
		"assets": {"data/assets.dat"},
		"gpu":    {"data/gpu.dat"},
		"native": {"data/native.dat"},
		"winres": {"data/win.dat"},
	} {
		if err := g.Add(name, files...); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Require("app", "assets"); err != nil {
		t.Fatal(err)
	}
	if err := g.Optional("app", "gpu"); err != nil {
		t.Fatal(err)
	}
	if err := g.Platform("app", "native", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if err := g.Platform("app", "winres", "windows", ""); err != nil {
		t.Fatal(err)
	}
	return g
}

func writeTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, "bin/app", 0o755, "binary")
	writeFile(t, root, "data/assets.dat", 0o644, "assets")
	writeFile(t, root, "data/gpu.dat", 0o644, "gpu")
	writeFile(t, root, "data/native.dat", 0o644, "native")
	writeFile(t, root, "data/win.dat", 0o644, "win")
}

func entryPaths(m *Manifest) []string {
	var out []string
	for _, e := range m.Entries {
		out = append(out, e.Path)
	}
	return out
}

func TestBuildClosureRequiredAndPlatform(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)

	m, err := Build(newGraph(t), Config{Root: root}, "app")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bin/app", "data/assets.dat", "data/native.dat"}
	if got := entryPaths(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if m.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("platform = %q", m.Platform)
	}
	if len(m.Optional) != 0 {
		t.Fatalf("optional = %v, want empty", m.Optional)
	}

	e, ok := m.Lookup("bin/app")
	if !ok {
		t.Fatal("bin/app not in manifest")
	}
	if e.Mode != 0o755 {
		t.Fatalf("bin/app mode = %#o, want 0755", e.Mode)
	}
	if e.Size != int64(len("binary")) {
		t.Fatalf("bin/app size = %d", e.Size)
	}
	if !strings.HasPrefix(e.Digest, "sha256:") {
		t.Fatalf("bin/app digest = %q", e.Digest)
	}
}

func TestBuildOptionalActivation(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	g := newGraph(t)

	off, err := Build(g, Config{Root: root}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := off.Lookup("data/gpu.dat"); ok {
		t.Fatal("inactive optional object leaked into manifest")
	}

	on, err := Build(g, Config{Root: root, Optional: []string{"gpu"}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := on.Lookup("data/gpu.dat"); !ok {
		t.Fatal("activated optional object missing from manifest")
	}
	if len(on.Optional) != 1 || on.Optional[0] != "gpu" {
		t.Fatalf("optional = %v, want [gpu]", on.Optional)
	}
}

func TestBuildMissingRequiredObject(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	if err := os.Remove(filepath.Join(root, "data", "assets.dat")); err != nil {
		t.Fatal(err)
	}
	_, err := Build(newGraph(t), Config{Root: root}, "app")
	if !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("err = %v, want ErrObjectMissing", err)
	}
}

func TestBuildActivatedOptionalMissingObject(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	if err := os.Remove(filepath.Join(root, "data", "gpu.dat")); err != nil {
		t.Fatal(err)
	}
	_, err := Build(newGraph(t), Config{Root: root, Optional: []string{"gpu"}}, "app")
	if !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("err = %v, want ErrObjectMissing", err)
	}
}

func TestBuildInactiveOptionalMissingObjectIgnored(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	if err := os.Remove(filepath.Join(root, "data", "gpu.dat")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(newGraph(t), Config{Root: root}, "app"); err != nil {
		t.Fatalf("inactive optional object must not fail the build: %v", err)
	}
}

func TestBuildUnreadableObject(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-0000 files")
	}
	root := t.TempDir()
	writeTree(t, root)
	p := filepath.Join(root, "data", "assets.dat")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	_, err := Build(newGraph(t), Config{Root: root}, "app")
	if !errors.Is(err, ErrObjectUnreadable) {
		t.Fatalf("err = %v, want ErrObjectUnreadable", err)
	}
}

func TestBuildRejectsNonRegularObject(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	target := filepath.Join(root, "data", "native.dat")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("assets.dat", target); err != nil {
		t.Fatal(err)
	}
	_, err := Build(newGraph(t), Config{Root: root}, "app")
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular", err)
	}
}

func TestBuildRejectsAliasPathInNode(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	g := &Graph{}
	if err := g.Add("app", "./bin/app"); err != nil {
		t.Fatal(err)
	}
	_, err := Build(g, Config{Root: root}, "app")
	if !errors.Is(err, ErrPathAlias) {
		t.Fatalf("err = %v, want ErrPathAlias", err)
	}
}

func TestBuildRejectsDuplicateOwnership(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	g := &Graph{}
	if err := g.Add("a", "bin/app"); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("b", "bin/app"); err != nil {
		t.Fatal(err)
	}
	if err := g.Require("a", "b"); err != nil {
		t.Fatal(err)
	}
	_, err := Build(g, Config{Root: root}, "a")
	if err == nil || !strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("err = %v, want duplicate ownership error", err)
	}
}

func TestBuildUnknownRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := Build(&Graph{}, Config{Root: root}, "nope"); err == nil {
		t.Fatal("want error for unknown root node")
	}
}

func TestManifestCanonicalDeterministic(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	g := newGraph(t)

	m1, err := Build(g, Config{Root: root, Optional: []string{"gpu"}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Build(g, Config{Root: root, Optional: []string{"gpu"}}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m1.Canonical(), m2.Canonical()) {
		t.Fatal("canonical encoding is not deterministic")
	}
	if m1.Digest() != m2.Digest() {
		t.Fatal("manifest digest is not deterministic")
	}

	m3, err := Decode(bytes.NewReader(m1.Canonical()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m3.Canonical(), m1.Canonical()) {
		t.Fatal("decode/encode round trip changed the manifest")
	}
	if _, ok := m3.Lookup("data/gpu.dat"); !ok {
		t.Fatal("decoded manifest lost the optional entry")
	}
}

func TestDecodeRejectsMalformedManifests(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	m, err := Build(newGraph(t), Config{Root: root}, "app")
	if err != nil {
		t.Fatal(err)
	}
	good := m.Canonical()

	cases := map[string]string{
		"bad version":    strings.Replace(string(good), `"version":1`, `"version":2`, 1),
		"alias path":     strings.Replace(string(good), `"bin/app"`, `"./bin/app"`, 1),
		"unsorted":       strings.Replace(string(good), `"bin/app"`, `"zzz"`, 1),
		"bad digest":     strings.Replace(string(good), `"sha256:`, `"md5:`, 1),
		"unknown fields": strings.Replace(string(good), `"version":1`, `"version":1,"evil":true`, 1),
	}
	for name, doc := range cases {
		if _, err := Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: Decode accepted malformed manifest", name)
		}
	}
}
