package netsec

import (
	"errors"
	"fmt"
	"testing"
)

func newResolver(fs FS) *Resolver {
	return &Resolver{FS: fs}
}

func TestMultiLevelDotDot(t *testing.T) {
	fs := NewMemFS()
	fs.AddDir("a")
	fs.AddDir("a/b")
	fs.AddDir("a/b/c")
	fs.AddFile("a/b/c/f.txt")
	fs.AddFile("a/top.txt")
	fs.AddFile("top.txt")

	r := newResolver(fs)

	res, err := r.Resolve(Request{Path: "a/b/c/../../top.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "a/top.txt" {
		t.Fatalf("got %q, want %q", res.Path, "a/top.txt")
	}

	res, err = r.Resolve(Request{Path: "a/b/c/../../../top.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "top.txt" {
		t.Fatalf("got %q, want %q", res.Path, "top.txt")
	}

	// ".." at the root would escape the preopen directory.
	for _, p := range []string{"..", "a/../..", "a/b/c/../../../..", "a/../../top.txt"} {
		if _, err := r.Resolve(Request{Path: p}); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("path %q: got %v, want ErrEscapesRoot", p, err)
		}
	}

	// "." segments are harmless no-ops.
	res, err = r.Resolve(Request{Path: "./a/./b/../top.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "a/top.txt" {
		t.Fatalf("got %q, want %q", res.Path, "a/top.txt")
	}
}

func TestAbsoluteAndEmptySegmentsRejected(t *testing.T) {
	fs := NewMemFS()
	fs.AddFile("a")
	r := newResolver(fs)

	for _, p := range []string{"/etc/passwd", "/a", "//", "/", "/a/b"} {
		if _, err := r.Resolve(Request{Path: p}); !errors.Is(err, ErrAbsolutePath) {
			t.Fatalf("path %q: got %v, want ErrAbsolutePath", p, err)
		}
	}
	for _, p := range []string{"", "a//b", "a/"} {
		if _, err := r.Resolve(Request{Path: p}); !errors.Is(err, ErrEmptySegment) {
			t.Fatalf("path %q: got %v, want ErrEmptySegment", p, err)
		}
	}
}

func TestSymlinkLoop(t *testing.T) {
	fs := NewMemFS()
	fs.AddSymlink("loop", "loop")
	fs.AddSymlink("l1", "l2")
	fs.AddSymlink("l2", "l1")
	r := newResolver(fs)

	for _, p := range []string{"loop", "l1", "l2"} {
		_, err := r.Resolve(Request{Path: p, PathFlags: PathFlagSymlinkFollow})
		if !errors.Is(err, ErrSymlinkLoop) {
			t.Fatalf("path %q: got %v, want ErrSymlinkLoop", p, err)
		}
	}

	// Intermediate symlinks are always expanded, so loops are caught
	// even without symlink-follow.
	if _, err := r.Resolve(Request{Path: "l1/x"}); !errors.Is(err, ErrSymlinkLoop) {
		t.Fatalf("got %v, want ErrSymlinkLoop", err)
	}
}

func TestSymlinkExpansionLimit(t *testing.T) {
	fs := NewMemFS()
	fs.AddFile("real")
	// Chain of exactly 8 symlinks: l1 -> l2 -> ... -> l8 -> real.
	for i := 1; i <= 8; i++ {
		target := "real"
		if i < 8 {
			target = fmt.Sprintf("l%d", i+1)
		}
		fs.AddSymlink(fmt.Sprintf("l%d", i), target)
	}
	// Chain of 9 symlinks: m1 -> ... -> m9 -> real.
	for i := 1; i <= 9; i++ {
		target := "real"
		if i < 9 {
			target = fmt.Sprintf("m%d", i+1)
		}
		fs.AddSymlink(fmt.Sprintf("m%d", i), target)
	}
	r := newResolver(fs)

	res, err := r.Resolve(Request{Path: "l1", PathFlags: PathFlagSymlinkFollow})
	if err != nil {
		t.Fatalf("8 expansions must succeed: %v", err)
	}
	if res.Path != "real" {
		t.Fatalf("got %q, want %q", res.Path, "real")
	}

	if _, err := r.Resolve(Request{Path: "m1", PathFlags: PathFlagSymlinkFollow}); !errors.Is(err, ErrSymlinkLoop) {
		t.Fatalf("9 expansions: got %v, want ErrSymlinkLoop", err)
	}
}

func TestFinalSymlinkFollowVsNoFollow(t *testing.T) {
	fs := NewMemFS()
	fs.AddDir("dir")
	fs.AddFile("dir/real.txt")
	fs.AddSymlink("link", "dir/real.txt")
	fs.AddSymlink("dirlink", "dir")
	r := newResolver(fs)

	// Without symlink-follow the final segment stays the symlink itself.
	res, err := r.Resolve(Request{Path: "link"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "link" || res.FinalEntry.Type != EntrySymlink {
		t.Fatalf("no-follow: got (%q, %v), want (link, symlink)", res.Path, res.FinalEntry.Type)
	}

	// With symlink-follow the final symlink is expanded.
	res, err = r.Resolve(Request{Path: "link", PathFlags: PathFlagSymlinkFollow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "dir/real.txt" || res.FinalEntry.Type != EntryFile {
		t.Fatalf("follow: got (%q, %v), want (dir/real.txt, file)", res.Path, res.FinalEntry.Type)
	}

	// Intermediate symlinks are expanded regardless of symlink-follow.
	res, err = r.Resolve(Request{Path: "dirlink/real.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "dir/real.txt" {
		t.Fatalf("got %q, want %q", res.Path, "dir/real.txt")
	}
}

func TestSymlinkEscapeAttempts(t *testing.T) {
	fs := NewMemFS()
	fs.AddDir("d")
	fs.AddFile("sibling.txt")
	fs.AddSymlink("evil-dotdot", "../../etc/passwd")
	fs.AddSymlink("evil-abs", "/etc/passwd")
	fs.AddSymlink("d/ok", "../sibling.txt")
	r := newResolver(fs)

	for _, p := range []string{"evil-dotdot", "evil-abs"} {
		_, err := r.Resolve(Request{Path: p, PathFlags: PathFlagSymlinkFollow})
		if !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("path %q: got %v, want ErrEscapesRoot", p, err)
		}
	}

	// A ".." inside a symlink target that stays within the root is fine.
	res, err := r.Resolve(Request{Path: "d/ok", PathFlags: PathFlagSymlinkFollow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "sibling.txt" {
		t.Fatalf("got %q, want %q", res.Path, "sibling.txt")
	}
}

func TestReadOnlyParentDowngrade(t *testing.T) {
	fs := NewMemFS()
	fs.AddFile("f.txt")
	r := newResolver(fs)
	readOnly := FlagRead | FlagFileIntegritySync

	// Requested write is silently dropped: the child never carries write.
	res, err := r.Resolve(Request{
		Path:      "f.txt",
		Requested: FlagRead | FlagWrite,
		Parent:    readOnly,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Flags&FlagWrite != 0 {
		t.Fatalf("child of read-only parent must not have write, got %v", res.Flags)
	}
	if res.Flags != FlagRead {
		t.Fatalf("got flags %v, want FlagRead", res.Flags)
	}

	// Open-flags that require write fail outright under a read-only parent.
	for _, of := range []OpenFlags{OpenCreate, OpenTruncate, OpenCreate | OpenExclusive} {
		_, err := r.Resolve(Request{
			Path:      "f.txt",
			OpenFlags: of,
			Requested: FlagRead | FlagWrite,
			Parent:    readOnly,
		})
		if !errors.Is(err, ErrNotCapable) {
			t.Fatalf("open-flags %v: got %v, want ErrNotCapable", of, err)
		}
	}

	// A subset of the parent's rights passes through unchanged.
	res, err = r.Resolve(Request{
		Path:      "f.txt",
		Requested: FlagRead,
		Parent:    FlagRead | FlagWrite,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Flags != FlagRead {
		t.Fatalf("got flags %v, want FlagRead", res.Flags)
	}

	// Write-capable parent plus write request keeps write.
	res, err = r.Resolve(Request{
		Path:      "f.txt",
		OpenFlags: OpenTruncate,
		Requested: FlagRead | FlagWrite,
		Parent:    FlagRead | FlagWrite,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Flags&FlagWrite == 0 {
		t.Fatalf("write-capable parent: got flags %v, want write set", res.Flags)
	}
}

func TestOpenDirectoryAndCreate(t *testing.T) {
	fs := NewMemFS()
	fs.AddDir("d")
	fs.AddFile("f.txt")
	r := newResolver(fs)
	rw := FlagRead | FlagWrite

	if _, err := r.Resolve(Request{Path: "f.txt", OpenFlags: OpenDirectory}); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("got %v, want ErrNotDirectory", err)
	}
	res, err := r.Resolve(Request{Path: "d", OpenFlags: OpenDirectory})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.FinalEntry.Type != EntryDirectory {
		t.Fatalf("got %v, want directory", res.FinalEntry.Type)
	}

	// OpenCreate on a missing final segment resolves the would-be path.
	res, err = r.Resolve(Request{
		Path:      "d/new.txt",
		OpenFlags: OpenCreate,
		Requested: rw,
		Parent:    rw,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Path != "d/new.txt" || res.Exists {
		t.Fatalf("got (%q, exists=%v), want (d/new.txt, false)", res.Path, res.Exists)
	}

	// OpenCreate does not resurrect missing intermediate directories.
	if _, err := r.Resolve(Request{
		Path:      "missing/new.txt",
		OpenFlags: OpenCreate,
		Requested: rw,
		Parent:    rw,
	}); !errors.Is(err, ErrNotExist) {
		t.Fatalf("got %v, want ErrNotExist", err)
	}

	// Traversing through a non-directory intermediate segment fails.
	if _, err := r.Resolve(Request{Path: "f.txt/x"}); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("got %v, want ErrNotDirectory", err)
	}
}
