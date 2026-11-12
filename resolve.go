package netsec

import (
	"errors"
	"strings"
)

// MaxSymlinkFollows bounds how many symbolic links a single resolution
// may follow, mirroring Linux MAXSYMLINKS. Exceeding it reports ErrLoop.
const MaxSymlinkFollows = 40

var (
	// ErrInvalidName is returned when a walk name is not a single path
	// component: it is empty, ".", "..", or contains '/' or NUL.
	ErrInvalidName = errors.New("netsec: invalid walk name")
	// ErrLoop is returned when resolution follows more than
	// MaxSymlinkFollows links, or when open with O_NOFOLLOW finds a
	// symbolic link in the final component (Linux reports ELOOP for both).
	ErrLoop = errors.New("netsec: too many levels of symbolic links")
	// ErrNotDir is returned when an intermediate component is not a directory.
	ErrNotDir = errors.New("netsec: not a directory")
	// ErrNotFound is returned when a component does not exist.
	ErrNotFound = errors.New("netsec: file not found")
)

// Node is a handle to a file in the backing filesystem. The kernel only
// ever passes handles to the gofer, never raw paths.
type Node interface {
	IsDir() bool
	IsSymlink() bool
}

// FS is the backing filesystem served by the gofer. All operations are
// handle based so the host never resolves container-controlled paths.
type FS interface {
	// Root returns the root node of the mount.
	Root() Node
	// Lookup returns the child of dir called name. name is guaranteed to
	// satisfy CheckName. It must not follow symbolic links.
	Lookup(dir Node, name string) (Node, error)
	// Readlink returns the target of symbolic link n.
	Readlink(n Node) (string, error)
}

// CheckName reports whether name is a valid single walk component.
func CheckName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, "/\x00") {
		return ErrInvalidName
	}
	return nil
}

// Resolver walks paths on behalf of the kernel, starting every
// resolution at the mount root.
type Resolver struct {
	FS FS
}

// Walk resolves each name in turn from the mount root, following
// symbolic links including one in the final component, and returns the
// resulting node. Every name must satisfy CheckName.
func (r *Resolver) Walk(names ...string) (Node, error) {
	return r.resolve(names, true)
}

// Open resolves names like Walk but never lets the host follow the final
// component: the returned node must be opened with O_NOFOLLOW on the
// host. If nofollow is set (the request carried O_NOFOLLOW) and the
// final component is a symbolic link, Open returns ErrLoop; otherwise
// the kernel follows the final link itself, subject to MaxSymlinkFollows.
func (r *Resolver) Open(nofollow bool, names ...string) (Node, error) {
	return r.resolve(names, !nofollow)
}

// resolve walks names starting at the mount root. Symbolic links are
// expanded by the kernel: absolute targets restart at the mount root,
// relative targets continue from the link's parent, and ".." is clamped
// at the root so it can never escape the mount. followFinal controls
// whether a symbolic link in the final component is followed.
func (r *Resolver) resolve(names []string, followFinal bool) (Node, error) {
	for _, name := range names {
		if err := CheckName(name); err != nil {
			return nil, err
		}
	}
	root := r.FS.Root()
	cur := root
	// ancestors holds the path of directories from the root down to the
	// parent of cur, so ".." can be resolved without host assistance.
	var ancestors []Node
	follows := 0
	// stack of pending components; the last element is processed next.
	stack := make([]string, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- {
		stack = append(stack, names[i])
	}
	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		last := len(stack) == 0
		switch name {
		case "", ".":
			// Only reachable from symlink targets; walk names are
			// validated above. Skip like Linux does.
			continue
		case "..":
			// Only reachable from symlink targets. Clamp at the root.
			if n := len(ancestors); n > 0 {
				cur = ancestors[n-1]
				ancestors = ancestors[:n-1]
			}
			continue
		}
		next, err := r.FS.Lookup(cur, name)
		if err != nil {
			return nil, err
		}
		if next.IsSymlink() {
			if last && !followFinal {
				return nil, ErrLoop
			}
			follows++
			if follows > MaxSymlinkFollows {
				return nil, ErrLoop
			}
			target, err := r.FS.Readlink(next)
			if err != nil {
				return nil, err
			}
			if target == "" {
				return nil, ErrNotFound
			}
			if target[0] == '/' {
				cur = root
				ancestors = ancestors[:0]
			}
			pushTarget(&stack, target)
			continue
		}
		if !last {
			if !next.IsDir() {
				return nil, ErrNotDir
			}
			ancestors = append(ancestors, cur)
		}
		cur = next
	}
	return cur, nil
}

// pushTarget splits a symlink target into components and pushes them onto
// the pending stack so they are processed before the remaining names.
func pushTarget(stack *[]string, target string) {
	comps := strings.Split(target, "/")
	for i := len(comps) - 1; i >= 0; i-- {
		*stack = append(*stack, comps[i])
	}
}
