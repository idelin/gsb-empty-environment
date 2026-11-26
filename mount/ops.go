package mount

import (
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// BindOptions controls a bind mount creation.
type BindOptions struct {
	// ReadOnly sets MS_RDONLY on the new bind mount.
	ReadOnly bool
	// Locked sets MNT_LOCKED, as the kernel does for mounts created by a
	// more privileged user namespace and locked against the child.
	Locked bool
	// Propagation selects MS_SHARED / MS_SLAVE / MS_PRIVATE.
	Propagation Propagation
}

// Clone creates a child mount namespace. Shared mounts stay attached to their
// peer group; slave mounts keep the same masters; private mounts remain
// private. The child is associated with the given user namespace.
func (ns *Namespace) Clone(name string, userNS *userns.UserNamespace) *Namespace {
	ns.mu.Lock()
	child := &Namespace{
		Name:        name,
		Parent:      ns,
		UserNS:      userNS,
		nextMountID: ns.nextMountID,
		nextGroupID: ns.nextGroupID,
		groups:      append([]*PeerGroup(nil), ns.groups...),
	}

	byOld := make(map[*Mount]*Mount, len(ns.mounts))
	var childRoot *Mount
	for _, old := range ns.mounts {
		cp := child.newMount(old.Root, old.MountPoint, nil, old.Bind)
		cp.ReadOnly = old.ReadOnly
		cp.Locked = old.Locked
		cp.Propagation = old.Propagation
		cp.PeerGroup = old.PeerGroup
		cp.Masters = append(cp.Masters, old.Masters...)
		if old.IsRoot() {
			childRoot = cp
		}
		byOld[old] = cp
	}
	for old, cp := range byOld {
		if old.Parent != nil {
			parent := byOld[old.Parent]
			cp.Parent = parent
			parent.Children = append(parent.Children, cp)
		}
		if cp.Propagation == PropShared && cp.PeerGroup != nil {
			cp.PeerGroup.mu.Lock()
			cp.PeerGroup.members = append(cp.PeerGroup.members, cp)
			cp.PeerGroup.mu.Unlock()
		}
		if cp.Propagation == PropSlave && len(cp.Masters) > 0 {
			master := cp.Masters[0]
			master.mu.Lock()
			master.slaves = append(master.slaves, cp)
			master.mu.Unlock()
		}
	}
	child.root = childRoot
	child.mounts = make([]*Mount, 0, len(ns.mounts))
	for _, old := range ns.mounts {
		child.mounts = append(child.mounts, byOld[old])
	}
	ns.mu.Unlock()
	return child
}

// BindMount creates a bind mount of source at mountPoint and propagates the
// event according to the parent mount's propagation type.
func (ns *Namespace) BindMount(source, mountPoint *vfs.Node, opts BindOptions, cred *userns.Cred) (*Mount, error) {
	if err := checkCap(ns, cred); err != nil {
		return nil, err
	}
	if mountPoint.Kind() != vfs.KindDir {
		return nil, ErrBadMountPoint
	}
	ns.mu.Lock()
	parent := ns.topmostMountLocked(mountPoint)
	if parent == nil {
		ns.mu.Unlock()
		return nil, ErrBadMountPoint
	}
	propParent := parent
	m := ns.newMount(source, mountPoint, parent, true)
	m.ReadOnly = opts.ReadOnly
	m.Locked = opts.Locked
	m.Propagation = opts.Propagation
	parent.Children = append(parent.Children, m)
	ns.mounts = append(ns.mounts, m)

	var group *PeerGroup
	switch opts.Propagation {
	case PropShared:
		group = ns.newPeerGroup()
		group.members = append(group.members, m)
		m.PeerGroup = group
	case PropSlave:
		if propParent.Propagation == PropShared && propParent.PeerGroup != nil {
			m.Masters = append(m.Masters, propParent.PeerGroup)
			propParent.PeerGroup.mu.Lock()
			propParent.PeerGroup.slaves = append(propParent.PeerGroup.slaves, m)
			propParent.PeerGroup.mu.Unlock()
		}
	}
	ns.mu.Unlock()

	ns.propagateFrom(propParent, Event{
		Kind:       EventMount,
		MountPoint: mountPoint,
		Source:     source,
		OriginNS:   ns,
	})
	return m, nil
}

// Unmount removes m from the namespace after the MNT_LOCKED and busy checks.
func (ns *Namespace) Unmount(m *Mount, cred *userns.Cred) error {
	if err := checkCap(ns, cred); err != nil {
		return err
	}
	if err := ns.ownsMount(m); err != nil {
		return err
	}
	if m.Locked && !ns.UserNS.IsInitial() {
		return ErrLockedMount
	}
	m.mu.RLock()
	busy := len(m.Children) > 0
	parent := m.Parent
	m.mu.RUnlock()
	if busy {
		return ErrMountBusy
	}

	ns.mu.Lock()
	filtered := ns.mounts[:0]
	for _, candidate := range ns.mounts {
		if candidate != m {
			filtered = append(filtered, candidate)
		}
	}
	ns.mounts = filtered
	if parent != nil {
		parent.mu.Lock()
		siblings := parent.Children[:0]
		for _, sibling := range m.Parent.Children {
			if sibling != m {
				siblings = append(siblings, sibling)
			}
		}
		parent.Children = siblings
		parent.mu.Unlock()
	}
	if m.PeerGroup != nil {
		m.PeerGroup.mu.Lock()
		members := m.PeerGroup.members[:0]
		for _, member := range m.PeerGroup.members {
			if member != m {
				members = append(members, member)
			}
		}
		m.PeerGroup.members = members
		m.PeerGroup.mu.Unlock()
	}
	ns.mu.Unlock()

	ns.propagateFrom(ns.coveringMount(m.MountPoint), Event{
		Kind:       EventUnmount,
		MountPoint: m.MountPoint,
		Source:     m.Root,
		OriginNS:   ns,
	})
	return nil
}

// SetReadOnly applies or removes MS_RDONLY on m. Removing the flag from a
// read-only bind mount while running in a non-initial user namespace is
// rejected, mirroring the MNT_LOCK_READONLY kernel lock.
func (ns *Namespace) SetReadOnly(m *Mount, readOnly bool, cred *userns.Cred) error {
	if err := checkCap(ns, cred); err != nil {
		return err
	}
	if err := ns.ownsMount(m); err != nil {
		return err
	}
	m.mu.RLock()
	bindRO := m.Bind && m.ReadOnly
	locked := m.Locked
	m.mu.RUnlock()
	if locked && !ns.UserNS.IsInitial() {
		return ErrLockedMount
	}
	if !readOnly && bindRO && !ns.UserNS.IsInitial() {
		return ErrReadOnlyBind
	}
	m.mu.Lock()
	m.ReadOnly = readOnly
	m.mu.Unlock()

	ns.propagateFrom(ns.coveringMount(m.MountPoint), Event{
		Kind:       EventRemount,
		MountPoint: m.MountPoint,
		Source:     m.Root,
		OriginNS:   ns,
	})
	return nil
}

// SetPropagation changes MS_SHARED / MS_SLAVE / MS_PRIVATE on m and updates
// peer-group memberships.
func (ns *Namespace) SetPropagation(m *Mount, prop Propagation, cred *userns.Cred) error {
	if err := checkCap(ns, cred); err != nil {
		return err
	}
	if err := ns.ownsMount(m); err != nil {
		return err
	}
	ns.mu.Lock()
	m.mu.Lock()
	var previousGroup *PeerGroup
	if m.PeerGroup != nil {
		group := m.PeerGroup
		previousGroup = group
		group.mu.Lock()
		members := group.members[:0]
		for _, member := range group.members {
			if member != m {
				members = append(members, member)
			}
		}
		group.members = members
		group.mu.Unlock()
		m.PeerGroup = nil
	}
	m.Masters = nil
	m.Propagation = prop
	if prop == PropShared {
		group := ns.newPeerGroup()
		group.members = append(group.members, m)
		m.PeerGroup = group
	}
	if prop == PropSlave {
		// The mount leaves its shared peer group but keeps it as the master,
		// which is exactly the MS_SHARED -> MS_SLAVE kernel transition.
		group := previousGroup
		if group == nil && m.Parent != nil && m.Parent.PeerGroup != nil {
			group = m.Parent.PeerGroup
		}
		if group != nil {
			m.Masters = append(m.Masters, group)
			group.mu.Lock()
			group.slaves = append(group.slaves, m)
			group.mu.Unlock()
		}
	}
	m.mu.Unlock()
	ns.mu.Unlock()
	return nil
}

// Lock marks m with MNT_LOCKED. Once locked, non-initial user namespaces can
// neither unmount it nor change its locked attributes.
func (ns *Namespace) Lock(m *Mount) error {
	if err := ns.ownsMount(m); err != nil {
		return err
	}
	m.mu.Lock()
	m.Locked = true
	m.mu.Unlock()
	return nil
}

func checkCap(ns *Namespace, cred *userns.Cred) error {
	if cred == nil || cred.NS != ns.UserNS || !cred.NS.HasCap("CAP_SYS_ADMIN") {
		return ErrNoCapability
	}
	return nil
}

func (ns *Namespace) ownsMount(m *Mount) error {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	for _, candidate := range ns.mounts {
		if candidate == m {
			return nil
		}
	}
	return ErrUnknownMount
}

func (ns *Namespace) topmostMountLocked(node *vfs.Node) *Mount {
	// Exact mount-point matches always win, with later mounts stacked on top.
	var exact *Mount
	for _, m := range ns.mounts {
		if m.MountPoint == node {
			exact = m
		}
	}
	if exact != nil {
		return exact
	}
	// Fall back to the mount whose point is the nearest ancestor directory of
	// node in the same directory tree.
	var best *Mount
	bestDepth := -1
	for cur := node; cur != nil; cur = cur.Parent() {
		for _, m := range ns.mounts {
			if m.MountPoint == cur {
				depth := depthOf(cur)
				if depth > bestDepth {
					best = m
					bestDepth = depth
				}
			}
		}
	}
	return best
}

func depthOf(node *vfs.Node) int {
	depth := 0
	for cur := node; cur != nil && cur.Parent() != nil; cur = cur.Parent() {
		depth++
	}
	return depth
}

// coveringMount returns the deepest mount whose mount point is node itself or
// an ancestor directory of node.
func (ns *Namespace) coveringMount(node *vfs.Node) *Mount {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.topmostMountLocked(node)
}

// propagateFrom delivers an event originating at originMount to every
// namespace that must observe it. The decision is made on the mount itself:
//
//   - MS_SHARED: deliver to every peer-group member namespace and to every
//     slave attached to the group.
//   - MS_SLAVE / MS_PRIVATE: no outgoing delivery. A slave only receives
//     events produced inside its master, which is covered by the shared case
//     above when the master namespace is the origin.
//
// Events therefore never travel from a slave back to a master, and never to
// an ancestor namespace of the origin.
func (ns *Namespace) propagateFrom(originMount *Mount, ev Event) {
	if originMount == nil {
		return
	}
	originMount.mu.RLock()
	if originMount.Propagation != PropShared {
		originMount.mu.RUnlock()
		return
	}
	group := originMount.PeerGroup
	originMount.mu.RUnlock()
	if group == nil {
		return
	}
	targets := map[*Namespace]bool{}
	group.mu.Lock()
	for _, member := range group.members {
		if member.hostNS != ns && ns.descendantOf(ns, member.hostNS) {
			targets[member.hostNS] = true
		}
	}
	for _, slave := range group.slaves {
		if slave.hostNS != ns && ns.descendantOf(ns, slave.hostNS) {
			targets[slave.hostNS] = true
		}
	}
	group.mu.Unlock()
	for target := range targets {
		copied := ev
		copied.TargetNS = target
		target.deliver(copied, group)
	}
}

// deliver records an event and, for mount/unmount events, mutates the target
// namespace's mount tree so propagation is observable structurally, not only
// through the event log. Slave namespaces receive copies marked as slaves of
// the same peer group; shared peers receive full peer members.
func (ns *Namespace) deliver(ev Event, masterGroup *PeerGroup) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	switch ev.Kind {
	case EventMount:
		// Skip if an equivalent mount already exists at the point.
		for _, m := range ns.mounts {
			if m.MountPoint == ev.MountPoint && m.Root == ev.Source {
				ns.events = append(ns.events, ev)
				return
			}
		}
		parent := ns.topmostMountLocked(ev.MountPoint)
		if parent == nil {
			parent = ns.root
		}
		m := ns.newMount(ev.Source, ev.MountPoint, parent, true)
		m.PeerGroup = masterGroup
		m.ReadOnly = false
		if parent.Propagation == PropSlave {
			m.Propagation = PropSlave
			m.Masters = append(m.Masters, masterGroup)
		} else {
			m.Propagation = PropShared
			masterGroup.mu.Lock()
			masterGroup.members = append(masterGroup.members, m)
			masterGroup.mu.Unlock()
		}
		parent.Children = append(parent.Children, m)
		ns.mounts = append(ns.mounts, m)
	case EventUnmount:
		filtered := ns.mounts[:0]
		for _, m := range ns.mounts {
			if m.MountPoint == ev.MountPoint && m.Root == ev.Source {
				continue
			}
			filtered = append(filtered, m)
		}
		ns.mounts = filtered
	}
	ns.events = append(ns.events, ev)
}

// descendantOf reports whether child is ancestor itself or nested below it.
func (ns *Namespace) descendantOf(ancestor, child *Namespace) bool {
	for cur := child; cur != nil; cur = cur.Parent {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// PeerGroupOf returns the peer group of m, or nil.
func (m *Mount) PeerGroupOf() *PeerGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.PeerGroup
}

// IsReadOnly reports the MS_RDONLY state.
func (m *Mount) IsReadOnly() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ReadOnly
}

// IsLocked reports the MNT_LOCKED state.
func (m *Mount) IsLocked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Locked
}
