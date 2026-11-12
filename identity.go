package netsec

import "syscall"

// ErrAccess is returned whenever a caller identity cannot be resolved to a
// backend identity. The daemon must never fall back to its own credentials.
var ErrAccess = syscall.EACCES

// Identity is a backend (storage-side) credential produced by mapping a
// FUSE request header through a Mapper.
type Identity struct {
	UID uint32
	GID uint32
}

// Header mirrors the identity-relevant fields of a FUSE request header
// (fuse_in_header.uid / fuse_in_header.gid).
type Header struct {
	UID uint32
	GID uint32
}

// Op identifies the kind of FUSE request being served.
type Op int

const (
	OpLookup Op = iota
	OpGetattr
	OpSetattr
	OpReadlink
	OpMknod
	OpMkdir
	OpUnlink
	OpRmdir
	OpSymlink
	OpRename
	OpLink
	OpOpen
	OpRead
	OpWrite
	OpStatfs
	OpRelease
	OpFsync
	OpSetxattr
	OpGetxattr
	OpListxattr
	OpRemovexattr
	OpFlush
	OpOpendir
	OpReaddir
	OpReleasedir
	OpFsyncdir
	OpCreate
	OpAccess
	// OpForget and OpInterrupt carry no authorization meaning: the kernel
	// sends them for cache management and request cancellation, and their
	// header uid/gid must not be used for access decisions.
	OpForget
	OpInterrupt
	OpInit
	OpDestroy
)

// RequiresMapping reports whether the operation performs an authorization
// decision and therefore needs a mapped backend identity. Forget, interrupt
// and other bookkeeping requests are never mapped.
func (op Op) RequiresMapping() bool {
	switch op {
	case OpForget, OpInterrupt, OpInit, OpDestroy:
		return false
	default:
		return true
	}
}

// Mapper translates caller credentials from FUSE request headers into
// backend identities according to caller-supplied lookup tables.
//
// A uid that is absent from the table is rejected with ErrAccess; there is
// no fallback to the daemon's own identity. uid 0 is treated like any other
// uid: it is valid only when explicitly present in the table.
type Mapper struct {
	uidMap map[uint32]uint32
	gidMap map[uint32]uint32
	// hasGIDMap records whether a gid table was supplied. Without one,
	// gids pass through unchanged; with one, unmapped gids are rejected.
	hasGIDMap bool
}

// NewMapper builds a Mapper from uid and gid translation tables. gidMap may
// be nil, in which case gids are passed through unmapped.
func NewMapper(uidMap, gidMap map[uint32]uint32) *Mapper {
	uidCopy := make(map[uint32]uint32, len(uidMap))
	for k, v := range uidMap {
		uidCopy[k] = v
	}
	m := &Mapper{uidMap: uidCopy}
	if gidMap != nil {
		gidCopy := make(map[uint32]uint32, len(gidMap))
		for k, v := range gidMap {
			gidCopy[k] = v
		}
		m.gidMap = gidCopy
		m.hasGIDMap = true
	}
	return m
}

// MapRequest resolves the caller in hdr to a backend identity for op.
//
// Operations without authorization meaning (forget, interrupt, ...) are not
// mapped and return the zero Identity with a nil error. For all other
// operations an unmapped uid (including uid 0 when not explicitly listed)
// yields ErrAccess, never the daemon's identity.
func (m *Mapper) MapRequest(hdr Header, op Op) (Identity, error) {
	if !op.RequiresMapping() {
		return Identity{}, nil
	}
	uid, ok := m.uidMap[hdr.UID]
	if !ok {
		return Identity{}, ErrAccess
	}
	gid := hdr.GID
	if m.hasGIDMap {
		mapped, ok := m.gidMap[hdr.GID]
		if !ok {
			return Identity{}, ErrAccess
		}
		gid = mapped
	}
	return Identity{UID: uid, GID: gid}, nil
}

// CreateOwner returns the backend owner (uid, gid) that must be stamped on
// a file newly created by the caller described in hdr.
func (m *Mapper) CreateOwner(hdr Header) (uid, gid uint32, err error) {
	id, err := m.MapRequest(hdr, OpCreate)
	if err != nil {
		return 0, 0, err
	}
	return id.UID, id.GID, nil
}
