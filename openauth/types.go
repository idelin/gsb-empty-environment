package openauth

import "context"

// Verifier is the 8-byte opaque create verifier used by EXCLUSIVE4_1.
type Verifier [8]byte

// CreateHow mirrors the NFSv4.1 createmode4 enumeration (RFC 8881).
type CreateHow uint32

const (
	// CreateUnchecked is UNCHECKED4: create if absent, otherwise open it.
	CreateUnchecked CreateHow = 0
	// CreateGuarded is GUARDED4: fail with NFS4ERR_EXIST if the file exists.
	CreateGuarded CreateHow = 1
	// CreateExclusive is EXCLUSIVE4_1: verifier based idempotent create.
	CreateExclusive CreateHow = 2
)

// ShareAccess mirrors the OPEN4_SHARE_ACCESS_* bits.
type ShareAccess uint32

const (
	ShareAccessNone  ShareAccess = 0
	ShareAccessRead  ShareAccess = 1
	ShareAccessWrite ShareAccess = 2
	ShareAccessBoth  ShareAccess = ShareAccessRead | ShareAccessWrite
)

// ShareDeny mirrors the OPEN4_SHARE_DENY_* bits.
type ShareDeny uint32

const (
	ShareDenyNone  ShareDeny = 0
	ShareDenyRead  ShareDeny = 4
	ShareDenyWrite ShareDeny = 8
	ShareDenyBoth  ShareDeny = ShareDenyRead | ShareDenyWrite
)

// AccessRights is a bit set of operations the caller may perform on a node.
type AccessRights uint32

const (
	AccessRead    AccessRights = 1 << 0
	AccessWrite   AccessRights = 1 << 1
	AccessExecute AccessRights = 1 << 2
)

// NodeID identifies an object in the backend file system.
type NodeID string

// Node is the minimal metadata the authorizer needs about an existing
// object. Backends map their own file handles onto this view.
type Node interface {
	ID() NodeID
	UID() uint32
	GID() uint32
	// Mode returns the Unix permission bits (e.g. 0o644).
	Mode() uint32
	IsDir() bool
}

// InitialAttrs is what the backend must apply when materializing a new file.
type InitialAttrs struct {
	UID  uint32
	GID  uint32
	Mode uint32
	Size uint64
}

// ErrNotExist is returned by Backend.Lookup when a name does not resolve.
var ErrNotExist = notExistError{}

type notExistError struct{}

func (notExistError) Error() string { return "openauth: no such file or directory" }

// Backend is the storage boundary the authorizer is built on. All methods
// must be safe for concurrent use; Authorizer.Open serializes its own state
// but the backend may be shared with other server machinery.
type Backend interface {
	// Lookup resolves name inside parent. It returns ErrNotExist when the
	// name is absent.
	Lookup(ctx context.Context, parent Node, name string) (Node, error)

	// Create materializes a new regular file inside parent and returns it.
	Create(ctx context.Context, parent Node, name string, attrs InitialAttrs) (Node, error)

	// Remove deletes a file previously created during the same OPEN so a
	// failed OPEN leaves no trace.
	Remove(ctx context.Context, parent Node, name string) error

	// Access evaluates the effective rights of id on node using the
	// backend's permission policy (typically Unix mode bits via UnixAccess).
	Access(ctx context.Context, id Identity, node Node) (AccessRights, error)

	// SetCreateVerifier stores verifier on node (EXCLUSIVE4_1) so a retried
	// OPEN can recognize it. It may persist it in a named attribute.
	SetCreateVerifier(ctx context.Context, node Node, verifier Verifier) error

	// CreateVerifier returns the stored verifier and true, or zero/false if
	// the node was not created with EXCLUSIVE4_1.
	CreateVerifier(ctx context.Context, node Node) (Verifier, bool, error)
}

// UnixAccess computes the rights granted to id on a node from standard Unix
// permission bits, including set-user-ID/set-group-ID handling. A privileged
// identity is granted read/write/execute unconditionally, matching the
// CAP_DAC_OVERRIDE semantics expected of an NFS root identity.
func UnixAccess(id Identity, node Node) AccessRights {
	mode := node.Mode()
	if id.Privileged {
		// Root may always read/write; execute requires at least one execute
		// bit to be set, mirroring the traditional kernel rule.
		rights := AccessRead | AccessWrite
		if mode&0o111 != 0 {
			rights |= AccessExecute
		}
		return rights
	}

	var r, w, x uint32
	switch {
	case node.UID() == id.UID:
		r, w, x = 0o400, 0o200, 0o100
	case id.IsGroup(node.GID()):
		r, w, x = 0o040, 0o020, 0o010
	default:
		r, w, x = 0o004, 0o002, 0o001
	}

	var rights AccessRights
	if mode&r != 0 {
		rights |= AccessRead
	}
	if mode&w != 0 {
		rights |= AccessWrite
	}
	if mode&x != 0 {
		rights |= AccessExecute
	}
	return rights
}
