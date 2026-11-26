// Package mount models mount namespaces as trees of mount objects.
//
// Every mount carries its source root inode, mount point, read-only flag, the
// MNT_LOCKED unmount/attr-change lock, propagation type and peer-group
// membership. The package implements the security decisions around unmount
// and read-only flag changes and the delivery rules of MS_SHARED / MS_SLAVE
// propagation events, including the guarantee that events never leak upwards
// from a slave to its master.
package mount

import (
	"errors"
	"fmt"
	"sync"

	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// Propagation mirrors the kernel mount propagation types.
type Propagation uint8

const (
	// PropPrivate is MS_PRIVATE: no propagation in or out.
	PropPrivate Propagation = iota
	// PropShared is MS_SHARED: bidirectional propagation through a peer group.
	PropShared
	// PropSlave is MS_SLAVE: receives events from a master peer group but
	// never sends events back.
	PropSlave
)

// String renders the propagation flag name.
func (p Propagation) String() string {
	switch p {
	case PropShared:
		return "MS_SHARED"
	case PropSlave:
		return "MS_SLAVE"
	default:
		return "MS_PRIVATE"
	}
}

// Errors returned by mount operation adjudication.
var (
	ErrNoCapability    = errors.New("mount: credential lacks CAP_SYS_ADMIN")
	ErrLockedMount     = errors.New("mount: MNT_LOCKED mount cannot be unmounted or changed from a non-initial user namespace")
	ErrReadOnlyBind    = errors.New("mount: cannot strip MS_RDONLY from a read-only bind mount in a non-initial user namespace")
	ErrMountBusy       = errors.New("mount: mount has child mounts")
	ErrUnknownMount    = errors.New("mount: mount not present in namespace")
	ErrBadMountPoint   = errors.New("mount: mount point must be a directory")
	ErrPropagationLeak = errors.New("mount: slave event cannot propagate to a master peer group")
)

// EventKind classifies a propagated mount event.
type EventKind uint8

const (
	// EventMount is a new bind mount appearing.
	EventMount EventKind = iota
	// EventUnmount is a mount disappearing.
	EventUnmount
	// EventRemount is an attribute (for example read-only) change.
	EventRemount
)

func (k EventKind) String() string {
	switch k {
	case EventUnmount:
		return "umount"
	case EventRemount:
		return "remount"
	default:
		return "mount"
	}
}

// Event is a propagation record delivered to a namespace.
type Event struct {
	Kind       EventKind
	MountPoint *vfs.Node
	Source     *vfs.Node
	OriginNS   *Namespace
	TargetNS   *Namespace
}

// PeerGroup is a propagation peer group (MS_SHARED). All member mounts
// exchange mount/unmount events bidirectionally. Slave mounts attach to a
// peer group as their master without becoming members.
type PeerGroup struct {
	mu      sync.Mutex
	ID      int
	members []*Mount
	slaves  []*Mount
}

// Members returns a snapshot of shared member mounts.
func (g *PeerGroup) Members() []*Mount {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*Mount, len(g.members))
	copy(out, g.members)
	return out
}

// Slaves returns a snapshot of slave mounts.
func (g *PeerGroup) Slaves() []*Mount {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*Mount, len(g.slaves))
	copy(out, g.slaves)
	return out
}

// Mount is a node in the mount tree of a [Namespace].
type Mount struct {
	mu sync.RWMutex

	ID         int
	Root       *vfs.Node
	MountPoint *vfs.Node
	Parent     *Mount
	Children   []*Mount

	ReadOnly bool
	Locked   bool
	Bind     bool

	Propagation Propagation
	PeerGroup   *PeerGroup
	Masters     []*PeerGroup

	OwnerNS *userns.UserNamespace
	hostNS  *Namespace
}

// String gives a stable debug rendering.
func (m *Mount) String() string {
	return fmt.Sprintf("mount#%d(%s->%s,ro=%v,locked=%v,%s)",
		m.ID, vfs.PathOf(m.Root), vfs.PathOf(m.MountPoint), m.ReadOnly, m.Locked, m.Propagation)
}

// Namespace is a mount namespace. Child namespaces clone the mount list and
// connect shared mounts through peer groups.
type Namespace struct {
	mu sync.RWMutex

	Name   string
	Parent *Namespace
	UserNS *userns.UserNamespace

	root   *Mount
	mounts []*Mount
	events []Event

	nextMountID int
	nextGroupID int
	groups      []*PeerGroup
}

// NewRootNamespace creates the initial mount namespace whose root mount is
// rooted at rootNode.
func NewRootNamespace(name string, rootNode *vfs.Node, owner *userns.UserNamespace) *Namespace {
	ns := &Namespace{Name: name, UserNS: owner, nextMountID: 1, nextGroupID: 1}
	root := ns.newMount(rootNode, rootNode, nil, false)
	root.Propagation = PropPrivate
	ns.root = root
	ns.mounts = []*Mount{root}
	return ns
}

func (ns *Namespace) newMount(root, point *vfs.Node, parent *Mount, bind bool) *Mount {
	m := &Mount{
		ID:          ns.nextMountID,
		Root:        root,
		MountPoint:  point,
		Parent:      parent,
		ReadOnly:    false,
		Bind:        bind,
		Propagation: PropPrivate,
		OwnerNS:     ns.UserNS,
		hostNS:      ns,
	}
	ns.nextMountID++
	return m
}

func (ns *Namespace) newPeerGroup() *PeerGroup {
	g := &PeerGroup{ID: ns.nextGroupID}
	ns.nextGroupID++
	ns.groups = append(ns.groups, g)
	return g
}

// RootMount returns the namespace's root mount.
func (ns *Namespace) RootMount() *Mount {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.root
}

// Mounts returns a snapshot of all mounts in the namespace.
func (ns *Namespace) Mounts() []*Mount {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]*Mount, len(ns.mounts))
	copy(out, ns.mounts)
	return out
}

// Events returns a snapshot of propagation events delivered to the namespace.
func (ns *Namespace) Events() []Event {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]Event, len(ns.events))
	copy(out, ns.events)
	return out
}

func (ns *Namespace) recordEvent(ev Event) {
	ns.mu.Lock()
	ns.events = append(ns.events, ev)
	ns.mu.Unlock()
}

// TopMountAt returns the topmost (most recently stacked) mount whose mount
// point equals node, or nil if none exists.
func (ns *Namespace) TopMountAt(node *vfs.Node) *Mount {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	var top *Mount
	for _, m := range ns.mounts {
		if m.MountPoint == node {
			top = m
		}
	}
	return top
}

// MountAt returns the stack of mounts mounted on node, bottom to top.
func (ns *Namespace) MountAt(node *vfs.Node) []*Mount {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	var out []*Mount
	for _, m := range ns.mounts {
		if m.MountPoint == node {
			out = append(out, m)
		}
	}
	return out
}

// Reachable reports whether node lies inside any mount source root in the
// namespace. It is the structural sandbox invariant: a node is reachable only
// if some mount exposes its subtree to the container.
func (ns *Namespace) Reachable(node *vfs.Node) bool {
	ns.mu.RLock()
	mounts := make([]*Mount, len(ns.mounts))
	copy(mounts, ns.mounts)
	ns.mu.RUnlock()
	for _, m := range mounts {
		if vfs.IsAncestor(m.Root, node) {
			return true
		}
	}
	return false
}

// ChildMounts returns the direct child mounts of m.
func (m *Mount) ChildMounts() []*Mount {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Mount, len(m.Children))
	copy(out, m.Children)
	return out
}

// IsRoot reports whether m is the namespace root mount.
func (m *Mount) IsRoot() bool { return m.Parent == nil }
