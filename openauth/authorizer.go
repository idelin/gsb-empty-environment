package openauth

import (
	"context"
	"sync"
	"sync/atomic"
)

// CreateAttrs is the owner/mode/size attribute triple the client may carry
// in an OPEN CREATE. Nil pointers mean the attribute was not supplied.
type CreateAttrs struct {
	Owner *uint32
	Group *uint32
	Mode  *uint32
	Size  *uint64
}

// OpenRequest is the translated NFSv4.1 OPEN operation.
type OpenRequest struct {
	// Cred is the raw AUTH_SYS credential from the RPC header.
	Cred AuthSysCreds

	// Parent is the directory in which Name is created/looked up.
	Parent Node
	Name   string

	// Create selects the CREATE branch. When false the request opens an
	// existing file and How/Attrs/Verifier are ignored.
	Create   bool
	How      CreateHow
	Attrs    CreateAttrs
	Verifier Verifier

	ShareAccess ShareAccess
	ShareDeny   ShareDeny

	// ClientID/OpenOwner identify the open-owner (RFC 8881 section 9.5).
	// Reopening the same open-owner replaces its prior share reservation.
	ClientID  string
	OpenOwner string
}

// OpenState is one granted share reservation.
type OpenState struct {
	ID          uint64
	Node        NodeID
	ClientID    string
	OpenOwner   string
	Access      ShareAccess
	Deny        ShareDeny
	Identity    Identity
	CreatedFile bool
}

// OpenResult reports the outcome of an authorized OPEN.
type OpenResult struct {
	Node        Node
	State       *OpenState
	CreatedFile bool
	// Retry is true for an EXCLUSIVE4_1 request that matched the verifier
	// of an already existing file instead of creating a new one.
	Retry bool
}

// Authorizer performs OPEN authorization against a Backend. It is safe for
// concurrent use.
type Authorizer struct {
	backend Backend
	mapper  UIDMapper

	mu     sync.Mutex
	states map[uint64]*OpenState
	seq    atomic.Uint64
}

// New builds an Authorizer. A nil mapper means credentials pass through
// unchanged and uid 0 is treated as privileged.
func New(backend Backend, mapper UIDMapper) *Authorizer {
	if mapper == nil {
		mapper = UIDMapFunc(func(c AuthSysCreds) Identity {
			return Identity{
				UID:        c.UID,
				GID:        c.GID,
				Groups:     append([]uint32(nil), c.Groups...),
				Privileged: c.UID == 0,
			}
		})
	}
	return &Authorizer{
		backend: backend,
		mapper:  mapper,
		states:  make(map[uint64]*OpenState),
	}
}

// Open authorizes and performs an NFSv4.1 OPEN. On failure it returns an
// *NFSError and guarantees that any file it created during the call is
// removed again.
func (a *Authorizer) Open(ctx context.Context, req OpenRequest) (*OpenResult, error) {
	if req.Parent == nil || req.Name == "" {
		return nil, NewNFSError(NFS4ERR_INVAL, "missing parent or name")
	}
	if !validAccess(req.ShareAccess) {
		return nil, NewNFSError(NFS4ERR_INVAL, "invalid share access")
	}
	if !validDeny(req.ShareDeny) {
		return nil, NewNFSError(NFS4ERR_INVAL, "invalid share deny")
	}
	if req.Create && req.How != CreateUnchecked && req.How != CreateGuarded &&
		req.How != CreateExclusive {
		return nil, NewNFSError(NFS4ERR_NOTSUPP, "unsupported create mode")
	}

	id := a.mapper.Map(req.Cred)

	// Resolve the target, honoring the three create-mode semantics.
	node, created, retry, err := a.resolveForOpen(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// A failure after a successful create must roll the new file back.
	defer func() {
		if err != nil && created {
			_ = a.rollback(ctx, req.Parent, req.Name)
		}
	}()

	// Only a subject that may read or write the file is allowed to hold
	// share reservation state on it.
	rights, ferr := a.backend.Access(ctx, id, node)
	if ferr != nil {
		err = NewNFSError(NFS4ERR_ACCES, "access evaluation failed: "+ferr.Error())
		return nil, err
	}
	if !shareAccessGranted(req.ShareAccess, rights) {
		err = NewNFSError(NFS4ERR_ACCES, "caller lacks read/write permission for share reservation")
		return nil, err
	}

	// Commit the verifier before granting state on a freshly made exclusive
	// file, so a later retry can recognize it.
	if created && req.Create && req.How == CreateExclusive {
		if verr := a.backend.SetCreateVerifier(ctx, node, req.Verifier); verr != nil {
			err = NewNFSError(NFS4ERR_SERVERFAULT, "cannot store create verifier: "+verr.Error())
			return nil, err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if conflict := findShareConflict(a.states, node.ID(), req, id); conflict != nil {
		err = NewNFSError(NFS4ERR_SHARE_DENIED, "conflicting share reservation held by another open-owner")
		return nil, err
	}

	state := a.installStateLocked(node, req, id, created)
	return &OpenResult{Node: node, State: state, CreatedFile: created, Retry: retry}, nil
}

// Close releases a share reservation previously returned by Open. Closing
// the last reservation does not remove the underlying file.
func (a *Authorizer) Close(stateID uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.states[stateID]; !ok {
		return NewNFSError(NFS4ERR_BAD_STATEID, "unknown open state")
	}
	delete(a.states, stateID)
	return nil
}

// ActiveStates returns a snapshot of the currently granted reservations.
func (a *Authorizer) ActiveStates() []OpenState {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]OpenState, 0, len(a.states))
	for _, s := range a.states {
		out = append(out, *s)
	}
	return out
}

func (a *Authorizer) resolveForOpen(ctx context.Context, id Identity, req OpenRequest) (node Node, created, retry bool, err error) {
	existing, lerr := a.backend.Lookup(ctx, req.Parent, req.Name)
	switch {
	case lerr == nil:
		node = existing
	case lerr == ErrNotExist:
		existing = nil
	default:
		return nil, false, false, NewNFSError(NFS4ERR_SERVERFAULT, "lookup failed: "+lerr.Error())
	}

	if !req.Create {
		if existing == nil {
			return nil, false, false, NewNFSError(NFS4ERR_NOENT, "target does not exist")
		}
		return existing, false, false, nil
	}

	if existing != nil {
		switch req.How {
		case CreateGuarded:
			return nil, false, false, NewNFSError(NFS4ERR_EXIST, "guarded create target already exists")
		case CreateExclusive:
			stored, ok, verr := a.backend.CreateVerifier(ctx, existing)
			if verr != nil {
				return nil, false, false, NewNFSError(NFS4ERR_SERVERFAULT, "verifier lookup failed: "+verr.Error())
			}
			if !ok || stored != req.Verifier {
				return nil, false, false, NewNFSError(NFS4ERR_EXIST, "exclusive create verifier mismatch")
			}
			return existing, false, true, nil
		default: // UNCHECKED: open the existing object.
			return existing, false, false, nil
		}
	}

	// File does not exist: enforce create-time attribute confinement and
	// parent directory permission, then create it.
	attrs, perr := buildInitialAttrs(id, req)
	if perr != nil {
		return nil, false, false, perr
	}
	if rights, aerr := a.backend.Access(ctx, id, req.Parent); aerr != nil {
		return nil, false, false, NewNFSError(NFS4ERR_ACCES, "parent access evaluation failed: "+aerr.Error())
	} else if rights&(AccessWrite|AccessExecute) != (AccessWrite | AccessExecute) {
		return nil, false, false, NewNFSError(NFS4ERR_ACCES, "create requires write+execute on parent directory")
	}

	node, cerr := a.backend.Create(ctx, req.Parent, req.Name, attrs)
	if cerr != nil {
		return nil, false, false, NewNFSError(NFS4ERR_SERVERFAULT, "create failed: "+cerr.Error())
	}
	return node, true, false, nil
}

// buildInitialAttrs applies the rule that an unprivileged caller may only
// create a file owned by its mapped identity. Privileged callers may assign
// arbitrary owner/group. EXCLUSIVE4_1 forbids owner/modesize attributes on
// the wire per RFC 8881.
func buildInitialAttrs(id Identity, req OpenRequest) (InitialAttrs, error) {
	attrs := InitialAttrs{
		UID:  id.UID,
		GID:  id.GID,
		Mode: 0o600,
	}

	if req.How == CreateExclusive {
		// EXCLUSIVE4_1 carries only the verifier; the mode/size/owner
		// attributes are not legal on the wire.
		if req.Attrs.Owner != nil || req.Attrs.Group != nil ||
			req.Attrs.Mode != nil || req.Attrs.Size != nil {
			return InitialAttrs{}, NewNFSError(NFS4ERR_INVAL, "exclusive4_1 create must not carry owner/mode/size attributes")
		}
		return attrs, nil
	}

	if req.Attrs.Owner != nil {
		if !id.Privileged && *req.Attrs.Owner != id.UID {
			return InitialAttrs{}, NewNFSError(NFS4ERR_PERM, "cannot create file owned by another uid")
		}
		attrs.UID = *req.Attrs.Owner
	}
	if req.Attrs.Group != nil {
		if !id.Privileged && !id.IsGroup(*req.Attrs.Group) {
			return InitialAttrs{}, NewNFSError(NFS4ERR_PERM, "cannot create file with a group the caller does not belong to")
		}
		attrs.GID = *req.Attrs.Group
	}
	if req.Attrs.Mode != nil {
		attrs.Mode = *req.Attrs.Mode & 0o7777
	}
	if req.Attrs.Size != nil {
		attrs.Size = *req.Attrs.Size
	}
	return attrs, nil
}

func (a *Authorizer) rollback(ctx context.Context, parent Node, name string) error {
	return a.backend.Remove(ctx, parent, name)
}

func (a *Authorizer) installStateLocked(node Node, req OpenRequest, id Identity, created bool) *OpenState {
	// Reopening the same open-owner replaces its previous reservation.
	for sid, s := range a.states {
		if s.Node == node.ID() && s.ClientID == req.ClientID && s.OpenOwner == req.OpenOwner {
			delete(a.states, sid)
			break
		}
	}

	id2 := a.seq.Add(1)
	state := &OpenState{
		ID:          id2,
		Node:        node.ID(),
		ClientID:    req.ClientID,
		OpenOwner:   req.OpenOwner,
		Access:      req.ShareAccess,
		Deny:        req.ShareDeny,
		Identity:    id,
		CreatedFile: created,
	}
	a.states[id2] = state
	return state
}

func validAccess(access ShareAccess) bool {
	switch access {
	case ShareAccessNone, ShareAccessRead, ShareAccessWrite, ShareAccessBoth:
		return true
	default:
		return false
	}
}

func validDeny(deny ShareDeny) bool {
	switch deny {
	case ShareDenyNone, ShareDenyRead, ShareDenyWrite, ShareDenyBoth:
		return true
	default:
		return false
	}
}

// shareAccessGranted reports whether the caller's rights permit every
// requested share access mode. At least one of read/write is required.
func shareAccessGranted(access ShareAccess, rights AccessRights) bool {
	switch access {
	case ShareAccessRead:
		return rights&AccessRead != 0
	case ShareAccessWrite:
		return rights&AccessWrite != 0
	case ShareAccessBoth:
		return rights&AccessRead != 0 && rights&AccessWrite != 0
	case ShareAccessNone:
		return rights&(AccessRead|AccessWrite) != 0
	default:
		return false
	}
}

// findShareConflict implements the OPEN4_SHARE_ACCESS/OPEN4_SHARE_DENY
// conflict matrix of RFC 8881 section 9.9:
//
//	new deny READ  conflicts with existing access READ
//	new deny WRITE conflicts with existing access WRITE
//	new access READ  conflicts with existing deny READ
//	new access WRITE conflicts with existing deny WRITE.
//
// Reservations belonging to the same open-owner are excluded because they
// are replaced atomically in installStateLocked.
func findShareConflict(states map[uint64]*OpenState, node NodeID, req OpenRequest, _ Identity) *OpenState {
	for _, s := range states {
		if s.Node != node {
			continue
		}
		if s.ClientID == req.ClientID && s.OpenOwner == req.OpenOwner {
			continue
		}
		if req.ShareDeny&ShareDenyRead != 0 && s.Access&ShareAccessRead != 0 {
			return s
		}
		if req.ShareDeny&ShareDenyWrite != 0 && s.Access&ShareAccessWrite != 0 {
			return s
		}
		if req.ShareAccess&ShareAccessRead != 0 && s.Deny&ShareDenyRead != 0 {
			return s
		}
		if req.ShareAccess&ShareAccessWrite != 0 && s.Deny&ShareDenyWrite != 0 {
			return s
		}
	}
	return nil
}
