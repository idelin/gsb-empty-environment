package resolve_test

import (
	"errors"
	"testing"

	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/resolve"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

type lab struct {
	fs      *vfs.VFS
	ns      *mount.Namespace
	root    *vfs.Node
	secret  *vfs.Node
	secure  *resolve.SecureResolver
	prefix  *resolve.PrefixResolver
	fdTable *vfs.FdTable
}

func newLab(t *testing.T) *lab {
	t.Helper()
	fs := vfs.New()
	secret, err := fs.Mkfile(fs.HostRoot, "host_secret", []byte("secret"), 0o600, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := fs.Mkdir(fs.HostRoot, "container", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	etc, err := fs.Mkdir(root, "etc", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkfile(etc, "shadow", []byte("s"), 0o644, 0, 0); err != nil {
		t.Fatal(err)
	}
	work, err := fs.Mkdir(root, "work", 0o777, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := fs.Mkdir(work, "a", 0o777, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.Mkdir(work, "b", 0o777, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkfile(a, "data", []byte("a"), 0o644, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkfile(b, "data", []byte("b"), 0o644, 0, 0); err != nil {
		t.Fatal(err)
	}
	userNS := userns.NewInitial()
	ns := mount.NewRootNamespace("mnt", root, userNS)
	ns.Lock(ns.RootMount())

	fdTable := vfs.NewFdTable()
	fdDir, err := fs.ProcTree(root, 1, fdTable)
	if err != nil {
		t.Fatal(err)
	}
	fd := fdTable.Install(secret)
	if _, err := fs.FdLinkAt(fdDir, fd, fdTable); err != nil {
		t.Fatal(err)
	}

	return &lab{
		fs:      fs,
		ns:      ns,
		root:    root,
		secret:  secret,
		secure:  resolve.NewSecure(fs, ns),
		prefix:  resolve.NewPrefix(fs, ns),
		fdTable: fdTable,
	}
}

func TestSecureNormalWalk(t *testing.T) {
	l := newLab(t)
	res, err := l.secure.Open("/etc/shadow", resolve.DefaultSecureFlags(), nil)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if string(res.Node.Data()) != "s" {
		t.Fatalf("wrong file contents")
	}
	if !res.InRoot {
		t.Fatal("expected InRoot")
	}
}

func TestRootClampOnDotDot(t *testing.T) {
	l := newLab(t)
	cases := []string{
		"/../../../../etc/shadow",
		"/work/../../../etc/shadow",
		"/work/a/../../etc/shadow",
	}
	for _, p := range cases {
		res, err := l.secure.Open(p, resolve.DefaultSecureFlags(), nil)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", p, err)
		}
		if !vfs.IsAncestor(l.root, res.Node) && res.Node != l.root {
			t.Fatalf("%s: escaped to %s", p, vfs.PathOf(res.Node))
		}
	}

	// The vulnerable resolver leaks above the container for the same input.
	res, err := l.prefix.Open("/work/../../../host_secret", resolve.Flags{}, nil)
	if err != nil {
		t.Fatalf("prefix resolver should have walked to host: %v", err)
	}
	if res.Node != l.secret {
		t.Fatalf("control group failed, got %s", vfs.PathOf(res.Node))
	}
}

func TestAbsoluteSymlinkReanchored(t *testing.T) {
	l := newLab(t)
	work, _ := l.root.Child("work")
	if _, err := l.fs.Symlink(work, "abs", "/host_secret", 0, 0); err != nil {
		t.Fatal(err)
	}
	_, err := l.secure.Open("/work/abs", resolve.DefaultSecureFlags(), nil)
	if !errors.Is(err, resolve.ErrNotExist) {
		t.Fatalf("expected container-anchored ENOENT, got %v", err)
	}

	// An absolute link to a path that exists inside the container must resolve.
	if _, err := l.fs.Symlink(work, "etc-link", "/etc/shadow", 0, 0); err != nil {
		t.Fatal(err)
	}
	res, err := l.secure.Open("/work/etc-link", resolve.DefaultSecureFlags(), nil)
	if err != nil {
		t.Fatalf("container-anchored absolute symlink failed: %v", err)
	}
	if string(res.Node.Data()) != "s" {
		t.Fatal("wrong target")
	}

	// Control group: the prefix resolver anchors at host root and escapes.
	res, err = l.prefix.Open("/work/abs", resolve.Flags{}, nil)
	if err != nil || res.Node != l.secret {
		t.Fatalf("prefix control failed: res=%v err=%v", res, err)
	}
}

func TestRelativeSymlinkEscape(t *testing.T) {
	l := newLab(t)
	work, _ := l.root.Child("work")
	if _, err := l.fs.Symlink(work, "escape", "../../host_secret", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Target host_secret is outside any container mount: final containment
	// must reject it even though ".." walks are clamped mid-path.
	_, err := l.secure.Open("/work/escape", resolve.DefaultSecureFlags(), nil)
	if !errors.Is(err, resolve.ErrEscapedRoot) && !errors.Is(err, resolve.ErrNotExist) {
		t.Fatalf("expected containment/ENOENT error, got %v", err)
	}

	res, err := l.prefix.Open("/work/escape", resolve.Flags{}, nil)
	if err != nil || res.Node != l.secret {
		t.Fatalf("prefix control failed: res=%v err=%v", res, err)
	}
}

func TestMagicLinkRefused(t *testing.T) {
	l := newLab(t)
	fd := l.fdTable.Lookup(3)
	_ = fd
	_, err := l.secure.Open("/proc/self/fd/3", resolve.DefaultSecureFlags(), nil)
	if !errors.Is(err, resolve.ErrMagicLink) {
		t.Fatalf("expected ErrMagicLink, got %v", err)
	}

	res, err := l.prefix.Open("/proc/self/fd/3", resolve.Flags{}, nil)
	if err != nil || res.Node != l.secret {
		t.Fatalf("prefix control should follow magic link, got res=%v err=%v", res, err)
	}
}

func TestSymlinkLoopLimit(t *testing.T) {
	l := newLab(t)
	work, _ := l.root.Child("work")
	if _, err := l.fs.Symlink(work, "x", "y", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.fs.Symlink(work, "y", "x", 0, 0); err != nil {
		t.Fatal(err)
	}
	_, err := l.secure.Open("/work/x", resolve.DefaultSecureFlags(), nil)
	if !errors.Is(err, resolve.ErrSymlinkLimit) {
		t.Fatalf("expected ErrSymlinkLimit, got %v", err)
	}
}

func TestRenameExchangeTOCTOUSlowWalk(t *testing.T) {
	l := newLab(t)
	work, _ := l.root.Child("work")

	// Attacker swaps directories a and b between victim steps.
	walk := l.secure.OpenSlow("/work/a/data", resolve.DefaultSecureFlags(), nil)
	node, done, err := walk.Step() // consume "work"
	if err != nil || done {
		t.Fatalf("step1 failed: node=%v done=%v err=%v", node, done, err)
	}
	if err := l.fs.Exchange(work, "a", work, "b"); err != nil {
		t.Fatal(err)
	}
	for {
		node, done, err = walk.Step()
		if err != nil {
			t.Fatalf("secure walk broke under exchange: %v", err)
		}
		if done {
			break
		}
	}
	res, err := walk.Finish(l.secure)
	if err != nil {
		t.Fatalf("secure slow open failed after exchange: %v", err)
	}
	if !res.InRoot || (!vfs.IsAncestor(l.root, res.Node) && res.Node != l.root) {
		t.Fatalf("escape via rename race: %s", vfs.PathOf(res.Node))
	}

	// Symlink race: benign link replaced by an absolute host link mid-walk.
	if _, err := l.fs.Symlink(work, "race", "a/data", 0, 0); err != nil {
		t.Fatal(err)
	}
	walk2 := l.secure.OpenSlow("/work/race", resolve.DefaultSecureFlags(), nil)
	if _, done, _ := walk2.Step(); done {
		t.Fatal("unexpected early completion")
	}
	if err := l.fs.Remove(work, "race"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.fs.Symlink(work, "race", "/host_secret", 0, 0); err != nil {
		t.Fatal(err)
	}
	for {
		_, done, err := walk2.Step()
		if err != nil {
			break
		}
		if done {
			break
		}
	}
	if _, err := walk2.Finish(l.secure); err == nil {
		t.Fatal("expected secure resolver to reject the swapped absolute link")
	}
}

func TestConcurrentRenameNoDataRace(t *testing.T) {
	l := newLab(t)
	work, _ := l.root.Child("work")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = l.fs.Exchange(work, "a", work, "b")
			_ = l.fs.Exchange(work, "a", work, "b")
		}
	}()
	for i := 0; i < 200; i++ {
		res, err := l.secure.Open("/work/a/data", resolve.DefaultSecureFlags(), nil)
		if err != nil {
			t.Fatalf("concurrent open error: %v", err)
		}
		if !res.InRoot {
			t.Fatal("escape detected under concurrent renames")
		}
	}
	<-done
}

func TestReadOnlyAndMountBoundary(t *testing.T) {
	l := newLab(t)
	etc, _ := l.root.Child("etc")
	userNS := l.ns.UserNS
	cred := &userns.Cred{NS: userNS, UID: 0, GID: 0}

	// Stack a read-only bind mount on /etc.
	roMount, err := l.ns.BindMount(etc, etc, mount.BindOptions{ReadOnly: true, Locked: true}, cred)
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.secure.Open("/etc/shadow", resolve.DefaultSecureFlags(), nil)
	if err != nil {
		t.Fatalf("read-only bind should still allow reads: %v", err)
	}
	if res.Mount != roMount {
		t.Fatal("resolver did not switch onto stacked bind mount")
	}

	// ".." from inside the bind mount must remain inside the container.
	res, err = l.secure.Open("/etc/../etc/shadow", resolve.DefaultSecureFlags(), nil)
	if err != nil || !res.InRoot {
		t.Fatalf("dotdot over mount boundary escaped: %v", err)
	}
}
