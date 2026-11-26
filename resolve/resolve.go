// Package resolve implements container-root path resolution.
//
// The [SecureResolver] reproduces the kernel contract used by
// openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS): component-by-component
// walks, a 40-hop symlink budget, absolute symlinks re-anchored to the
// container root, ".." clamped at the root, magic-link refusal and a final
// containment check against the mount namespace. A deliberately vulnerable
// [PrefixResolver] is provided as the control for the adversarial searcher.
package resolve

import (
	"errors"
	"strings"

	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// MaxSymlinkHops mirrors the kernel MAXSYMLINKS limit.
const MaxSymlinkHops = 40

// Flags configure a resolution.
type Flags struct {
	// NoMagicLinks refuses procfs magic links (RESOLVE_NO_MAGICLINKS).
	NoMagicLinks bool
	// RequireRegular rejects directory targets on final open.
	RequireRegular bool
	// CheckDAC applies credential-based permission checks.
	CheckDAC bool
}

// DefaultSecureFlags returns the hardening set equivalent to
// openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS).
func DefaultSecureFlags() Flags {
	return Flags{NoMagicLinks: true}
}

// Sentinel errors distinguishing rejection classes.
var (
	ErrMagicLink          = errors.New("resolve: magic link crossing refused (RESOLVE_NO_MAGICLINKS)")
	ErrSymlinkLimit       = errors.New("resolve: too many symbolic links (40)")
	ErrNotExist           = errors.New("resolve: no such file or directory")
	ErrNotDir             = errors.New("resolve: not a directory")
	ErrIsDir              = errors.New("resolve: is a directory")
	ErrEscapedRoot        = errors.New("resolve: path escaped container root")
	ErrCrossedRestriction = errors.New("resolve: crossing restricted mount boundary denied")
	ErrPermission         = errors.New("resolve: permission denied")
	ErrReadOnly           = errors.New("resolve: mount is read-only")
	ErrBadPath            = errors.New("resolve: malformed path")
)

// Result is the outcome of a successful resolution.
type Result struct {
	Node   *vfs.Node
	Mount  *mount.Mount
	Root   *vfs.Node
	Hops   int
	InRoot bool
}

// Walker is the common interface implemented by both resolvers. Open returns
// the resolved node plus an error.
type Walker interface {
	Open(containerPath string, flags Flags, cred *userns.Cred) (*Result, error)
}

// Resolver bundles the context shared by every walk.
type Resolver struct {
	FS        *vfs.VFS
	NS        *mount.Namespace
	RootMount *mount.Mount
	Root      *vfs.Node
}

// NewResolver anchors a resolver at the root mount of ns.
func NewResolver(fs *vfs.VFS, ns *mount.Namespace) *Resolver {
	rootMount := ns.RootMount()
	return &Resolver{
		FS:        fs,
		NS:        ns,
		RootMount: rootMount,
		Root:      rootMount.Root,
	}
}

// SecureResolver is the openat2-semantics sandbox walker.
type SecureResolver struct{ *Resolver }

// NewSecure constructs a secure resolver.
func NewSecure(fs *vfs.VFS, ns *mount.Namespace) *SecureResolver {
	return &SecureResolver{NewResolver(fs, ns)}
}

type walkState struct {
	cur      *vfs.Node
	mount    *mount.Mount
	stack    []string
	hops     int
	flags    Flags
	cred     *userns.Cred
	resolver *Resolver
}

// Open walks containerPath entirely inside the container root.
func (r *SecureResolver) Open(containerPath string, flags Flags, cred *userns.Cred) (*Result, error) {
	if !strings.HasPrefix(containerPath, "/") {
		return nil, ErrBadPath
	}
	state := &walkState{
		cur:      r.Root,
		mount:    r.RootMount,
		stack:    vfs.Components(containerPath),
		flags:    flags,
		cred:     cred,
		resolver: r.Resolver,
	}
	node, finalMount, hops, err := state.walk()
	if err != nil {
		return nil, err
	}
	if !r.contained(node) {
		return nil, ErrEscapedRoot
	}
	if flags.RequireRegular && node.Kind() == vfs.KindDir {
		return nil, ErrIsDir
	}
	if flags.CheckDAC && cred != nil && node.Kind() == vfs.KindFile {
		if !cred.CanRead(node.UID(), node.GID(), node.Mode()) {
			return nil, ErrPermission
		}
	}
	return &Result{Node: node, Mount: finalMount, Root: r.Root, Hops: hops, InRoot: true}, nil
}

func (s *walkState) walk() (*vfs.Node, *mount.Mount, int, error) {
	for {
		if len(s.stack) == 0 {
			return s.cur, s.mount, s.hops, nil
		}
		component := s.stack[0]
		s.stack = s.stack[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			s.climb()
			continue
		}
		if s.cur.Kind() != vfs.KindDir {
			return nil, nil, s.hops, ErrNotDir
		}
		child, err := s.cur.Child(component)
		if err != nil {
			return nil, nil, s.hops, ErrNotExist
		}
		// Links are expanded in place: the cursor remains on the parent
		// directory and the target components are pushed onto the stack.
		if child.Kind() == vfs.KindMagicLink {
			if s.flags.NoMagicLinks {
				return nil, nil, s.hops, ErrMagicLink
			}
			s.stack = append(vfs.Components(child.Magic().Resolve()), s.stack...)
			s.cur = s.resolver.Root
			s.mount = s.resolver.RootMount
			continue
		}
		if child.Kind() == vfs.KindSymlink {
			s.hops++
			if s.hops > MaxSymlinkHops {
				return nil, nil, s.hops, ErrSymlinkLimit
			}
			target := child.Target()
			if strings.HasPrefix(target, "/") {
				s.cur = s.resolver.Root
				s.mount = s.resolver.RootMount
			}
			s.stack = append(vfs.Components(target), s.stack...)
			continue
		}
		s.enter(child)
	}
}

// enter moves onto node, switching to the topmost mount stacked on it.
func (s *walkState) enter(node *vfs.Node) {
	s.cur = node
	if stacked := s.resolver.NS.TopMountAt(node); stacked != nil {
		s.mount = stacked
		s.cur = stacked.Root
	}
}

// climb implements ".." with root clamping and mount boundary semantics.
func (s *walkState) climb() {
	// RESOLVE_IN_ROOT: ".." at the container root stays put.
	if s.cur == s.resolver.Root {
		return
	}
	// ".." at the root of a stacked bind mount returns to the mount point's
	// parent inside the container rather than the host-side source parent.
	if s.cur == s.mount.Root && s.mount != s.resolver.RootMount {
		point := s.mount.MountPoint
		parent := point.Parent()
		if parent == nil || parent == s.mount.Root {
			s.cur = s.resolver.Root
			s.mount = s.resolver.RootMount
			return
		}
		s.cur = parent
		s.mount = s.resolver.RootMount
		if stacked := s.resolver.NS.TopMountAt(parent); stacked != nil {
			s.mount = stacked
		}
		return
	}
	parent := s.cur.Parent()
	if parent == nil {
		s.cur = s.resolver.Root
		s.mount = s.resolver.RootMount
		return
	}
	if stacked := s.resolver.NS.TopMountAt(parent); stacked != nil {
		s.mount = stacked
	}
	s.cur = parent
}

// contained is the final RESOLVE_IN_ROOT containment predicate: the node must
// be reachable through the mount namespace and must not escape via mount
// source ancestry beyond the container root.
func (r *SecureResolver) contained(node *vfs.Node) bool {
	if node == r.Root || vfs.IsAncestor(r.Root, node) {
		return true
	}
	for _, m := range r.NS.Mounts() {
		if vfs.IsAncestor(m.Root, node) && (m.Root == r.Root || vfs.IsAncestor(r.Root, m.Root) || r.NS.Reachable(m.Root)) {
			return true
		}
	}
	return r.NS.Reachable(node)
}

// OpenSlow walks one component per call so a test harness can interleave
// attacker mutations between victim walk steps. Each call returns the node at
// the current cursor; Done reports completion.
func (r *SecureResolver) OpenSlow(containerPath string, flags Flags, cred *userns.Cred) *SlowWalk {
	return &SlowWalk{
		state: &walkState{
			cur:      r.Root,
			mount:    r.RootMount,
			stack:    vfs.Components(containerPath),
			flags:    flags,
			cred:     cred,
			resolver: r.Resolver,
		},
	}
}

// SlowWalk is a resumable component-by-component walk.
type SlowWalk struct {
	state *walkState
	err   error
	done  bool
}

// Step advances exactly one path component (including one symlink expansion).
func (w *SlowWalk) Step() (*vfs.Node, bool, error) {
	if w.done || w.err != nil {
		return w.state.cur, w.done, w.err
	}
	if len(w.state.stack) == 0 {
		w.done = true
		return w.state.cur, true, nil
	}
	component := w.state.stack[0]
	w.state.stack = w.state.stack[1:]
	if component == ".." {
		w.state.climb()
	} else if component != "" && component != "." {
		if w.state.cur.Kind() != vfs.KindDir {
			w.err = ErrNotDir
			return nil, false, w.err
		}
		node, err := w.state.cur.Child(component)
		if err != nil {
			w.err = ErrNotExist
			return nil, false, w.err
		}
		if node.Kind() == vfs.KindMagicLink {
			if w.state.flags.NoMagicLinks {
				w.err = ErrMagicLink
				return nil, false, w.err
			}
			w.state.stack = append(vfs.Components(node.Magic().Resolve()), w.state.stack...)
			w.state.cur = w.state.resolver.Root
			w.state.mount = w.state.resolver.RootMount
			return w.state.cur, false, nil
		}
		if node.Kind() == vfs.KindSymlink {
			w.state.hops++
			if w.state.hops > MaxSymlinkHops {
				w.err = ErrSymlinkLimit
				return nil, false, w.err
			}
			target := node.Target()
			if strings.HasPrefix(target, "/") {
				w.state.cur = w.state.resolver.Root
				w.state.mount = w.state.resolver.RootMount
			}
			w.state.stack = append(vfs.Components(target), w.state.stack...)
			return w.state.cur, false, nil
		}
		w.state.enter(node)
	}
	if len(w.state.stack) == 0 &&
		w.state.cur.Kind() != vfs.KindSymlink &&
		w.state.cur.Kind() != vfs.KindMagicLink {
		w.done = true
	}
	return w.state.cur, w.done, nil
}

// Finish performs the final containment/DAC checks and returns the result.
func (w *SlowWalk) Finish(r *SecureResolver) (*Result, error) {
	if w.err != nil {
		return nil, w.err
	}
	if !r.contained(w.state.cur) {
		return nil, ErrEscapedRoot
	}
	if w.state.flags.RequireRegular && w.state.cur.Kind() == vfs.KindDir {
		return nil, ErrIsDir
	}
	return &Result{Node: w.state.cur, Mount: w.state.mount, Root: r.Root, Hops: w.state.hops, InRoot: true}, nil
}
