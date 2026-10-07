package vfs

import (
	"fmt"
	"sync"
	"testing"
)

func mustResolve(t *testing.T, fs *FS, path string) *Node {
	t.Helper()
	n, err := fs.Resolve(path)
	if err != nil {
		t.Fatalf("Resolve(%q) unexpected error: %v", path, err)
	}
	return n
}

func mustErr(t *testing.T, fs *FS, path string, want error) {
	t.Helper()
	if _, err := fs.Resolve(path); err != want {
		t.Fatalf("Resolve(%q) = %v, want %v", path, err, want)
	}
}

func buildTree(t *testing.T) *FS {
	t.Helper()
	fs := New()
	if _, err := fs.MkdirAll("/a/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MkdirAll("/x/y"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.AddFile("/a/b/f"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.AddFile("/x/y/g"); err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestDotSegmentsCleaned(t *testing.T) {
	fs := buildTree(t)
	want := mustResolve(t, fs, "/a/b/f")
	for _, p := range []string{
		"/a/./b//f",
		"./a/b/./f",
		"/a/b/../b/f",
		"/x/y/../../a/b/f",
		"a//b/f",
	} {
		if got := mustResolve(t, fs, p); got != want {
			t.Errorf("Resolve(%q) resolved to wrong inode", p)
		}
	}
	if got := mustResolve(t, fs, "/./"); got != fs.Root() {
		t.Error("Resolve(/./) should be root")
	}
}

func TestDotDotAboveRootRejected(t *testing.T) {
	fs := buildTree(t)
	for _, p := range []string{"/..", "/../a", "/a/../../b", "..", "a/../../.."} {
		mustErr(t, fs, p, ErrEscapesRoot)
	}
}

func TestSymlinkWithinRoot(t *testing.T) {
	fs := buildTree(t)
	// Relative target resolved against the containing directory.
	if _, err := fs.AddSymlink("/a/link", "../x/y"); err != nil {
		t.Fatal(err)
	}
	if got, want := mustResolve(t, fs, "/a/link/g"), mustResolve(t, fs, "/x/y/g"); got != want {
		t.Error("relative symlink did not land on /x/y/g")
	}
	// Absolute target is jailed to the virtual root.
	if _, err := fs.AddSymlink("/abs", "/a/b"); err != nil {
		t.Fatal(err)
	}
	if got, want := mustResolve(t, fs, "/abs/f"), mustResolve(t, fs, "/a/b/f"); got != want {
		t.Error("absolute symlink did not land on /a/b/f")
	}
	// ".." inside a link target that stays within the root is fine,
	// and is resolved by inode, not by string prefix.
	if _, err := fs.AddSymlink("/x/back", "../a/b"); err != nil {
		t.Fatal(err)
	}
	if got, want := mustResolve(t, fs, "/x/back/f"), mustResolve(t, fs, "/a/b/f"); got != want {
		t.Error("symlink with in-root .. did not land on /a/b/f")
	}
	// ".." after a symlink climbs the *physical* parent of the target.
	if got, want := mustResolve(t, fs, "/abs/../b/f"), mustResolve(t, fs, "/a/b/f"); got != want {
		t.Error(".. after symlink should climb the target's physical parent")
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	fs := buildTree(t)
	// A string-prefix check would happily accept these targets; the
	// inode walk must reject them once ".." reaches the root.
	if _, err := fs.AddSymlink("/a/evil", "../../../etc"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/a/evil", ErrEscapesRoot)
	mustErr(t, fs, "/a/evil/passwd", ErrEscapesRoot)
	if _, err := fs.AddSymlink("/top", "/.."); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/top", ErrEscapesRoot)
}

func TestSymlinkLoops(t *testing.T) {
	fs := New()
	if _, err := fs.AddSymlink("/a", "/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.AddSymlink("/b", "/a"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/a", ErrTooManyLinks)
	if _, err := fs.AddSymlink("/self", "/self"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/self", ErrTooManyLinks)
}

func TestSymlinkHopLimit(t *testing.T) {
	fs := New()
	if _, err := fs.AddFile("/file"); err != nil {
		t.Fatal(err)
	}
	// Chain of exactly MaxSymlinkHops links resolves.
	prev := "/file"
	for i := MaxSymlinkHops - 1; i >= 0; i-- {
		name := fmt.Sprintf("/l%d", i)
		if _, err := fs.AddSymlink(name, prev); err != nil {
			t.Fatal(err)
		}
		prev = name
	}
	if got, want := mustResolve(t, fs, "/l0"), mustResolve(t, fs, "/file"); got != want {
		t.Error("40-link chain should resolve to /file")
	}
	// One more link in the chain exceeds the limit.
	if _, err := fs.AddSymlink("/head", "/l0"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/head", ErrTooManyLinks)
}

func TestNotDirAndNotExist(t *testing.T) {
	fs := buildTree(t)
	mustErr(t, fs, "/a/b/f/g", ErrNotDir)
	mustErr(t, fs, "/a/nope", ErrNotExist)
}

// TestReplaceBeforeLookup swaps a directory for a symlink *before* the
// walk reaches it: the fresh lookup must follow the replacement,
// proving resolution is keyed on per-step inode lookups, not on a
// cached string prefix.
func TestReplaceBeforeLookup(t *testing.T) {
	fs := buildTree(t)
	if _, err := fs.AddFile("/x/y/f"); err != nil {
		t.Fatal(err)
	}
	steps := 0
	fs.preStep = func() {
		steps++
		if steps == 1 {
			// Before "a" is looked up, /a becomes a symlink to /x/y.
			if _, err := fs.Replace("/a", Symlink, "/x/y"); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { fs.preStep = nil }()
	got, err := fs.Resolve("/a/f")
	if err != nil {
		t.Fatal(err)
	}
	if want := mustResolve(t, fs, "/x/y/f"); got != want {
		t.Error("walk did not follow the replacement symlink")
	}
}

// TestReplaceAfterLookup swaps the directory *after* the walk has
// already looked it up: the walk stays pinned to the old inode, which
// is the correct POSIX open-by-inode behaviour and cannot escape.
func TestReplaceAfterLookup(t *testing.T) {
	fs := buildTree(t)
	old := mustResolve(t, fs, "/a/b/f")
	steps := 0
	fs.preStep = func() {
		steps++
		if steps == 3 {
			// "a" and "b" already resolved; replace /a/b.
			if _, err := fs.Replace("/a/b", Symlink, "/x/y"); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { fs.preStep = nil }()
	got, err := fs.Resolve("/a/b/f")
	if err != nil {
		t.Fatal(err)
	}
	if got != old {
		t.Error("walk should stay pinned to the inode it already holds")
	}
	if p, ok := fs.Path(got); !ok || p != "/a/b/f" {
		t.Errorf("result escaped the tree: %q ok=%v", p, ok)
	}
}

// TestConcurrentReplace hammers the tree with replacements while
// resolving; every result must be either a defined error or an inode
// still reachable from the root. Run with -race.
func TestConcurrentReplace(t *testing.T) {
	fs := buildTree(t)
	if _, err := fs.AddFile("/x/y/f"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Mutator: /a/b flips between a real directory (with f) and
	// symlinks, including escape attempts.
	var mwg sync.WaitGroup
	mwg.Add(1)
	go func() {
		defer mwg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			switch i % 3 {
			case 0:
				if _, err := fs.Replace("/a/b", Dir, ""); err == nil {
					fs.Replace("/a/b/f", File, "")
				}
			case 1:
				fs.Replace("/a/b", Symlink, "/x/y")
			case 2:
				fs.Replace("/a/b", Symlink, "../../../etc")
			}
		}
	}()

	// Resolvers.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				n, err := fs.Resolve("/a/b/f")
				if err != nil {
					switch err {
					case ErrNotExist, ErrEscapesRoot, ErrNotDir, ErrTooManyLinks:
					default:
						t.Errorf("unexpected error: %v", err)
						return
					}
					continue
				}
				if _, ok := fs.Path(n); !ok {
					t.Error("resolved node is not reachable from root")
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	mwg.Wait()
}
