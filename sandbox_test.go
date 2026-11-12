package netsec

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// fakeFS is an in-memory Resolver that resolves ".." lexically and
// follows symlinks, mirroring what a real filesystem walk would do.
type fakeFS struct {
	entries  map[string]bool
	symlinks map[string]string
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		entries:  map[string]bool{"/": true},
		symlinks: map[string]string{},
	}
}

func (f *fakeFS) mkdir(p string) {
	f.entries[p] = true
	for parent := parentOf(p); parent != p; parent = parentOf(parent) {
		f.entries[parent] = true
		if parent == "/" {
			break
		}
	}
}

func (f *fakeFS) touch(p string) { f.mkdir(parentOf(p)); f.entries[p] = true }

func (f *fakeFS) symlink(target, link string) {
	f.mkdir(parentOf(link))
	f.symlinks[link] = target
}

func parentOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func (f *fakeFS) Resolve(path string) (string, error) {
	return f.resolve(path, 0)
}

func (f *fakeFS) resolve(path string, depth int) (string, error) {
	if depth > 40 {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: errors.New("too many symlinks")}
	}
	if !strings.HasPrefix(path, "/") {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: errors.New("relative path")}
	}
	var stack []string
	parts := strings.Split(path, "/")
	for i, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, part)
			cur := "/" + strings.Join(stack, "/")
			if target, ok := f.symlinks[cur]; ok {
				rest := strings.Join(parts[i+1:], "/")
				return f.resolve(target+"/"+rest, depth+1)
			}
		}
	}
	resolved := "/" + strings.Join(stack, "/")
	if !f.entries[resolved] {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: fs.ErrNotExist}
	}
	return resolved, nil
}

// testFS builds: /home/user/{docs,docs/report.txt}, /safe/secret,
// /home/user/link -> /safe.
func testFS() *fakeFS {
	f := newFakeFS()
	f.touch("/home/user/docs/report.txt")
	f.touch("/safe/secret")
	f.symlink("/safe", "/home/user/link")
	return f
}

func TestPledgeOnlyNarrows(t *testing.T) {
	s := New(nil)
	if err := s.Pledge("stdio", "rpath", "wpath"); err != nil {
		t.Fatalf("initial pledge: %v", err)
	}
	if err := s.Pledge("stdio", "rpath"); err != nil {
		t.Fatalf("narrowing pledge should succeed: %v", err)
	}
	if s.Promises("wpath") {
		t.Fatal("wpath should have been dropped")
	}
	if err := s.Pledge("stdio", "rpath", "inet"); !errors.Is(err, ErrPromiseExpand) {
		t.Fatalf("re-escalation must fail with ErrPromiseExpand, got %v", err)
	}
	if err := s.Pledge("stdio", "wpath"); !errors.Is(err, ErrPromiseExpand) {
		t.Fatalf("regaining dropped promise must fail, got %v", err)
	}
	if err := s.Pledge(); err != nil {
		t.Fatalf("dropping all promises should succeed: %v", err)
	}
	if err := s.Pledge("stdio"); !errors.Is(err, ErrPromiseExpand) {
		t.Fatalf("pledge after dropping all must fail, got %v", err)
	}
}

func TestUnveilLockBlocksLateRegistration(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatalf("unveil before lock: %v", err)
	}
	s.UnveilLock()
	s.UnveilLock() // idempotent
	if err := s.Unveil("/safe", PermRead); !errors.Is(err, ErrUnveilLocked) {
		t.Fatalf("unveil after lock must fail with ErrUnveilLocked, got %v", err)
	}
	if err := s.Check("/safe/secret", PermRead); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("late-registered path must stay hidden (ENOENT), got %v", err)
	}
}

func TestUnveilDuplicateIntersectsPerms(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user/docs", PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}
	if err := s.Unveil("/home/user/docs", PermRead|PermExec); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermRead); err != nil {
		t.Fatalf("intersected read should pass: %v", err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermWrite); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("write must be intersected away (EACCES), got %v", err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermExec); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("exec must be intersected away (EACCES), got %v", err)
	}
}

func TestCheckAllowsCoveredPath(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermRead); err != nil {
		t.Fatalf("covered read should pass: %v", err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermRead|PermWrite); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("uncovered permission must be EACCES, got %v", err)
	}
}

func TestSymlinkEscapeReturnsENOENT(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatal(err)
	}
	err := s.Check("/home/user/link/secret", PermRead)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("symlink escape must be ENOENT, got %v", err)
	}
	if errors.Is(err, fs.ErrPermission) {
		t.Fatal("symlink escape must not be EACCES")
	}
}

func TestDotDotEscapeReturnsENOENT(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatal(err)
	}
	err := s.Check("/home/user/../safe/secret", PermRead)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf(".. escape must be ENOENT, got %v", err)
	}
	if errors.Is(err, fs.ErrPermission) {
		t.Fatal(".. escape must not be EACCES")
	}
}

func TestUnexposedPathReturnsENOENTNotEACCES(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatal(err)
	}
	err := s.Check("/safe/secret", PermRead)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexposed existing path must be ENOENT, got %v", err)
	}
	if errors.Is(err, fs.ErrPermission) {
		t.Fatal("unexposed path must not be EACCES")
	}
}

func TestUnveilThroughSymlinkCanonicalized(t *testing.T) {
	f := testFS()
	f.symlink("/home/user/docs", "/home/user/doclink")
	s := New(f)
	// Registering via a symlink must canonicalize to the real directory,
	// so the entry cannot be bypassed by spelling the path differently.
	if err := s.Unveil("/home/user/doclink", PermRead); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermRead); err != nil {
		t.Fatalf("canonicalized entry should cover real path: %v", err)
	}
}

func TestNestedUnveilLongestPrefixWins(t *testing.T) {
	f := testFS()
	f.touch("/home/user/docs/private/diary.txt")
	s := New(f)
	if err := s.Unveil("/home/user", PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}
	if err := s.Unveil("/home/user/docs/private", PermRead); err != nil {
		t.Fatal(err)
	}
	if err := s.Check("/home/user/docs/private/diary.txt", PermWrite); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("nested entry must restrict to read-only, got %v", err)
	}
	if err := s.Check("/home/user/docs/report.txt", PermWrite); err != nil {
		t.Fatalf("outer entry should still allow write: %v", err)
	}
}

func TestCheckPropagatesResolverError(t *testing.T) {
	s := New(testFS())
	if err := s.Unveil("/home/user", PermRead); err != nil {
		t.Fatal(err)
	}
	err := s.Check("/home/user/ghost.txt", PermRead)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("nonexistent path must surface ENOENT from resolver, got %v", err)
	}
}

func TestPermString(t *testing.T) {
	if got := (PermRead | PermCreate).String(); got != "r--c" {
		t.Fatalf("Perm.String = %q, want r--c", got)
	}
	if got := Perm(0).String(); got != "----" {
		t.Fatalf("Perm.String = %q, want ----", got)
	}
}
