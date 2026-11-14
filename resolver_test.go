package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testLayout builds:
//
//	tmp/
//	  root/
//	    public/
//	      hello.txt       "hello world"
//	      sub/page.txt    "sub page"
//	      escape.conf ->  ../outside/secret.conf   (symlink out of root)
//	      diresacpe  ->   ../outside               (dir symlink out of root)
//	      oklink.txt ->   sub/page.txt             (symlink inside root)
//	  outside/
//	    secret.conf       "top secret"
func testLayout(t *testing.T) (root string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, "root")
	outside := filepath.Join(tmp, "outside")
	sub := filepath.Join(root, "public", "sub")
	for _, dir := range []string{sub, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "public", "hello.txt"): "hello world",
		filepath.Join(sub, "page.txt"):             "sub page",
		filepath.Join(outside, "secret.conf"):      "top secret",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(root, "public", "escape.conf"): filepath.Join("..", "..", "outside", "secret.conf"),
		filepath.Join(root, "public", "direscape"):   filepath.Join("..", "..", "outside"),
		filepath.Join(root, "public", "oklink.txt"):  filepath.Join("sub", "page.txt"),
	}
	for name, target := range links {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func newTestResolver(t *testing.T) *Resolver {
	t.Helper()
	r, err := NewResolver(testLayout(t), "/static")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveEncodedTraversal(t *testing.T) {
	r := newTestResolver(t)
	paths := []string{
		"/static/%2e%2e/outside/secret.conf",
		"/static/%2e%2e%2f%2e%2e%2foutside/secret.conf",
		"/static/%2E%2E/outside/secret.conf",
		"/static/public/../../outside/secret.conf",
		"/static/..",
		"/static/../",
	}
	for _, p := range paths {
		if _, err := r.Resolve(p); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %v, want ErrNotFound", p, err)
		}
	}
}

func TestResolveDoubleEncodedIsLiteral(t *testing.T) {
	r := newTestResolver(t)
	// Decoded exactly once: "%252e%252e" becomes the literal name "%2e%2e",
	// which does not exist inside the root and must not traverse.
	res, err := r.Resolve("/static/%252e%252e/secret.conf")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve double-encoded = %v, want ErrNotFound", err)
	}
	_ = res
}

func TestResolveDoubleSlashAndAbsolute(t *testing.T) {
	r := newTestResolver(t)
	// Extra slashes collapse; the path stays inside the root.
	res, err := r.Resolve("/static//public//sub//page.txt")
	if err != nil {
		t.Fatalf("Resolve double slash: %v", err)
	}
	if res.Rel != "public/sub/page.txt" {
		t.Fatalf("Rel = %q, want public/sub/page.txt", res.Rel)
	}
	// Absolute-path tricks normalize into the root and miss.
	for _, p := range []string{
		"/static//etc/passwd",
		"/static/%2fetc/passwd",
		"/static/./././../../etc/passwd",
	} {
		if _, err := r.Resolve(p); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %v, want ErrNotFound", p, err)
		}
	}
}

func TestResolveRejectsMalformed(t *testing.T) {
	r := newTestResolver(t)
	paths := []string{
		"/static/%00",                 // encoded NUL
		"/static/hello.txt%00.jpg",    // NUL suffix trick
		"/static/%5c..%5c..",          // encoded backslashes
		"/static/..\\..\\secret.conf", // raw backslashes
		"/static/%zz",                 // invalid escape
		"/static/%2",                  // truncated escape
		"/static/%",                   // lone percent
	}
	for _, p := range paths {
		if _, err := r.Resolve(p); !errors.Is(err, ErrBadRequest) {
			t.Errorf("Resolve(%q) = %v, want ErrBadRequest", p, err)
		}
	}
}

func TestResolvePrefixMatchBySegment(t *testing.T) {
	r := newTestResolver(t)
	// "/staticfoo" shares a string prefix with "/static" but no segment.
	if _, err := r.Resolve("/staticfoo/public/hello.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(/staticfoo/...) = %v, want ErrNotFound", err)
	}
	// The bare prefix resolves to the root directory itself.
	res, err := r.Resolve("/static")
	if err != nil {
		t.Fatalf("Resolve(/static): %v", err)
	}
	if !res.Info.IsDir() || res.Rel != "" {
		t.Fatalf("Resolve(/static) = %+v, want root dir", res)
	}
}

func TestResolveSymlinks(t *testing.T) {
	r := newTestResolver(t)
	// Symlinks pointing outside the root are refused like missing files.
	for _, p := range []string{
		"/static/public/escape.conf",
		"/static/public/direscape/secret.conf",
	} {
		if _, err := r.Resolve(p); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %v, want ErrNotFound", p, err)
		}
	}
	// A symlink staying inside the root resolves fine.
	res, err := r.Resolve("/static/public/oklink.txt")
	if err != nil {
		t.Fatalf("Resolve(oklink): %v", err)
	}
	if res.Info.Name() != "page.txt" {
		t.Fatalf("oklink resolved to %q, want page.txt", res.Info.Name())
	}
}

func TestResolveLegitSubdir(t *testing.T) {
	r := newTestResolver(t)
	res, err := r.Resolve("/static/public/sub/page.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	f, err := res.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "sub page" {
		t.Fatalf("content = %q, want %q", buf[:n], "sub page")
	}
}
