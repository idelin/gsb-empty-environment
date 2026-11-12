package fidauth_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/q0481/fidauth"
)

// tree builds:
//
//	base/export/          export root
//	base/export/sub/deep/
//	base/export/escape -> base/outside        (symlink out of root)
//	base/export/escfile -> base/outside/secret.txt
//	base/export/inl -> sub                    (symlink inside root)
//	base/export/sub/up -> ..                  (symlink back to root)
//	base/outside/secret.txt
func tree(t *testing.T) (auth *fidauth.Auth, root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "export")
	outside = filepath.Join(base, "outside")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755))
	must(os.MkdirAll(outside, 0o755))
	must(os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644))
	must(os.Symlink(outside, filepath.Join(root, "escape")))
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escfile")))
	must(os.Symlink("sub", filepath.Join(root, "inl")))
	must(os.Symlink("..", filepath.Join(root, "sub", "up")))

	auth = fidauth.New(nil)
	must(auth.RegisterExport("export", root))
	realRoot, err := filepath.EvalSymlinks(root)
	must(err)
	return auth, realRoot, outside
}

func TestAttachUnknownExport(t *testing.T) {
	auth, _, _ := tree(t)
	for _, aname := range []string{"nope", "/etc", "../outside", "export/../outside"} {
		if _, err := auth.Attach(1, aname); !errors.Is(err, fidauth.ErrUnknownExport) {
			t.Fatalf("aname %q: got %v, want ErrUnknownExport", aname, err)
		}
	}
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatalf("registered aname: %v", err)
	}
}

func TestWalkDotDotClampsAtRoot(t *testing.T) {
	auth, root, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	// ".." at the root stays at the root, never escapes above it.
	p, err := auth.Walk(1, 2, []string{"..", "..", ".."})
	if err != nil {
		t.Fatal(err)
	}
	if p != root {
		t.Fatalf("got %q, want clamped root %q", p, root)
	}
	// Mixed descent and ".." still lands inside the root.
	p, err = auth.Walk(1, 3, []string{"sub", "deep", "..", "..", "sub"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sub"); p != want {
		t.Fatalf("got %q, want %q", p, want)
	}
}

func TestWalkTooManyElements(t *testing.T) {
	auth, _, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	names := make([]string, fidauth.MaxWalkElements+1)
	for i := range names {
		names[i] = ".."
	}
	if _, err := auth.Walk(1, 2, names); !errors.Is(err, fidauth.ErrTooManyElems) {
		t.Fatalf("got %v, want ErrTooManyElems", err)
	}
}

func TestWalkBadElement(t *testing.T) {
	auth, _, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	for _, el := range []string{"a/b", "a\x00b", "", "../.."} {
		if _, err := auth.Walk(1, 2, []string{el}); !errors.Is(err, fidauth.ErrBadElement) {
			t.Fatalf("element %q: got %v, want ErrBadElement", el, err)
		}
	}
}

func TestWalkSymlinkEscape(t *testing.T) {
	auth, root, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	// Symlinks pointing outside the export root are rejected.
	for _, el := range []string{"escape", "escfile"} {
		if _, err := auth.Walk(1, 2, []string{el}); !errors.Is(err, fidauth.ErrEscape) {
			t.Fatalf("element %q: got %v, want ErrEscape", el, err)
		}
	}
	// A failed walk leaves the fid's path untouched.
	if p, err := auth.Path(1); err != nil || p != root {
		t.Fatalf("after failed walk: path %q, err %v", p, err)
	}
	// Symlinks resolving inside the root are fine.
	p, err := auth.Walk(1, 2, []string{"inl", "deep"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sub", "deep"); p != want {
		t.Fatalf("got %q, want %q", p, want)
	}
	// Symlink ".." from sub resolves to the root itself.
	if p, err = auth.Walk(1, 3, []string{"sub", "up"}); err != nil || p != root {
		t.Fatalf("got %q, %v; want root %q", p, err, root)
	}
}

func TestWalkDuplicateNewfid(t *testing.T) {
	auth, _, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Walk(1, 2, []string{"sub"}); err != nil {
		t.Fatal(err)
	}
	// newfid 2 is occupied and != fid: reject.
	if _, err := auth.Walk(1, 2, []string{"sub"}); !errors.Is(err, fidauth.ErrFidInUse) {
		t.Fatalf("got %v, want ErrFidInUse", err)
	}
	// newfid == fid is allowed and moves the fid itself.
	if _, err := auth.Walk(1, 1, []string{"sub"}); err != nil {
		t.Fatalf("newfid == fid: %v", err)
	}
	// Attach on a busy fid is also rejected.
	if _, err := auth.Attach(1, "export"); !errors.Is(err, fidauth.ErrFidInUse) {
		t.Fatalf("attach on busy fid: got %v, want ErrFidInUse", err)
	}
}

func TestClunkInvalidatesFid(t *testing.T) {
	auth, root, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	auth.Clunk(1)
	if _, err := auth.Walk(1, 2, []string{"sub"}); !errors.Is(err, fidauth.ErrFidInvalid) {
		t.Fatalf("walk after clunk: got %v, want ErrFidInvalid", err)
	}
	if _, err := auth.Path(1); !errors.Is(err, fidauth.ErrFidInvalid) {
		t.Fatalf("path after clunk: got %v, want ErrFidInvalid", err)
	}
	// Reuse requires a fresh attach (or walk from another live fid).
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatalf("re-attach after clunk: %v", err)
	}
	p, err := auth.Walk(1, 2, []string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sub"); p != want {
		t.Fatalf("got %q, want %q", p, want)
	}
}

func TestCloneWalk(t *testing.T) {
	auth, root, _ := tree(t)
	if _, err := auth.Attach(1, "export"); err != nil {
		t.Fatal(err)
	}
	p, err := auth.Walk(1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p != root {
		t.Fatalf("got %q, want %q", p, root)
	}
	if p, err = auth.Path(2); err != nil || p != root {
		t.Fatalf("cloned fid: path %q, err %v", p, err)
	}
}

// fakeFS resolves only the paths listed in links; everything else is its
// own canonical path. It proves authorization honors the caller-injected
// file system view.
type fakeFS struct {
	links map[string]string
}

func (f fakeFS) EvalSymlinks(p string) (string, error) {
	if target, ok := f.links[p]; ok {
		return target, nil
	}
	return p, nil
}

func TestInjectedFileSystem(t *testing.T) {
	fs := fakeFS{links: map[string]string{
		"/export/root/link": "/etc/secret",
	}}
	auth := fidauth.New(fs)
	if err := auth.RegisterExport("e", "/export/root"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Attach(7, "e"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Walk(7, 8, []string{"link"}); !errors.Is(err, fidauth.ErrEscape) {
		t.Fatalf("got %v, want ErrEscape", err)
	}
	p, err := auth.Walk(7, 8, []string{"dir", "..", "dir"})
	if err != nil {
		t.Fatal(err)
	}
	if p != "/export/root/dir" {
		t.Fatalf("got %q, want /export/root/dir", p)
	}
}
