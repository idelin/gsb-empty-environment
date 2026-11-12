package netsec

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func newLoader(t *testing.T, optional ...string) (string, *Loader) {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root)
	m, err := Build(newGraph(t), Config{Root: root, Optional: optional}, "app")
	if err != nil {
		t.Fatal(err)
	}
	return root, NewLoader(root, m)
}

func TestLoaderReadsManifestObject(t *testing.T) {
	_, l := newLoader(t)
	data, err := l.ReadFile("bin/app")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary" {
		t.Fatalf("data = %q", data)
	}

	rc, err := l.Open("data/assets.dat")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	viaOpen, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(viaOpen) != "assets" {
		t.Fatalf("via Open = %q", viaOpen)
	}
}

func TestLoaderRejectsUnlistedResource(t *testing.T) {
	root, l := newLoader(t)
	// Present on disk but omitted from the manifest closure.
	writeFile(t, root, "data/secret.dat", 0o644, "secret")
	if _, err := l.ReadFile("data/secret.dat"); !errors.Is(err, ErrNotInManifest) {
		t.Fatalf("err = %v, want ErrNotInManifest", err)
	}
}

func TestLoaderRejectsPathAliases(t *testing.T) {
	_, l := newLoader(t)
	aliases := []string{
		"./bin/app",
		"bin//app",
		"bin/./app",
		"bin/../bin/app",
		"bin/app/",
		"bin\\app",
		"../escape",
		"..",
		".",
		"",
		"/bin/app",
	}
	for _, a := range aliases {
		if _, err := l.ReadFile(a); !errors.Is(err, ErrPathAlias) {
			t.Errorf("ReadFile(%q): err = %v, want ErrPathAlias", a, err)
		}
	}
	// A case variant is canonical but unlisted, so it must not resolve to
	// the real object even on case-insensitive filesystems.
	if _, err := l.ReadFile("BIN/APP"); !errors.Is(err, ErrNotInManifest) {
		t.Errorf("ReadFile(BIN/APP): err = %v, want ErrNotInManifest", err)
	}
}

func TestLoaderRejectsTamperedContent(t *testing.T) {
	root, l := newLoader(t)
	p := filepath.Join(root, "data", "assets.dat")
	if err := os.WriteFile(p, []byte("forged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadFile("data/assets.dat"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
}

func TestLoaderRejectsModeChange(t *testing.T) {
	root, l := newLoader(t)
	p := filepath.Join(root, "bin", "app")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadFile("bin/app"); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("err = %v, want ErrModeMismatch", err)
	}
}

func TestLoaderRejectsSymlinkReplacement(t *testing.T) {
	root, l := newLoader(t)
	// The symlink target carries identical content and mode; the loader
	// must still refuse the non-regular replacement.
	evil := filepath.Join(root, "evil.dat")
	if err := os.WriteFile(evil, []byte("assets"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "data", "assets.dat")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, p); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadFile("data/assets.dat"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular", err)
	}
}

func TestLoaderRejectsMissingObject(t *testing.T) {
	root, l := newLoader(t)
	if err := os.Remove(filepath.Join(root, "data", "assets.dat")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadFile("data/assets.dat"); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("err = %v, want ErrObjectMissing", err)
	}
}

func TestLoaderOptionalActivation(t *testing.T) {
	// Inactive optional dependency: object exists on disk but is outside
	// the signed closure, so the loader must refuse it.
	_, off := newLoader(t)
	if _, err := off.ReadFile("data/gpu.dat"); !errors.Is(err, ErrNotInManifest) {
		t.Fatalf("inactive optional: err = %v, want ErrNotInManifest", err)
	}

	// Activated optional dependency: object is inside the closure.
	_, on := newLoader(t, "gpu")
	data, err := on.ReadFile("data/gpu.dat")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "gpu" {
		t.Fatalf("data = %q", data)
	}
}

func TestLoaderVerifyAll(t *testing.T) {
	root, l := newLoader(t, "gpu")
	if err := l.VerifyAll(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "data", "gpu.dat"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.VerifyAll(); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("err = %v, want ErrModeMismatch", err)
	}
}
