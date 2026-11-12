package netsec_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	netsec "example.org/netsec/q0263"
	"example.org/netsec/q0263/fakefs"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func buildWorld(t *testing.T) *fakefs.FS {
	t.Helper()
	f := fakefs.New()
	must(t, f.MkdirAll("/root/sub/deep"))
	must(t, f.WriteFile("/root/alpha"))
	must(t, f.WriteFile("/root/sub/beta"))
	must(t, f.WriteFile("/root/sub/deep/gamma"))
	must(t, f.MkdirAll("/outside"))
	must(t, f.WriteFile("/outside/secret"))
	return f
}

func reclaimRoot(t *testing.T, f *fakefs.FS, opts *netsec.Options) (netsec.Report, error) {
	t.Helper()
	d, err := f.Open("/root")
	must(t, err)
	defer d.Close()
	return netsec.Reclaim(d, opts)
}

// assertOutsideIntact proves no capability operation ever addressed an
// object outside /root and the outside data survived.
func assertOutsideIntact(t *testing.T, f *fakefs.FS) {
	t.Helper()
	if !f.Exists("/outside/secret") {
		t.Fatal("outside object was touched")
	}
	for _, rec := range f.Ops {
		if rec.Path == "/outside" || strings.HasPrefix(rec.Path, "/outside/") {
			t.Fatalf("capability operation escaped the root: %s %s", rec.Op, rec.Path)
		}
	}
}

func TestReclaimEmptiesTree(t *testing.T) {
	f := buildWorld(t)
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.FilesRemoved != 3 || rep.DirsRemoved != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	entries, err := f.Entries("/root")
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("root not empty: %v", entries)
	}
	if !f.Exists("/root") {
		t.Fatal("Reclaim removed the root directory itself")
	}
	assertOutsideIntact(t, f)
}

func TestReclaimChildRemovesChild(t *testing.T) {
	f := buildWorld(t)
	parent, err := f.Open("/")
	must(t, err)
	defer parent.Close()
	rep, err := netsec.ReclaimChild(parent, "root", nil)
	must(t, err)
	if rep.FilesRemoved != 3 || rep.DirsRemoved != 3 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if f.Exists("/root") {
		t.Fatal("child directory still exists")
	}
	assertOutsideIntact(t, f)
}

func TestReclaimChildUnlinksSymlink(t *testing.T) {
	f := buildWorld(t)
	must(t, f.Symlink("/outside/secret", "/root/link"))
	parent, err := f.Open("/root")
	must(t, err)
	defer parent.Close()
	rep, err := netsec.ReclaimChild(parent, "link", nil)
	must(t, err)
	if rep.FilesRemoved != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if f.Exists("/root/link") {
		t.Fatal("symlink still exists")
	}
	assertOutsideIntact(t, f)
}

func TestSymlinkLoopsNeverFollowed(t *testing.T) {
	f := buildWorld(t)
	must(t, f.Symlink("/root/b", "/root/a")) // a -> b
	must(t, f.Symlink("/root/a", "/root/b")) // b -> a: a cycle
	must(t, f.Symlink("/root", "/root/self"))
	must(t, f.Symlink("/outside", "/root/escape"))
	must(t, f.Symlink("../outside", "/root/sub/up"))
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.FilesRemoved != 3+5 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	entries, err := f.Entries("/root")
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("root not empty: %v", entries)
	}
	assertOutsideIntact(t, f)
}

// The attacker replaces a file between the traversal lstat and the
// confirming lstat: the new generation must survive the round.
func TestFileReplacementBeforeConfirmIsSkipped(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	must(t, f.WriteFile("/root/victim"))
	original, err := f.Lstat("/root/victim")
	must(t, err)
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpLstat, Path: "/root/victim", N: 2,
		Fn: func(f *fakefs.FS) error {
			if err := f.RemoveAll("/root/victim"); err != nil {
				return err
			}
			return f.WriteFile("/root/victim")
		},
	})
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.FilesRemoved != 0 || rep.StaleSkipped != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	cur, err := f.Lstat("/root/victim")
	must(t, err)
	if cur.ID == original.ID {
		t.Fatal("the pre-replacement generation should be gone already")
	}
}

// The attacker swaps a directory for a symlink to /outside between
// lstat and opendir: the capability must refuse to follow it.
func TestDirSwappedForSymlinkBeforeOpen(t *testing.T) {
	f := buildWorld(t)
	must(t, f.Symlink("/outside", "/root/placeholder"))
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpOpenDir, Path: "/root/sub", N: 1,
		Fn: func(f *fakefs.FS) error {
			if err := f.RemoveAll("/root/sub"); err != nil {
				return err
			}
			return f.Symlink("/outside", "/root/sub")
		},
	})
	_, err := reclaimRoot(t, f, nil)
	if err == nil {
		t.Fatal("expected an error from the refused opendir")
	}
	info, lerr := f.Lstat("/root/sub")
	must(t, lerr)
	if info.Type != netsec.TypeSymlink {
		t.Fatalf("attacker symlink was removed or followed: %v", info.Type)
	}
	assertOutsideIntact(t, f)
}

// The attacker replaces a directory with a fresh empty one while its
// subtree is being reclaimed: rmdir must skip the new generation.
func TestDirReplacementBeforeRmdirIsSkipped(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root/sub"))
	must(t, f.WriteFile("/root/sub/file"))
	must(t, f.MkdirAll("/outside"))
	must(t, f.WriteFile("/outside/secret"))
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpLstat, Path: "/root/sub", N: 2,
		Fn: func(f *fakefs.FS) error {
			if err := f.RemoveAll("/root/sub"); err != nil {
				return err
			}
			return f.MkdirAll("/root/sub")
		},
	})
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.DirsRemoved != 0 || rep.StaleSkipped != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !f.Exists("/root/sub") {
		t.Fatal("replacement directory was removed")
	}
	assertOutsideIntact(t, f)
}

// The attacker swaps a reclaimed directory for a symlink to /outside
// before rmdir: the link must be left alone and /outside untouched.
func TestDirSwappedForSymlinkBeforeRmdir(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root/sub"))
	must(t, f.WriteFile("/root/sub/file"))
	must(t, f.MkdirAll("/outside"))
	must(t, f.WriteFile("/outside/secret"))
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpLstat, Path: "/root/sub", N: 2,
		Fn: func(f *fakefs.FS) error {
			if err := f.RemoveAll("/root/sub"); err != nil {
				return err
			}
			return f.Symlink("/outside", "/root/sub")
		},
	})
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.DirsRemoved != 0 || rep.StaleSkipped != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	info, lerr := f.Lstat("/root/sub")
	must(t, lerr)
	if info.Type != netsec.TypeSymlink {
		t.Fatal("attacker symlink was removed")
	}
	assertOutsideIntact(t, f)
}

// Entries inserted after the snapshot are left for the next round.
func TestNewEntriesLeftForNextRound(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	must(t, f.WriteFile("/root/seed"))
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpLstat, Path: "/root/seed", N: 1,
		Fn: func(f *fakefs.FS) error {
			return f.WriteFile("/root/late")
		},
	})
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.FilesRemoved != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !f.Exists("/root/late") {
		t.Fatal("entry inserted during traversal was removed")
	}
}

// An attacker creating a new entry on every removal cannot keep the
// round alive: the snapshot bounds the work.
func TestContinuousGrowthTerminates(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	must(t, f.WriteFile("/root/seed"))
	created := 0
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpUnlink, // every unlink, any path
		Fn: func(f *fakefs.FS) error {
			created++
			return f.WriteFile(fmt.Sprintf("/root/grown-%03d", created))
		},
	})
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep.FilesRemoved != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if created != 1 || !f.Exists("/root/grown-001") {
		t.Fatalf("growth not deferred to next round: created=%d", created)
	}
	if len(f.Ops) > 20 {
		t.Fatalf("round did unbounded work: %d ops", len(f.Ops))
	}
}

func TestUnlinkFailure(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	must(t, f.WriteFile("/root/bad"))
	must(t, f.WriteFile("/root/good"))
	boom := errors.New("permission denied")
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpUnlink, Path: "/root/bad", N: 1,
		Fn: func(f *fakefs.FS) error { return boom },
	})
	rep, err := reclaimRoot(t, f, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected joined boom, got %v", err)
	}
	if rep.FilesRemoved != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !f.Exists("/root/bad") {
		t.Fatal("failed unlink removed the entry")
	}
	if f.Exists("/root/good") {
		t.Fatal("sibling was not reclaimed after the failure")
	}
}

func TestRmdirFailure(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root/sub"))
	boom := errors.New("directory busy")
	f.AddHook(&fakefs.Hook{
		Op: fakefs.OpRmdir, Path: "/root/sub", N: 1,
		Fn: func(f *fakefs.FS) error { return boom },
	})
	rep, err := reclaimRoot(t, f, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected joined boom, got %v", err)
	}
	if rep.DirsRemoved != 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !f.Exists("/root/sub") {
		t.Fatal("failed rmdir removed the directory")
	}
}

// A nesting attack deeper than MaxDepth is abandoned, not chased.
func TestMaxDepthBoundsRecursion(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root/d1/d2/d3/d4/d5"))
	must(t, f.WriteFile("/root/d1/d2/d3/d4/d5/leaf"))
	must(t, f.WriteFile("/root/top"))
	rep, err := reclaimRoot(t, f, &netsec.Options{MaxDepth: 2})
	if err == nil {
		t.Fatal("expected a depth-limit error")
	}
	if rep.DepthLimited != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !f.Exists("/root/d1/d2/d3/d4/d5/leaf") {
		t.Fatal("deep subtree was chased past the limit")
	}
	if f.Exists("/root/top") {
		t.Fatal("shallow entry was not reclaimed")
	}
}

func TestReclaimEmptyDir(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	rep, err := reclaimRoot(t, f, nil)
	must(t, err)
	if rep != (netsec.Report{}) {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestReclaimChildMissing(t *testing.T) {
	f := fakefs.New()
	must(t, f.MkdirAll("/root"))
	parent, err := f.Open("/root")
	must(t, err)
	defer parent.Close()
	rep, err := netsec.ReclaimChild(parent, "gone", nil)
	must(t, err)
	if rep != (netsec.Report{}) {
		t.Fatalf("unexpected report: %+v", rep)
	}
}
