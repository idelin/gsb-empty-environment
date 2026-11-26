package vfs

import (
	"path"
	"strconv"
	"sync"
)

// MagicLink is the dynamic target of a procfs-style magic symbolic link.
// Resolve is invoked every time the link is followed; implementations must be
// safe for concurrent use.
type MagicLink interface {
	Resolve() string
}

// FdTable models the per-process file descriptor table referenced by
// /proc/<pid>/fd. Entries are node pointers captured when a descriptor is
// installed, so a magic link can reach a host file even after all ordinary
// directory entries pointing at it have been removed.
type FdTable struct {
	mu    sync.RWMutex
	next  int
	files map[int]*Node
}

// NewFdTable returns an empty descriptor table.
func NewFdTable() *FdTable {
	return &FdTable{next: 3, files: map[int]*Node{}}
}

// Install stores node in the table and returns the allocated descriptor.
func (t *FdTable) Install(node *Node) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	fd := t.next
	t.next++
	t.files[fd] = node
	return fd
}

// DupInstall stores node at a specific descriptor number, replacing any
// previous entry (dup2 semantics).
func (t *FdTable) DupInstall(fd int, node *Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if fd >= t.next {
		t.next = fd + 1
	}
	t.files[fd] = node
}

// Lookup returns the node referenced by fd, or nil if unused.
func (t *FdTable) Lookup(fd int) *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.files[fd]
}

// Close removes a descriptor.
func (t *FdTable) Close(fd int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.files, fd)
}

// FdMagicLink evaluates to "/proc/<pid>/fd/<n>" style host paths, resolving
// through the supplied descriptor table at access time.
type FdMagicLink struct {
	Table *FdTable
	FD    int
}

// Resolve returns the current host-absolute path of the descriptor's node.
func (m *FdMagicLink) Resolve() string {
	node := m.Table.Lookup(m.FD)
	if node == nil {
		return "/dev/null"
	}
	return PathOf(node)
}

// StaticMagic always resolves to a fixed host-absolute path. It models links
// such as /proc/self/exe or /proc/self/root whose target is computed but
// stable for the lifetime of the process.
type StaticMagic struct{ Target string }

// Resolve implements MagicLink.
func (m StaticMagic) Resolve() string { return m.Target }

// ProcTree builds the standard /proc/self/fd hierarchy inside procRoot.
// selfPid controls the numeric pid directory; the "self" entry is a static
// symlink to that directory.
func (v *VFS) ProcTree(procRoot *Node, pid int, table *FdTable) (*Node, error) {
	proc, err := v.Mkdir(procRoot, "proc", 0o555, 0, 0)
	if err != nil {
		return nil, err
	}
	pidDir, err := v.Mkdir(proc, strconv.Itoa(pid), 0o555, 0, 0)
	if err != nil {
		return nil, err
	}
	fdDir, err := v.Mkdir(pidDir, "fd", 0o555, 0, 0)
	if err != nil {
		return nil, err
	}
	if _, err := v.Symlink(proc, "self", strconv.Itoa(pid), 0, 0); err != nil {
		return nil, err
	}
	link := v.MagicLinkDetached("0", &FdMagicLink{Table: table, FD: 0}, 0, 0)
	if err := v.Link(fdDir, "0", link); err != nil {
		return nil, err
	}
	return fdDir, nil
}

// FdLinkAt creates /proc-style fd/<n> magic link entries under fdDir.
func (v *VFS) FdLinkAt(fdDir *Node, fd int, table *FdTable) (*Node, error) {
	name := strconv.Itoa(fd)
	link := v.MagicLinkDetached(name, &FdMagicLink{Table: table, FD: fd}, 0, 0)
	if err := v.Link(fdDir, name, link); err != nil {
		return nil, err
	}
	return link, nil
}

// Split splits a host-absolute path into clean components.
func Split(p string) []string {
	cleaned := path.Clean(p)
	if cleaned == "/" || cleaned == "." {
		return nil
	}
	parts := make([]string, 0)
	for _, part := range splitSlash(cleaned) {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	return parts
}

// Components splits a path into raw components without lexical cleaning.
// Empty segments and "." are dropped, but ".." is preserved so path
// resolvers can implement root clamping and mount-boundary semantics
// themselves instead of trusting a pre-cleaned string.
func Components(p string) []string {
	out := make([]string, 0)
	for _, part := range splitSlash(p) {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

func splitSlash(p string) []string {
	out := make([]string, 0)
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	return out
}
