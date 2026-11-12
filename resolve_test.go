package netsec_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	netsec "example.org/netsec/q0489"
)

// memNode is an in-memory file used to stand in for the gofer's backing
// filesystem in tests.
type memNode struct {
	dir    bool
	link   bool
	target string
	data   string
	kids   map[string]*memNode
}

func (n *memNode) IsDir() bool     { return n.dir }
func (n *memNode) IsSymlink() bool { return n.link }

type memFS struct {
	root *memNode
}

func newMemFS() *memFS {
	return &memFS{root: &memNode{dir: true, kids: map[string]*memNode{}}}
}

func (f *memFS) Root() netsec.Node { return f.root }

func (f *memFS) Lookup(dir netsec.Node, name string) (netsec.Node, error) {
	d := dir.(*memNode)
	if !d.dir {
		return nil, netsec.ErrNotDir
	}
	child, ok := d.kids[name]
	if !ok {
		return nil, netsec.ErrNotFound
	}
	return child, nil
}

func (f *memFS) Readlink(n netsec.Node) (string, error) {
	l := n.(*memNode)
	if !l.link {
		return "", errors.New("not a symlink")
	}
	return l.target, nil
}

// add inserts path into the filesystem. A content of the form "->target"
// creates a symbolic link; anything else creates a regular file. Parent
// directories are created implicitly.
func (f *memFS) add(t *testing.T, path, content string) {
	t.Helper()
	comps := strings.Split(strings.TrimPrefix(path, "/"), "/")
	cur := f.root
	for i, comp := range comps {
		if i == len(comps)-1 {
			if strings.HasPrefix(content, "->") {
				cur.kids[comp] = &memNode{link: true, target: strings.TrimPrefix(content, "->")}
			} else {
				cur.kids[comp] = &memNode{data: content}
			}
			return
		}
		next, ok := cur.kids[comp]
		if !ok {
			next = &memNode{dir: true, kids: map[string]*memNode{}}
			cur.kids[comp] = next
		}
		cur = next
	}
}

func dataOf(t *testing.T, node netsec.Node, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	return node.(*memNode).data
}

func walkData(t *testing.T, r *netsec.Resolver, names ...string) string {
	t.Helper()
	node, err := r.Walk(names...)
	return dataOf(t, node, err)
}

func openData(t *testing.T, r *netsec.Resolver, nofollow bool, names ...string) string {
	t.Helper()
	node, err := r.Open(nofollow, names...)
	return dataOf(t, node, err)
}

func TestCheckName(t *testing.T) {
	bad := []string{"", ".", "..", "/", "a/b", "a/", "/a", "a\x00b", "\x00"}
	for _, name := range bad {
		if err := netsec.CheckName(name); !errors.Is(err, netsec.ErrInvalidName) {
			t.Errorf("CheckName(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	good := []string{"a", "...", "..x", "x..", "a b", "-"}
	for _, name := range good {
		if err := netsec.CheckName(name); err != nil {
			t.Errorf("CheckName(%q) = %v, want nil", name, err)
		}
	}
}

func TestWalkRejectsInvalidNames(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/dir/file", "data")
	r := &netsec.Resolver{FS: fs}
	bad := []string{"", ".", "..", "a/b", "x\x00y"}
	for _, name := range bad {
		if _, err := r.Walk("dir", name); !errors.Is(err, netsec.ErrInvalidName) {
			t.Errorf("Walk(dir, %q) err = %v, want ErrInvalidName", name, err)
		}
		if _, err := r.Open(false, name); !errors.Is(err, netsec.ErrInvalidName) {
			t.Errorf("Open(%q) err = %v, want ErrInvalidName", name, err)
		}
	}
	// A ".." walk name must never reach the backing filesystem, even when
	// it would stay inside the mount.
	if _, err := r.Walk("dir", "..", "dir", "file"); !errors.Is(err, netsec.ErrInvalidName) {
		t.Errorf("Walk with internal .. err = %v, want ErrInvalidName", err)
	}
}

func TestRelativeLinkCannotEscapeRoot(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/etc/passwd", "sandbox-secret")
	fs.add(t, "/a/b/c/evil", "->../../../../../../etc/passwd")
	r := &netsec.Resolver{FS: fs}
	// The link climbs past the mount root; ".." must clamp at the root so
	// the target resolves to the sandbox's own /etc/passwd.
	if got := walkData(t, r, "a", "b", "c", "evil"); got != "sandbox-secret" {
		t.Errorf("escaped link resolved to %q, want sandbox passwd", got)
	}
}

func TestDotDotInsideRoot(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/x/keep", "kept")
	fs.add(t, "/a/b/hop", "->../..//x/./keep")
	r := &netsec.Resolver{FS: fs}
	if got := walkData(t, r, "a", "b", "hop"); got != "kept" {
		t.Errorf("relative link resolved to %q, want %q", got, "kept")
	}
}

func TestSymlinkChainLimit(t *testing.T) {
	build := func(n int) *netsec.Resolver {
		fs := newMemFS()
		fs.add(t, "/real", "payload")
		for i := n - 1; i >= 0; i-- {
			next := "real"
			if i+1 < n {
				next = fmt.Sprintf("l%d", i+1)
			}
			fs.add(t, fmt.Sprintf("/l%d", i), "->"+next)
		}
		return &netsec.Resolver{FS: fs}
	}
	// A chain of exactly MaxSymlinkFollows links resolves.
	if got := walkData(t, build(netsec.MaxSymlinkFollows), "l0"); got != "payload" {
		t.Errorf("40-link chain resolved to %q, want payload", got)
	}
	// One link more must fail with ErrLoop.
	if _, err := build(netsec.MaxSymlinkFollows + 1).Walk("l0"); !errors.Is(err, netsec.ErrLoop) {
		t.Errorf("41-link chain err = %v, want ErrLoop", err)
	}
}

func TestSymlinkLoop(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/ping", "->pong")
	fs.add(t, "/pong", "->ping")
	fs.add(t, "/self", "->self")
	r := &netsec.Resolver{FS: fs}
	for _, name := range []string{"ping", "self"} {
		if _, err := r.Walk(name); !errors.Is(err, netsec.ErrLoop) {
			t.Errorf("Walk(%q) err = %v, want ErrLoop", name, err)
		}
	}
}

func TestAbsoluteLinkRestartsAtMountRoot(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/deep/nested/dir/jump", "->/etc/motd")
	fs.add(t, "/etc/motd", "hello-sandbox")
	r := &netsec.Resolver{FS: fs}
	if got := walkData(t, r, "deep", "nested", "dir", "jump"); got != "hello-sandbox" {
		t.Errorf("absolute link resolved to %q, want sandbox /etc/motd", got)
	}
	// Absolute links also reset ".." accounting: after restarting at the
	// root, climbing stays inside the mount.
	fs.add(t, "/deep/nested/dir/jump2", "->/../etc/motd")
	if got := walkData(t, r, "deep", "nested", "dir", "jump2"); got != "hello-sandbox" {
		t.Errorf("absolute link with .. resolved to %q, want sandbox /etc/motd", got)
	}
}

func TestIntermediateDirLink(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/real/sub/file", "inner")
	fs.add(t, "/alias", "->real")
	r := &netsec.Resolver{FS: fs}
	if got := walkData(t, r, "alias", "sub", "file"); got != "inner" {
		t.Errorf("walk through dir link got %q, want inner", got)
	}
}

func TestOpenNoFollow(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/target", "data")
	fs.add(t, "/link", "->target")
	r := &netsec.Resolver{FS: fs}

	// Without O_NOFOLLOW the kernel follows the final link itself.
	if got := openData(t, r, false, "link"); got != "data" {
		t.Errorf("Open without nofollow got %q, want data", got)
	}
	// With O_NOFOLLOW a final symlink is an error (ELOOP on Linux).
	if _, err := r.Open(true, "link"); !errors.Is(err, netsec.ErrLoop) {
		t.Errorf("Open with nofollow on link err = %v, want ErrLoop", err)
	}
	// O_NOFOLLOW on a regular file succeeds.
	if got := openData(t, r, true, "target"); got != "data" {
		t.Errorf("Open with nofollow on file got %q, want data", got)
	}
	// Walk always follows the final link.
	if got := walkData(t, r, "link"); got != "data" {
		t.Errorf("Walk on link got %q, want data", got)
	}
	// Intermediate links are still followed under O_NOFOLLOW.
	fs.add(t, "/dirlink", "->sub")
	fs.add(t, "/sub/leaf", "leaf-data")
	if got := openData(t, r, true, "dirlink", "leaf"); got != "leaf-data" {
		t.Errorf("Open nofollow through dir link got %q, want leaf-data", got)
	}
}

func TestDeepPath(t *testing.T) {
	fs := newMemFS()
	const depth = 64
	var sb strings.Builder
	names := make([]string, 0, depth+1)
	for i := 0; i < depth; i++ {
		name := fmt.Sprintf("d%02d", i)
		sb.WriteString("/" + name)
		names = append(names, name)
	}
	sb.WriteString("/leaf")
	names = append(names, "leaf")
	fs.add(t, sb.String(), "deep-data")
	r := &netsec.Resolver{FS: fs}
	if got := walkData(t, r, names...); got != "deep-data" {
		t.Errorf("deep walk got %q, want deep-data", got)
	}
}

func TestWalkErrors(t *testing.T) {
	fs := newMemFS()
	fs.add(t, "/file", "data")
	r := &netsec.Resolver{FS: fs}
	if _, err := r.Walk("missing"); !errors.Is(err, netsec.ErrNotFound) {
		t.Errorf("Walk(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := r.Walk("file", "child"); !errors.Is(err, netsec.ErrNotDir) {
		t.Errorf("Walk through file err = %v, want ErrNotDir", err)
	}
	// Walking zero components yields the mount root.
	node, err := r.Walk()
	if err != nil || node != fs.Root() {
		t.Errorf("Walk() = %v, %v, want root", node, err)
	}
}
