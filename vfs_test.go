package vfs

import (
	"fmt"
	"sync"
	"testing"
)

func mustResolve(t *testing.T, fs *FS, path string) Result {
	t.Helper()
	r, err := fs.Resolve(path)
	if err != nil {
		t.Fatalf("Resolve(%q) unexpected error: %v", path, err)
	}
	return r
}

func mustErr(t *testing.T, fs *FS, path string, want error) {
	t.Helper()
	if _, err := fs.Resolve(path); err != want {
		t.Fatalf("Resolve(%q) = %v, want %v", path, err, want)
	}
}

func build(t *testing.T, fs *FS) {
	t.Helper()
	for _, d := range []string{"/a/b", "/a/c", "/x/y"} {
		if err := fs.MkdirAll(d); err != nil {
			t.Fatalf("MkdirAll(%q): %v", d, err)
		}
	}
	if err := fs.Create("/a/b/f"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestDotSegmentsCleaned(t *testing.T) {
	fs := New()
	build(t, fs)
	base := mustResolve(t, fs, "/a/b")
	for _, p := range []string{"/a/./b", "/a//b", "/a/c/../b", "/a/b/.", "/x/y/../../a/b", "//a///./b//"} {
		if r := mustResolve(t, fs, p); r != base {
			t.Fatalf("Resolve(%q) = %+v, want %+v", p, r, base)
		}
	}
	root := mustResolve(t, fs, "/")
	for _, p := range []string{"/.", "/..", "/a/..", "/a/b/../.."} {
		if p == "/.." {
			continue // escape, covered elsewhere
		}
		if r := mustResolve(t, fs, p); r != root {
			t.Fatalf("Resolve(%q) = %+v, want root %+v", p, r, root)
		}
	}
}

func TestDotDotEscapingRootRejected(t *testing.T) {
	fs := New()
	build(t, fs)
	for _, p := range []string{"/..", "/../x", "/../../..", "/a/../../b", "/a/b/../../../c"} {
		mustErr(t, fs, p, ErrEscapesRoot)
	}
}

func TestRelativeAndNonAbsoluteRejected(t *testing.T) {
	fs := New()
	build(t, fs)
	for _, p := range []string{"", "a/b", "./a", "../a"} {
		mustErr(t, fs, p, ErrNotAbsolute)
	}
}

func TestSymlinkInRootJumps(t *testing.T) {
	fs := New()
	build(t, fs)
	if err := fs.Symlink("/a/b", "/link"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("../a/c", "/x/rel"); err != nil { // /x/rel -> /a/c
		t.Fatal(err)
	}
	if err := fs.Symlink("b", "/a/dirlink"); err != nil { // /a/dirlink -> /a/b
		t.Fatal(err)
	}
	if err := fs.Symlink("/", "/toroot"); err != nil {
		t.Fatal(err)
	}
	ab := mustResolve(t, fs, "/a/b")
	if r := mustResolve(t, fs, "/link"); r != ab {
		t.Fatalf("absolute symlink: got %+v want %+v", r, ab)
	}
	if r := mustResolve(t, fs, "/link/f"); r != mustResolve(t, fs, "/a/b/f") {
		t.Fatalf("symlink prefix resolution wrong: %+v", r)
	}
	if r := mustResolve(t, fs, "/x/rel"); r != mustResolve(t, fs, "/a/c") {
		t.Fatalf("relative symlink: got %+v", r)
	}
	if r := mustResolve(t, fs, "/a/dirlink"); r != ab {
		t.Fatalf("same-dir relative symlink: got %+v", r)
	}
	if r := mustResolve(t, fs, "/toroot"); r != mustResolve(t, fs, "/") {
		t.Fatalf("symlink to root: got %+v", r)
	}
	// Chained in-root jumps.
	if err := fs.Symlink("/link", "/link2"); err != nil {
		t.Fatal(err)
	}
	if r := mustResolve(t, fs, "/link2/f"); r != mustResolve(t, fs, "/a/b/f") {
		t.Fatalf("chained symlink: got %+v", r)
	}
}

func TestSymlinkEscapingRootRejected(t *testing.T) {
	fs := New()
	build(t, fs)
	links := [][2]string{
		{"/..", "/s1"},
		{"/../a", "/s2"},
		{"../../..", "/a/s3"},     // from /a: climbs past root
		{"../../../etc", "/x/s4"}, // from /x: climbs past root
		{"/a/../../b", "/s5"},
	}
	for _, l := range links {
		if err := fs.Symlink(l[0], l[1]); err != nil {
			t.Fatal(err)
		}
		mustErr(t, fs, l[1], ErrEscapesRoot)
	}
}

func TestSymlinkLoops(t *testing.T) {
	fs := New()
	build(t, fs)
	if err := fs.Symlink("/b1", "/a1"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("/a1", "/b1"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/a1", ErrLinkLoop)
	mustErr(t, fs, "/b1", ErrLinkLoop)
	if err := fs.Symlink("/self", "/self"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/self", ErrLinkLoop)
	// Loop reachable only after a legitimate in-root jump.
	if err := fs.Symlink("/a1", "/a/entry"); err != nil {
		t.Fatal(err)
	}
	mustErr(t, fs, "/a/entry", ErrLinkLoop)
}

func TestHopLimitBoundary(t *testing.T) {
	fs := New()
	build(t, fs)
	// Chain c0 -> c1 -> ... -> cN -> /a/b, exactly N+1 symlinks.
	mk := func(prefix string, n int) string {
		if err := fs.Symlink("/a/b", fmt.Sprintf("/%s%d", prefix, n)); err != nil {
			t.Fatal(err)
		}
		for i := n - 1; i >= 0; i-- {
			if err := fs.Symlink(fmt.Sprintf("/%s%d", prefix, i+1), fmt.Sprintf("/%s%d", prefix, i)); err != nil {
				t.Fatal(err)
			}
		}
		return "/" + prefix + "0"
	}
	head := mk("c", MaxHops-1) // MaxHops symlinks total: allowed
	if r := mustResolve(t, fs, head); r != mustResolve(t, fs, "/a/b") {
		t.Fatalf("chain at hop limit: got %+v", r)
	}
	head = mk("d", MaxHops) // MaxHops+1 symlinks: one too many
	mustErr(t, fs, head, ErrTooManyLinks)
}

func TestContainmentByInodeIdentityNotStringPrefix(t *testing.T) {
	fs := New()
	build(t, fs)
	// "/real" and "/realistic" share a string prefix; identity must decide.
	if err := fs.MkdirAll("/real"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/realistic"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("/real", "/lnk"); err != nil {
		t.Fatal(err)
	}
	real := mustResolve(t, fs, "/real")
	if r := mustResolve(t, fs, "/lnk"); r != real {
		t.Fatalf("identity mismatch: got %+v want %+v", r, real)
	}
	if r := mustResolve(t, fs, "/lnk"); r == mustResolve(t, fs, "/realistic") {
		t.Fatal("string-prefix confusion: /lnk resolved to /realistic")
	}
	// Canonical path is derived from the inode's parent chain, so resolving
	// through a symlink reports the real location, not the spelling used.
	if r := mustResolve(t, fs, "/lnk"); r.Path != "/real" {
		t.Fatalf("canonical path = %q, want /real", r.Path)
	}
	// Replacing /real must change the resolved inode even though the
	// string path is identical: resolution follows identity, not spelling.
	if err := fs.ReplaceWithDir("/real"); err != nil {
		t.Fatal(err)
	}
	r2 := mustResolve(t, fs, "/lnk")
	if r2.Ino == real.Ino {
		t.Fatal("stale inode after replacement")
	}
	if r2 != mustResolve(t, fs, "/real") {
		t.Fatalf("post-replacement identity mismatch: %+v", r2)
	}
}

func TestFilesAndNotDir(t *testing.T) {
	fs := New()
	build(t, fs)
	f := mustResolve(t, fs, "/a/b/f")
	if f.IsDir {
		t.Fatal("file reported as dir")
	}
	mustErr(t, fs, "/a/b/f/g", ErrNotDir)
	mustErr(t, fs, "/a/b/f/", ErrNotDir)
	if err := fs.Symlink("/a/b/f", "/flink"); err != nil {
		t.Fatal(err)
	}
	if r := mustResolve(t, fs, "/flink"); r != f {
		t.Fatalf("symlink to file: got %+v", r)
	}
	mustErr(t, fs, "/flink/g", ErrNotDir)
	mustErr(t, fs, "/a/b/nope", ErrNotExist)
}

func TestMutationErrors(t *testing.T) {
	fs := New()
	build(t, fs)
	if err := fs.Create("/a/b/f"); err != ErrExist {
		t.Fatalf("Create existing = %v", err)
	}
	if err := fs.Symlink("/a", "/a/b"); err != ErrExist {
		t.Fatalf("Symlink existing = %v", err)
	}
	if err := fs.Create("/a/b/f/g"); err != ErrNotDir {
		t.Fatalf("Create under file = %v", err)
	}
	if err := fs.MkdirAll("/../escape"); err != ErrEscapesRoot {
		t.Fatalf("MkdirAll escape = %v", err)
	}
	if err := fs.MkdirAll("relative"); err != ErrNotAbsolute {
		t.Fatalf("MkdirAll relative = %v", err)
	}
	// MkdirAll follows symlinked parents.
	if err := fs.Symlink("/a", "/alias"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/alias/newdir"); err != nil {
		t.Fatal(err)
	}
	if r := mustResolve(t, fs, "/a/newdir"); !r.IsDir {
		t.Fatalf("MkdirAll through symlink failed: %+v", r)
	}
}

// TestReplacementRace hammers Resolve while /a is atomically swapped between
// two symlink targets. Every observation must be one of the two consistent
// outcomes; a resolution may never mix pre- and post-replacement state.
func TestReplacementRace(t *testing.T) {
	fs := New()
	if err := fs.MkdirAll("/b"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Create("/b/f"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/c"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Create("/c/f"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("/b", "/a"); err != nil {
		t.Fatal(err)
	}
	wantB := mustResolve(t, fs, "/b/f")
	wantC := mustResolve(t, fs, "/c/f")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := "/b"
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := fs.ReplaceWithSymlink("/a", target); err != nil {
					t.Errorf("replace: %v", err)
					return
				}
				if target == "/b" {
					target = "/c"
				} else {
					target = "/b"
				}
			}
		}(i)
	}
	for i := 0; i < 20000; i++ {
		r, err := fs.Resolve("/a/f")
		if err != nil {
			t.Fatalf("Resolve under race: %v", err)
		}
		if r != wantB && r != wantC {
			t.Fatalf("inconsistent resolution under race: %+v", r)
		}
	}
	close(stop)
	wg.Wait()
}

// TestReplacementRaceDirSwap swaps /d between a real directory (rebuilt with
// its child) and a symlink. Outcomes must be a consistent whole state:
// either the directory view, the symlink view, or a clean NotExist while the
// directory is being rebuilt — never a torn mix.
func TestReplacementRaceDirSwap(t *testing.T) {
	fs := New()
	if err := fs.MkdirAll("/target"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Create("/target/f"); err != nil {
		t.Fatal(err)
	}
	if err := fs.ReplaceWithDir("/d"); err != nil {
		t.Fatal(err)
	}
	wantLink := mustResolve(t, fs, "/target/f")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		link := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if link {
				if err := fs.ReplaceWithSymlink("/d", "/target"); err != nil {
					t.Errorf("swap to symlink: %v", err)
					return
				}
			} else {
				if err := fs.ReplaceWithDir("/d"); err != nil {
					t.Errorf("swap to dir: %v", err)
					return
				}
				if err := fs.Create("/d/f"); err != nil {
					t.Errorf("populate dir: %v", err)
					return
				}
			}
			link = !link
		}
	}()

	for i := 0; i < 20000; i++ {
		r, err := fs.Resolve("/d/f")
		if err == ErrNotExist {
			continue // directory view momentarily without its child
		}
		if err != nil {
			t.Fatalf("Resolve under dir swap: %v", err)
		}
		if r == wantLink {
			continue // symlink view
		}
		// Directory view: canonical path must be /d/f and the inode must be
		// contained in root and distinct from the symlink target's inode.
		if r.Path != "/d/f" {
			t.Fatalf("torn resolution: %+v", r)
		}
		if r.Ino == wantLink.Ino {
			t.Fatalf("identity collision: %+v", r)
		}
	}
	close(stop)
	wg.Wait()
}
