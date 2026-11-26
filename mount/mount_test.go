package mount_test

import (
	"errors"
	"testing"

	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

func buildTree(t *testing.T) (*vfs.VFS, *vfs.Node, *vfs.Node) {
	t.Helper()
	fs := vfs.New()
	root, err := fs.Mkdir(fs.HostRoot, "root", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	mnt, err := fs.Mkdir(root, "mnt", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	src, err := fs.Mkdir(fs.HostRoot, "src", 0o755, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fs, mnt, src
}

func TestLockedMountUnmount(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	hostMNT := mount.NewRootNamespace("host", fs.HostRoot, initNS)
	rootCred := &userns.Cred{NS: initNS, UID: 0, GID: 0}

	childUserNS := initNS.Child("child",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		"CAP_SYS_ADMIN")
	childCred := &userns.Cred{NS: childUserNS, UID: 0, GID: 0}

	m, err := hostMNT.BindMount(src, point, mount.BindOptions{
		Locked:      true,
		Propagation: mount.PropPrivate,
	}, rootCred)
	if err != nil {
		t.Fatal(err)
	}

	childMNT := hostMNT.Clone("child_mnt", childUserNS)
	childMount := childMNT.TopMountAt(point)
	if childMount == nil {
		t.Fatal("cloned namespace lost the bind mount")
	}

	// Non-initial user namespace cannot unmount a locked mount.
	if err := childMNT.Unmount(childMount, childCred); !errors.Is(err, mount.ErrLockedMount) {
		t.Fatalf("expected ErrLockedMount, got %v", err)
	}
	// It also cannot strip read-only attributes once locked.
	if err := childMNT.SetReadOnly(childMount, false, childCred); !errors.Is(err, mount.ErrLockedMount) {
		t.Fatalf("expected locked attr error, got %v", err)
	}
	// The initial user namespace retains full control.
	if err := hostMNT.Unmount(m, rootCred); err != nil {
		t.Fatalf("initial namespace unmount failed: %v", err)
	}
}

func TestReadOnlyBindFlagLocked(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	hostMNT := mount.NewRootNamespace("host", fs.HostRoot, initNS)
	rootCred := &userns.Cred{NS: initNS, UID: 0, GID: 0}

	childUserNS := initNS.Child("child",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		"CAP_SYS_ADMIN")
	childCred := &userns.Cred{NS: childUserNS, UID: 0, GID: 0}

	m, err := hostMNT.BindMount(src, point, mount.BindOptions{ReadOnly: true}, rootCred)
	if err != nil {
		t.Fatal(err)
	}
	childMNT := hostMNT.Clone("child_mnt", childUserNS)
	childMount := childMNT.TopMountAt(point)

	if err := childMNT.SetReadOnly(childMount, false, childCred); !errors.Is(err, mount.ErrReadOnlyBind) {
		t.Fatalf("expected ErrReadOnlyBind, got %v", err)
	}
	if !m.IsReadOnly() {
		t.Fatal("read-only state must remain intact after rejected remount")
	}
	// Making it more restrictive is allowed.
	if err := childMNT.SetReadOnly(childMount, true, childCred); err != nil {
		t.Fatalf("applying MS_RDONLY should succeed: %v", err)
	}
}

func TestCapabilityGate(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	ns := mount.NewRootNamespace("host", fs.HostRoot, initNS)

	weakNS := initNS.Child("weak",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}})
	weakCred := &userns.Cred{NS: weakNS, UID: 0, GID: 0}

	if _, err := ns.BindMount(src, point, mount.BindOptions{}, weakCred); !errors.Is(err, mount.ErrNoCapability) {
		t.Fatalf("expected ErrNoCapability, got %v", err)
	}
	// Credentials from another user namespace are rejected even with caps.
	other := initNS.Child("other",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 2000, Count: 1}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 2000, Count: 1}}},
		"CAP_SYS_ADMIN")
	if _, err := ns.BindMount(src, point, mount.BindOptions{}, &userns.Cred{NS: other}); !errors.Is(err, mount.ErrNoCapability) {
		t.Fatalf("cross-namespace cred must fail, got %v", err)
	}
}

func TestSharedPropagationBidirectional(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	parent := mount.NewRootNamespace("parent", fs.HostRoot, initNS)
	cred := &userns.Cred{NS: initNS, UID: 0, GID: 0}

	shared, err := parent.BindMount(src, point, mount.BindOptions{Propagation: mount.PropShared}, cred)
	if err != nil {
		t.Fatal(err)
	}
	child := parent.Clone("child", initNS)

	// Peer-group membership survives cloning.
	childMount := child.TopMountAt(point)
	if childMount.PeerGroupOf() == nil || childMount.PeerGroupOf() != shared.PeerGroupOf() {
		t.Fatal("cloned shared mount lost peer group")
	}

	// An event in the parent propagates down to the child namespace.
	vol, _ := fs.Mkdir(fs.HostRoot, "vol", 0o755, 0, 0)
	sub, _ := fs.Mkdir(point, "sub", 0o755, 0, 0)
	if _, err := parent.BindMount(vol, sub, mount.BindOptions{Propagation: mount.PropShared}, cred); err != nil {
		t.Fatal(err)
	}
	events := child.Events()
	found := false
	for _, ev := range events {
		if ev.Kind == mount.EventMount && ev.OriginNS == parent {
			found = true
		}
	}
	if !found {
		t.Fatal("MS_SHARED event did not propagate to child")
	}
}

func TestSlavePropagationOneWay(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	parent := mount.NewRootNamespace("parent", fs.HostRoot, initNS)
	cred := &userns.Cred{NS: initNS, UID: 0, GID: 0}

	if _, err := parent.BindMount(src, point, mount.BindOptions{Propagation: mount.PropShared}, cred); err != nil {
		t.Fatal(err)
	}
	child := parent.Clone("child", initNS)
	childMount := child.TopMountAt(point)
	if err := child.SetPropagation(childMount, mount.PropSlave, cred); err != nil {
		t.Fatal(err)
	}

	// Parent -> child delivery still works.
	vol, _ := fs.Mkdir(fs.HostRoot, "vol2", 0o755, 0, 0)
	sub, _ := fs.Mkdir(point, "sub2", 0o755, 0, 0)
	if _, err := parent.BindMount(vol, sub, mount.BindOptions{Propagation: mount.PropShared}, cred); err != nil {
		t.Fatal(err)
	}
	if len(child.Events()) == 0 {
		t.Fatal("slave must still receive master events")
	}

	// Child -> parent delivery must never happen.
	before := len(parent.Events())
	otherVol, _ := fs.Mkdir(fs.HostRoot, "vol3", 0o755, 0, 0)
	otherSub, _ := fs.Mkdir(point, "sub3", 0o755, 0, 0)
	if _, err := child.BindMount(otherVol, otherSub, mount.BindOptions{Propagation: mount.PropSlave}, cred); err != nil {
		t.Fatal(err)
	}
	if len(parent.Events()) != before {
		t.Fatal("slave event leaked back to the master namespace")
	}
}

func TestPrivateMountIsolation(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	parent := mount.NewRootNamespace("parent", fs.HostRoot, initNS)
	cred := &userns.Cred{NS: initNS, UID: 0, GID: 0}
	if _, err := parent.BindMount(src, point, mount.BindOptions{Propagation: mount.PropPrivate}, cred); err != nil {
		t.Fatal(err)
	}
	child := parent.Clone("child", initNS)
	vol, _ := fs.Mkdir(fs.HostRoot, "volp", 0o755, 0, 0)
	sub, _ := fs.Mkdir(point, "subp", 0o755, 0, 0)
	if _, err := parent.BindMount(vol, sub, mount.BindOptions{Propagation: mount.PropPrivate}, cred); err != nil {
		t.Fatal(err)
	}
	if len(child.Events()) != 0 {
		t.Fatal("MS_PRIVATE mount must not propagate any event")
	}
}

func TestMountTreeReachability(t *testing.T) {
	fs, point, src := buildTree(t)
	initNS := userns.NewInitial()
	ns := mount.NewRootNamespace("ns", fs.HostRoot, initNS)
	cred := &userns.Cred{NS: initNS, UID: 0, GID: 0}

	inside, _ := fs.Mkfile(src, "inside", []byte("x"), 0o644, 0, 0)
	m, err := ns.BindMount(src, point, mount.BindOptions{}, cred)
	if err != nil {
		t.Fatal(err)
	}
	if !ns.Reachable(inside) {
		t.Fatal("bind source content must be reachable")
	}
	if !ns.Reachable(m.Root) {
		t.Fatal("mount root must be reachable")
	}
	// After unmount, the source subtree is no longer exposed at that point.
	if err := ns.Unmount(m, cred); err != nil {
		t.Fatal(err)
	}
	if ns.TopMountAt(point) != nil {
		t.Fatal("mount still present after unmount")
	}
}
