// Package userns models Linux user namespaces: UID/GID id-mappings,
// credentials translated across namespace boundaries and the capability
// checks that gate mount operations.
package userns

import (
	"errors"
	"io/fs"
)

// IDMapEntry maps one contiguous id range between two namespaces.
type IDMapEntry struct {
	// Container is the id seen inside the child namespace.
	Container uint32
	// Host is the id seen in the parent namespace.
	Host uint32
	// Count is the number of ids covered.
	Count uint32
}

// IDMap is a set of non-overlapping id-mapping ranges.
type IDMap struct {
	Entries []IDMapEntry
}

var (
	// ErrNoMapping indicates an id is not covered by any mapping range.
	ErrNoMapping = errors.New("userns: id has no mapping")
)

// ToHost translates a child-namespace id into the parent namespace.
func (m IDMap) ToHost(id uint32) (uint32, error) {
	for _, e := range m.Entries {
		if id >= e.Container && id < e.Container+e.Count {
			return e.Host + (id - e.Container), nil
		}
	}
	return 0, ErrNoMapping
}

// ToContainer translates a parent-namespace id into the child namespace.
func (m IDMap) ToContainer(id uint32) (uint32, error) {
	for _, e := range m.Entries {
		if id >= e.Host && id < e.Host+e.Count {
			return e.Container + (id - e.Host), nil
		}
	}
	return 0, ErrNoMapping
}

// UserNamespace is a node in the user-namespace hierarchy. The initial
// namespace has Parent == nil and is the only namespace considered "initial".
type UserNamespace struct {
	Name   string
	Parent *UserNamespace
	UIDMap IDMap
	GIDMap IDMap

	// capabilities granted to the namespace owner, expressed as capability
	// names (for example "CAP_SYS_ADMIN").
	caps map[string]bool
}

// NewInitial creates the initial user namespace with identity mappings and the
// full capability set.
func NewInitial() *UserNamespace {
	ns := &UserNamespace{
		Name: "init_user_ns",
		caps: map[string]bool{},
	}
	ns.UIDMap = IDMap{Entries: []IDMapEntry{{Container: 0, Host: 0, Count: 4294967295}}}
	ns.GIDMap = ns.UIDMap
	for _, cap := range AllCapabilities() {
		ns.caps[cap] = true
	}
	return ns
}

// Child creates a new user namespace owned by the receiver.
func (u *UserNamespace) Child(name string, uidMap, gidMap IDMap, caps ...string) *UserNamespace {
	child := &UserNamespace{
		Name:   name,
		Parent: u,
		UIDMap: uidMap,
		GIDMap: gidMap,
		caps:   map[string]bool{},
	}
	for _, cap := range caps {
		child.caps[cap] = true
	}
	return child
}

// IsInitial reports whether this is the host's initial user namespace.
func (u *UserNamespace) IsInitial() bool { return u.Parent == nil }

// HasCap reports whether the namespace grants the named capability.
func (u *UserNamespace) HasCap(cap string) bool { return u.caps[cap] }

// Depth returns the nesting depth, zero for the initial namespace.
func (u *UserNamespace) Depth() int {
	d := 0
	for cur := u; cur.Parent != nil; cur = cur.Parent {
		d++
	}
	return d
}

// Cred is a set of credentials in a specific user namespace, mirroring
// struct cred (fsuid/fsgid plus supplementary groups).
type Cred struct {
	NS     *UserNamespace
	UID    uint32
	GID    uint32
	Groups []uint32
}

// HostUID walks every mapping level to obtain the kernel-level host UID.
func (c *Cred) HostUID() (uint32, error) {
	id := c.UID
	for ns := c.NS; ns.Parent != nil; ns = ns.Parent {
		mapped, err := ns.UIDMap.ToHost(id)
		if err != nil {
			return 0, err
		}
		id = mapped
	}
	return id, nil
}

// HostGID walks every mapping level to obtain the kernel-level host GID.
func (c *Cred) HostGID() (uint32, error) {
	id := c.GID
	for ns := c.NS; ns.Parent != nil; ns = ns.Parent {
		mapped, err := ns.GIDMap.ToHost(id)
		if err != nil {
			return 0, err
		}
		id = mapped
	}
	return id, nil
}

// CanRead performs a simplified DAC read check against node ownership and
// permission bits, resolving both sides to host ids.
func (c *Cred) CanRead(nodeUID, nodeGID uint32, mode fs.FileMode) bool {
	hostUID, err := c.HostUID()
	if err != nil {
		return false
	}
	hostGID, err := c.HostGID()
	if err != nil {
		return false
	}
	owner := hostUID == nodeUID
	group := hostGID == nodeGID
	for _, g := range c.Groups {
		if g == nodeGID {
			group = true
		}
	}
	switch {
	case owner:
		return mode&0o400 != 0
	case group:
		return mode&0o040 != 0
	default:
		return mode&0o004 != 0
	}
}

// AllCapabilities returns the capability names modelled by this package.
func AllCapabilities() []string {
	return []string{
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_DAC_READ_SEARCH",
		"CAP_FOWNER",
		"CAP_FSETID",
		"CAP_SETUID",
		"CAP_SETGID",
		"CAP_SYS_ADMIN",
	}
}
