package adversarial

import (
	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/resolve"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// BuildStandardScenario constructs the canonical small tree used across tests:
//
//	/host_secret (root-only file)
//	/container/
//	  etc/
//	    shadow         (regular file inside the sandbox)
//	  work/
//	    a/             (directory)
//	    b/             (directory)
//	    link           (symlink the attacker can repoint)
//	  proc/self/fd/3   (magic link backed by an fd table)
//
// The container root /container is bind-mounted into a dedicated mount
// namespace; an fd referencing /host_secret is pre-installed so magic-link
// attacks have a concrete target.
func BuildStandardScenario() *Scenario {
	fs := vfs.New()
	hostSecret, _ := fs.Mkfile(fs.HostRoot, "host_secret", []byte("TOP-SECRET"), 0o600, 0, 0)

	container, _ := fs.Mkdir(fs.HostRoot, "container", 0o755, 0, 0)
	etc, _ := fs.Mkdir(container, "etc", 0o755, 0, 0)
	_, _ = fs.Mkfile(etc, "shadow", []byte("container-shadow"), 0o644, 0, 0)
	work, _ := fs.Mkdir(container, "work", 0o777, 0, 0)
	dirA, _ := fs.Mkdir(work, "a", 0o777, 0, 0)
	dirB, _ := fs.Mkdir(work, "b", 0o777, 0, 0)
	harmless, _ := fs.Mkfile(dirA, "data", []byte("a-data"), 0o644, 0, 0)
	_, _ = fs.Mkfile(dirB, "data", []byte("b-data"), 0o644, 0, 0)
	_, _ = fs.Symlink(work, "link", "a/data", 0, 0)

	fdTable := vfs.NewFdTable()
	fd := fdTable.Install(hostSecret)
	fdDir, _ := fs.ProcTree(container, 4242, fdTable)
	_, _ = fs.FdLinkAt(fdDir, fd, fdTable)

	initUserNS := userns.NewInitial()
	cred := &userns.Cred{NS: initUserNS, UID: 0, GID: 0}
	// The container mount namespace root is the container directory itself:
	// "/" inside the container maps to /container on the host, exactly as a
	// pivot_root-based runtime would arrange it.
	containerMNT := mount.NewRootNamespace("container_mnt", container, initUserNS)
	containerMNT.Lock(containerMNT.RootMount())

	return &Scenario{
		FS:            fs,
		NS:            containerMNT,
		ContainerRoot: container,
		HostSecret:    hostSecret,
		Secure:        resolve.NewSecure(fs, containerMNT),
		Vulnerable:    resolve.NewPrefix(fs, containerMNT),
		Cred:          cred,
		Flags:         resolve.DefaultSecureFlags(),
		OpenPaths: []string{
			"/work/link",
			"/work/a/data",
			"/proc/self/fd/" + itoa(fd),
			"/etc/shadow",
		},
		Candidates: standardCandidates(fs, container, work, dirA, dirB, harmless),
	}
}

func standardCandidates(fs *vfs.VFS, container, work, dirA, dirB *vfs.Node, harmless *vfs.Node) []Action {
	return []Action{
		{Kind: ActSymlink, Dir: work, Name: "evil", Target: "../../host_secret"},
		{Kind: ActSymlink, Dir: work, Name: "abs", Target: "/host_secret"},
		{Kind: ActSymlink, Dir: dirA, Name: "up", Target: "../../../../host_secret"},
		{Kind: ActSymlink, Dir: work, Name: "loop1", Target: "loop2"},
		{Kind: ActSymlink, Dir: work, Name: "loop2", Target: "loop1"},
		{Kind: ActRename, SrcDir: work, SrcName: "link", DstDir: work, DstName: "link2"},
		{Kind: ActRename, SrcDir: dirA, SrcName: "data", DstDir: dirB, DstName: "data2"},
		{Kind: ActExchange, SrcDir: work, SrcName: "a", DstDir: work, DstName: "b"},
		{Kind: ActExchange, SrcDir: work, SrcName: "link", DstDir: work, DstName: "evil"},
	}
}

// ExhaustiveMicroTree builds a deliberately tiny tree for full enumeration
// checks: container root with one directory d, one file f, one symlink l and a
// sibling host secret. Candidate actions cover every legal mutation so the
// test can assert that no six-step sequence escapes the secure resolver.
func ExhaustiveMicroTree() *Scenario {
	fs := vfs.New()
	secret, _ := fs.Mkfile(fs.HostRoot, "secret", []byte("secret"), 0o600, 0, 0)
	root, _ := fs.Mkdir(fs.HostRoot, "root", 0o755, 0, 0)
	dir, _ := fs.Mkdir(root, "d", 0o777, 0, 0)
	file, _ := fs.Mkfile(root, "f", []byte("in-root"), 0o644, 0, 0)
	_, _ = fs.Symlink(root, "l", "f", 0, 0)

	userNS := userns.NewInitial()
	ns := mount.NewRootNamespace("micro_mnt", root, userNS)
	cred := &userns.Cred{NS: userNS, UID: 0, GID: 0}
	ns.Lock(ns.RootMount())

	// A procfs magic link backed by an fd that references the host secret.
	fdTable := vfs.NewFdTable()
	fd := fdTable.Install(secret)
	fdDir, err := fs.ProcTree(root, 9, fdTable)
	if err != nil {
		panic(err)
	}
	if _, err := fs.FdLinkAt(fdDir, fd, fdTable); err != nil {
		panic(err)
	}

	sc := &Scenario{
		FS:            fs,
		NS:            ns,
		ContainerRoot: root,
		HostSecret:    secret,
		Secure:        resolve.NewSecure(fs, ns),
		Vulnerable:    resolve.NewPrefix(fs, ns),
		Cred:          cred,
		Flags:         resolve.DefaultSecureFlags(),
		OpenPaths:     []string{"/l", "/f", "/d", "/d/../l", "/proc/self/fd/" + itoa(fd)},
	}
	sc.Candidates = []Action{
		{Kind: ActSymlink, Dir: root, Name: "s1", Target: "../secret"},
		{Kind: ActSymlink, Dir: root, Name: "s2", Target: "/secret"},
		{Kind: ActSymlink, Dir: dir, Name: "s3", Target: "../../secret"},
		{Kind: ActRename, SrcDir: root, SrcName: "l", DstDir: root, DstName: "lmoved"},
		{Kind: ActRename, SrcDir: root, SrcName: "f", DstDir: dir, DstName: "fmoved"},
		{Kind: ActExchange, SrcDir: root, SrcName: "d", DstDir: root, DstName: "l"},
	}
	_ = file
	return sc
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
