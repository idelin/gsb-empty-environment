package resolve

import (
	"strings"

	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// PrefixResolver is a deliberately vulnerable reference implementation.
//
// It models host-side code that validates a requested container path only with
// a string prefix check before walking it, then follows ordinary host
// semantics: absolute symlinks anchor at the host root, ".." can walk above
// the container root, magic links resolve to host paths, and no final
// containment verification is performed. The adversarial searcher uses it as
// the control group: every escape found here must be refused by
// [SecureResolver].
//
// Never use this resolver in production code.
type PrefixResolver struct{ *Resolver }

// NewPrefix constructs the vulnerable control resolver.
func NewPrefix(fs *vfs.VFS, ns *mount.Namespace) *PrefixResolver {
	return &PrefixResolver{NewResolver(fs, ns)}
}

func (r *PrefixResolver) hostPrefix() string {
	base := vfs.PathOf(r.Root)
	if base == "/" {
		return ""
	}
	return base
}

// StringCheckOnly reproduces the classic flawed validation: true whenever the
// request string starts with the container path. It does not resolve "..",
// symlinks or magic links.
func (r *PrefixResolver) StringCheckOnly(containerPath string) bool {
	requested := r.hostPrefix() + containerPath
	return strings.HasPrefix(requested, r.hostPrefix()+"/")
}

// Open walks with host semantics and only a string prefix check.
func (r *PrefixResolver) Open(containerPath string, flags Flags, cred *userns.Cred) (*Result, error) {
	if !strings.HasPrefix(containerPath, "/") {
		return nil, ErrBadPath
	}
	// The only "security" check: a string prefix comparison before lookup.
	if containerPath != "/" && !r.StringCheckOnly(containerPath) {
		return nil, ErrEscapedRoot
	}
	// The walk begins at the container root, but nothing below re-validates
	// containment: symlinks anchor at the host root and ".." can climb out.
	cur := r.Root
	stack := vfs.Components(containerPath)
	hops := 0

	for {
		if len(stack) == 0 {
			break
		}
		component := stack[0]
		stack = stack[1:]
		if component == ".." {
			if parent := cur.Parent(); parent != nil {
				cur = parent
			}
			continue
		}
		if component == "" || component == "." {
			continue
		}
		child, err := cur.Child(component)
		if err != nil {
			return nil, ErrNotExist
		}
		if child.Kind() == vfs.KindMagicLink {
			// Vulnerable: magic links resolve straight to host paths.
			node := r.hostLookup(child.Magic().Resolve())
			if node == nil {
				return nil, ErrNotExist
			}
			cur = node
			continue
		}
		if child.Kind() == vfs.KindSymlink {
			hops++
			if hops > MaxSymlinkHops {
				return nil, ErrSymlinkLimit
			}
			target := child.Target()
			if strings.HasPrefix(target, "/") {
				// Vulnerable: absolute targets anchor at the HOST root.
				cur = r.FS.HostRoot
			}
			stack = append(vfs.Components(target), stack...)
			continue
		}
		cur = child
	}
	// Vulnerable: no final containment check exists. The initial string
	// prefix comparison was the only gate, and it has already been bypassed.
	return &Result{Node: cur, Root: r.Root, Hops: hops, InRoot: cur == r.Root || vfs.IsAncestor(r.Root, cur)}, nil
}

func (r *PrefixResolver) hostLookup(hostPath string) *vfs.Node {
	cur := r.FS.HostRoot
	for _, component := range vfs.Split(hostPath) {
		if cur.Kind() != vfs.KindDir {
			return nil
		}
		child, err := cur.Child(component)
		if err != nil {
			return nil
		}
		cur = child
	}
	return cur
}

// OpenSlow returns a resumable walk for interleaved-attack testing.
func (r *PrefixResolver) OpenSlow(containerPath string, flags Flags, cred *userns.Cred) *PrefixSlowWalk {
	return &PrefixSlowWalk{
		r:     r,
		cur:   r.Root,
		stack: vfs.Components(containerPath),
	}
}

// PrefixSlowWalk is the vulnerable resumable walk.
type PrefixSlowWalk struct {
	r     *PrefixResolver
	cur   *vfs.Node
	stack []string
	hops  int
	done  bool
	err   error
}

// Step advances one component, resolving one link if encountered.
func (w *PrefixSlowWalk) Step() (*vfs.Node, bool, error) {
	if w.done || w.err != nil {
		return w.cur, w.done, w.err
	}
	if len(w.stack) == 0 {
		w.done = true
		return w.cur, true, nil
	}
	component := w.stack[0]
	w.stack = w.stack[1:]
	if component == ".." {
		if parent := w.cur.Parent(); parent != nil {
			w.cur = parent
		}
	} else if component != "" && component != "." {
		child, err := w.cur.Child(component)
		if err != nil {
			w.err = ErrNotExist
			return nil, false, w.err
		}
		if child.Kind() == vfs.KindMagicLink {
			node := w.r.hostLookup(child.Magic().Resolve())
			if node == nil {
				w.err = ErrNotExist
				return nil, false, w.err
			}
			w.cur = node
		} else if child.Kind() == vfs.KindSymlink {
			w.hops++
			if w.hops > MaxSymlinkHops {
				w.err = ErrSymlinkLimit
				return nil, false, w.err
			}
			target := child.Target()
			if strings.HasPrefix(target, "/") {
				w.cur = w.r.FS.HostRoot
			}
			w.stack = append(vfs.Components(target), w.stack...)
		} else {
			w.cur = child
		}
	}
	if len(w.stack) == 0 {
		w.done = true
	}
	return w.cur, w.done, nil
}

// Finish applies the flawed string-only final check.
func (w *PrefixSlowWalk) Finish() (*Result, error) {
	if w.err != nil {
		return nil, w.err
	}
	return &Result{
		Node:   w.cur,
		Root:   w.r.Root,
		Hops:   w.hops,
		InRoot: w.cur == w.r.Root || vfs.IsAncestor(w.r.Root, w.cur),
	}, nil
}
