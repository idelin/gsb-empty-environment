package vfs

import "strings"

// Resolve walks path from the virtual root and returns the inode it
// names, expanding symlinks along the way.
func (fs *FS) Resolve(path string) (*Node, error) {
	return fs.resolve(fs.root, path)
}

// resolve is a component-at-a-time walk in the spirit of Linux namei.
//
// The pending components live on a stack so that a symlink target can
// be spliced into the middle of the walk. Every step re-reads the
// child mapping of the current directory inode under a lock, so a
// concurrent Replace simply changes what the *next* lookup sees; the
// walk itself is pinned to inodes it already holds. No string prefix
// is ever consulted to decide whether a result is "inside" the root —
// that follows from the fact that every step starts from an inode
// already known to be inside, and ".." at the root is rejected.
func (fs *FS) resolve(base *Node, path string) (*Node, error) {
	var stack []string
	push := func(p string) {
		parts := strings.Split(p, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			stack = append(stack, parts[i])
		}
	}

	dir := base
	if strings.HasPrefix(path, "/") {
		dir = fs.root
	}
	push(path)
	node := dir
	hops := 0

	for len(stack) > 0 {
		comp := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch comp {
		case "", ".":
			node = dir
			continue
		case "..":
			// Reject any ".." that would climb past the jail root.
			// Detached nodes (parent == nil) cannot prove they are
			// still inside, so they are rejected too.
			if dir == fs.root || dir.parent == nil {
				return nil, ErrEscapesRoot
			}
			dir = dir.parent
			node = dir
			continue
		}

		if fs.preStep != nil {
			fs.preStep()
		}

		child := fs.lookup(dir, comp)
		if child == nil {
			if dir.kind != Dir {
				return nil, ErrNotDir
			}
			return nil, ErrNotExist
		}

		if child.kind == Symlink {
			hops++
			if hops > MaxSymlinkHops {
				return nil, ErrTooManyLinks
			}
			target := child.target
			if strings.HasPrefix(target, "/") {
				// Absolute targets restart at the jail root.
				dir = fs.root
			}
			// Relative targets resolve against the directory that
			// contains the link, i.e. the current dir inode.
			push(target)
			node = dir
			continue
		}

		dir = child
		node = child
	}
	return node, nil
}
