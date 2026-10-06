package dirindex

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func mustUsage(t *testing.T, ix *Index, p string, want int64) {
	t.Helper()
	got, err := ix.Usage(p)
	if err != nil {
		t.Fatalf("Usage(%q) error: %v", p, err)
	}
	if got != want {
		t.Fatalf("Usage(%q) = %d, want %d", p, got, want)
	}
	model, err := ix.ModelUsage(p)
	if err != nil {
		t.Fatalf("ModelUsage(%q) error: %v", p, err)
	}
	if model != want {
		t.Fatalf("ModelUsage(%q) = %d, want %d", p, model, want)
	}
}

func mustCheck(t *testing.T, ix *Index) {
	t.Helper()
	if err := ix.Check(); err != nil {
		t.Fatal(err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Cross-subtree hard links: the same inode linked under two sibling
// subtrees is deduplicated independently per subtree. The parent's
// usage is therefore smaller than the sum of its children's usages,
// which is exactly why child usages must never be added together.
func TestCrossSubtreeHardlinks(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a"))
	mustOK(t, ix.Mkdir("/b"))
	mustOK(t, ix.AddFile("/a", "f1", 1, 100))
	mustOK(t, ix.AddFile("/b", "f2", 1, 100))
	mustOK(t, ix.AddFile("/b", "g", 2, 40))

	mustUsage(t, ix, "/a", 100)
	mustUsage(t, ix, "/b", 140)
	mustUsage(t, ix, "/", 140)

	ua, _ := ix.Usage("/a")
	ub, _ := ix.Usage("/b")
	ur, _ := ix.Usage("/")
	if ua+ub != 240 || ur != 140 {
		t.Fatalf("ua+ub=%d ur=%d, want 240 and 140", ua+ub, ur)
	}
	mustCheck(t, ix)
}

// Two links to the same inode within one subtree count once there.
func TestSameSubtreeHardlinkDedup(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a/x"))
	mustOK(t, ix.AddFile("/a", "f1", 1, 100))
	mustOK(t, ix.AddFile("/a/x", "f2", 1, 100))

	mustUsage(t, ix, "/a", 100)
	mustUsage(t, ix, "/a/x", 100)
	mustUsage(t, ix, "/", 100)
	mustCheck(t, ix)
}

// Deleting links: usage only drops when the last link to an inode
// within that subtree is removed; the inode is forgotten entirely when
// its last link in the whole index is removed.
func TestDeleteLastLink(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a"))
	mustOK(t, ix.Mkdir("/b"))
	mustOK(t, ix.AddFile("/a", "f1", 1, 100))
	mustOK(t, ix.AddFile("/a", "f2", 1, 100))
	mustOK(t, ix.AddFile("/b", "f3", 1, 100))

	mustOK(t, ix.RemoveFile("/a", "f1"))
	mustUsage(t, ix, "/a", 100)
	mustUsage(t, ix, "/", 100)

	mustOK(t, ix.RemoveFile("/a", "f2"))
	mustUsage(t, ix, "/a", 0)
	mustUsage(t, ix, "/b", 100)
	mustUsage(t, ix, "/", 100)

	mustOK(t, ix.RemoveFile("/b", "f3"))
	mustUsage(t, ix, "/", 0)
	mustOK(t, ix.AddFile("/b", "f4", 1, 555))
	mustUsage(t, ix, "/", 555)
	mustCheck(t, ix)
}

// Moving a directory updates every ancestor summary on both the old
// and the new chain, including dedup changes caused by links that
// cross the moved boundary. Root usage is invariant under Move.
func TestMoveUpdatesAncestors(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a/sub/deep"))
	mustOK(t, ix.Mkdir("/b"))
	mustOK(t, ix.AddFile("/a/sub", "f", 1, 100))
	mustOK(t, ix.AddFile("/a/sub/deep", "g", 1, 100))
	mustOK(t, ix.AddFile("/a/sub/deep", "h", 2, 30))
	mustOK(t, ix.AddFile("/b", "k", 1, 100))
	mustOK(t, ix.AddFile("/a", "top", 3, 7))

	mustUsage(t, ix, "/a", 137)
	mustUsage(t, ix, "/a/sub", 130)
	mustUsage(t, ix, "/b", 100)
	mustUsage(t, ix, "/", 137)

	rootBefore, _ := ix.Usage("/")
	mustOK(t, ix.Move("/a/sub", "/b"))

	mustUsage(t, ix, "/a", 7)
	mustUsage(t, ix, "/b", 130)
	mustUsage(t, ix, "/b/sub", 130)
	mustUsage(t, ix, "/b/sub/deep", 130)
	mustUsage(t, ix, "/", rootBefore)
	mustCheck(t, ix)

	mustOK(t, ix.Move("/b/sub", "/a"))
	mustUsage(t, ix, "/a", 137)
	mustUsage(t, ix, "/b", 100)
	mustUsage(t, ix, "/", rootBefore)
	mustCheck(t, ix)
}

func TestMoveErrors(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a/sub"))
	mustOK(t, ix.Mkdir("/b"))
	mustOK(t, ix.AddFile("/b", "sub", 1, 10))

	if err := ix.Move("/", "/b"); err != ErrMoveRoot {
		t.Fatalf("move root: got %v", err)
	}
	if err := ix.Move("/a", "/a/sub"); err != ErrMoveIntoSelf {
		t.Fatalf("move into self: got %v", err)
	}
	if err := ix.Move("/a", "/a"); err != ErrMoveIntoSelf {
		t.Fatalf("move into itself: got %v", err)
	}
	if err := ix.Move("/a/sub", "/nope"); err != ErrNotFound {
		t.Fatalf("move to missing dir: got %v", err)
	}
	if err := ix.Move("/a/sub", "/b"); err != ErrExist {
		t.Fatalf("move onto existing name: got %v", err)
	}
	mustCheck(t, ix)
}

// Snapshots are immutable point-in-time copies: later mutations must
// not change them, and a fresh snapshot reflects the new state.
func TestSnapshot(t *testing.T) {
	ix := New()
	mustOK(t, ix.Mkdir("/a"))
	mustOK(t, ix.Mkdir("/b"))
	mustOK(t, ix.AddFile("/a", "f", 1, 100))
	mustOK(t, ix.AddFile("/b", "g", 1, 100))

	snap1 := ix.Snapshot()
	for p, v := range snap1 {
		model, err := ix.ModelUsage(p)
		if err != nil || model != v {
			t.Fatalf("snapshot[%q]=%d, model=%d, err=%v", p, v, model, err)
		}
	}

	mustOK(t, ix.RemoveFile("/b", "g"))
	mustOK(t, ix.Move("/a", "/b"))
	mustOK(t, ix.AddFile("/b", "h", 2, 5))

	if v, ok := snap1.Usage("/a"); !ok || v != 100 {
		t.Fatalf("snap1[/a] = %d, %v; want 100, true", v, ok)
	}
	if v, ok := snap1.Usage("/b"); !ok || v != 100 {
		t.Fatalf("snap1[/b] = %d, %v; want 100, true", v, ok)
	}
	if v, ok := snap1.Usage("/"); !ok || v != 100 {
		t.Fatalf("snap1[/] = %d, %v; want 100, true", v, ok)
	}
	if _, ok := snap1.Usage("/b/a"); ok {
		t.Fatalf("snap1 must not contain /b/a")
	}

	snap2 := ix.Snapshot()
	if err := ix.Check(); err != nil {
		t.Fatal(err)
	}
	for _, p := range ix.Paths() {
		v, ok := snap2.Usage(p)
		if !ok {
			t.Fatalf("snap2 missing %q", p)
		}
		model, _ := ix.ModelUsage(p)
		if v != model {
			t.Fatalf("snap2[%q]=%d, model=%d", p, v, model)
		}
	}
	if len(snap2) != len(ix.Paths()) {
		t.Fatalf("snap2 has %d entries, index has %d dirs", len(snap2), len(ix.Paths()))
	}
}

func TestInodeSizeConflict(t *testing.T) {
	ix := New()
	mustOK(t, ix.AddFile("/", "f", 1, 100))
	if err := ix.AddFile("/", "g", 1, 200); err != ErrInodeSizeConflict {
		t.Fatalf("got %v, want ErrInodeSizeConflict", err)
	}
	mustCheck(t, ix)
}

// Randomized differential test: apply random mutations and, after every
// single one, cross-check the incrementally maintained usage of every
// directory against the per-subtree inode-set model. Inode sizes are
// derived deterministically from the inode number so repeated links to
// the same inode always agree on size.
func TestRandomizedDifferential(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ix := New()
			type fileRef struct {
				dir, name string
			}
			var files []fileRef
			sizeOf := func(inode uint64) int64 { return int64(inode%97+1) * 100 }

			for step := 0; step < 600; step++ {
				dirs := ix.Paths()
				switch rng.Intn(5) {
				case 0:
					parent := dirs[rng.Intn(len(dirs))]
					name := fmt.Sprintf("d%d", rng.Intn(6))
					_ = ix.Mkdir(parent + "/" + name)
				case 1, 2:
					dir := dirs[rng.Intn(len(dirs))]
					name := fmt.Sprintf("f%d", rng.Intn(10))
					inode := uint64(rng.Intn(12))
					if err := ix.AddFile(dir, name, inode, sizeOf(inode)); err == nil {
						files = append(files, fileRef{dir, name})
					}
				case 3:
					if len(files) == 0 {
						continue
					}
					i := rng.Intn(len(files))
					f := files[i]
					if err := ix.RemoveFile(f.dir, f.name); err != nil {
						t.Fatalf("step %d: RemoveFile(%q, %q): %v", step, f.dir, f.name, err)
					}
					files = append(files[:i], files[i+1:]...)
				case 4:
					if len(dirs) < 2 {
						continue
					}
					src := dirs[1+rng.Intn(len(dirs)-1)]
					dst := dirs[rng.Intn(len(dirs))]
					rootBefore, _ := ix.Usage("/")
					if err := ix.Move(src, dst); err == nil {
						rootAfter, _ := ix.Usage("/")
						if rootAfter != rootBefore {
							t.Fatalf("step %d: root usage changed by move: %d -> %d",
								step, rootBefore, rootAfter)
						}
						base := src[strings.LastIndex(src, "/"):]
						if dst != "/" {
							base = dst + base
						} else {
							base = "/" + base[1:]
						}
						for i, f := range files {
							if f.dir == src {
								files[i].dir = base
							} else if strings.HasPrefix(f.dir, src+"/") {
								files[i].dir = base + f.dir[len(src):]
							}
						}
					}
				}
				if err := ix.Check(); err != nil {
					t.Fatalf("step %d: %v", step, err)
				}
				if step%50 == 0 {
					snap := ix.Snapshot()
					for p, v := range snap {
						model, err := ix.ModelUsage(p)
						if err != nil || model != v {
							t.Fatalf("step %d: snapshot[%q]=%d model=%d err=%v",
								step, p, v, model, err)
						}
					}
				}
			}
		})
	}
}
