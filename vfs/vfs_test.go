package vfs_test

import (
	"errors"
	"sync"
	"testing"

	"formal.local/w27-6467acc7b4/vfs"
)

func TestTreeMutationAndLookup(t *testing.T) {
	fs := vfs.New()
	dir, err := fs.Mkdir(fs.HostRoot, "d", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	file, err := fs.Mkfile(dir, "f", []byte("hi"), 0o644, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	link, err := fs.Symlink(dir, "l", "f", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if link.Kind() != vfs.KindSymlink || link.Target() != "f" {
		t.Fatal("symlink attributes wrong")
	}
	got, err := dir.Child("f")
	if err != nil || got != file {
		t.Fatalf("lookup mismatch: %v %v", got, err)
	}
	if _, err := dir.Child("missing"); !errors.Is(err, vfs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if vfs.PathOf(file) != "/d/f" {
		t.Fatalf("path = %q", vfs.PathOf(file))
	}
	if !vfs.IsAncestor(dir, file) || vfs.IsAncestor(file, dir) {
		t.Fatal("ancestor predicate wrong")
	}
}

func TestAtomicExchange(t *testing.T) {
	fs := vfs.New()
	root, _ := fs.Mkdir(fs.HostRoot, "root", 0o755, 0, 0)
	a, _ := fs.Mkdir(root, "a", 0o755, 0, 0)
	b, _ := fs.Mkdir(root, "b", 0o755, 0, 0)
	fa, _ := fs.Mkfile(a, "x", []byte("A"), 0o644, 0, 0)
	fb, _ := fs.Mkfile(b, "x", []byte("B"), 0o644, 0, 0)

	if err := fs.Exchange(root, "a", root, "b"); err != nil {
		t.Fatal(err)
	}
	// Directory nodes swap slots while their contents stay attached. Held
	// node pointers remain valid throughout the atomic exchange.
	newA, err := root.Child("a")
	if err != nil {
		t.Fatal(err)
	}
	newB, err := root.Child("b")
	if err != nil {
		t.Fatal(err)
	}
	if newA != b || newB != a {
		t.Fatal("directory slots were not swapped atomically")
	}
	if fa.Parent() != a || fb.Parent() != b {
		t.Fatal("child linkage must stay with its directory")
	}
	if vfs.PathOf(fa) != "/root/b/x" {
		t.Fatalf("exchanged path = %q", vfs.PathOf(fa))
	}
}

func TestRenameKeepsNodePointerStable(t *testing.T) {
	fs := vfs.New()
	src, _ := fs.Mkdir(fs.HostRoot, "src", 0o755, 0, 0)
	dst, _ := fs.Mkdir(fs.HostRoot, "dst", 0o755, 0, 0)
	node, _ := fs.Mkfile(src, "n", []byte("x"), 0o644, 0, 0)
	held := node
	if err := fs.Rename(src, "n", dst, "n2"); err != nil {
		t.Fatal(err)
	}
	if held.Parent() != dst || held.Name() != "n2" {
		t.Fatal("renamed node pointer must remain valid with new linkage")
	}
	if _, err := src.Child("n"); !errors.Is(err, vfs.ErrNotExist) {
		t.Fatal("old entry must be gone")
	}
}

func TestConcurrentExchangeSafety(t *testing.T) {
	fs := vfs.New()
	root, _ := fs.Mkdir(fs.HostRoot, "root", 0o755, 0, 0)
	if _, err := fs.Mkdir(root, "a", 0o755, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkdir(root, "b", 0o755, 0, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = fs.Exchange(root, "a", root, "b")
			}
		}()
	}
	for i := 0; i < 200; i++ {
		names := vfs.SortedNames(root)
		if len(names) != 2 || names[0] != "a" || names[1] != "b" {
			t.Fatalf("torn directory state: %v", names)
		}
	}
	wg.Wait()
}

func TestMagicLinkFdTable(t *testing.T) {
	fs := vfs.New()
	secret, _ := fs.Mkfile(fs.HostRoot, "secret", []byte("s"), 0o600, 0, 0)
	table := vfs.NewFdTable()
	fd := table.Install(secret)
	if table.Lookup(fd) != secret {
		t.Fatal("fd lookup failed")
	}
	proc, _ := fs.ProcTree(fs.HostRoot, 7, table)
	if _, err := fs.FdLinkAt(proc, fd, table); err != nil {
		t.Fatal(err)
	}
	link, err := proc.Child("3")
	if err != nil {
		t.Fatal(err)
	}
	if link.Kind() != vfs.KindMagicLink {
		t.Fatal("proc fd entry must be a magic link")
	}
	if got := link.Magic().Resolve(); got != "/secret" {
		t.Fatalf("magic target = %q, want /secret", got)
	}
	table.Close(fd)
	if table.Lookup(fd) != nil {
		t.Fatal("closed fd still resolves")
	}
}

func TestComponentsPreservesDotDot(t *testing.T) {
	parts := vfs.Components("/a/./b/../c//")
	want := []string{"a", "b", "..", "c"}
	if len(parts) != len(want) {
		t.Fatalf("parts = %v", parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("parts = %v, want %v", parts, want)
		}
	}
}
