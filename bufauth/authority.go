package bufauth

import "sync"

// knownPerms is every permission bit the authority understands.
const knownPerms = PermRead | PermWrite | PermReexport | PermMap

// Authority owns the grant tree and the transfer ledger for a buffer proxy.
// It is safe for concurrent use.
type Authority struct {
	mu        sync.RWMutex
	nextID    int64
	grants    map[int64]*Grant
	transfers map[string]*Transfer
}

// NewAuthority creates an empty authority.
func NewAuthority() *Authority {
	return &Authority{
		nextID:    1,
		grants:    make(map[int64]*Grant),
		transfers: make(map[string]*Transfer),
	}
}

// Issue creates a root grant for a buffer. Root grants are the trust anchor
// and are the only grants that may carry permissions without an upstream.
func (a *Authority) Issue(bufferID string, perms Perm) (*Grant, error) {
	if perms == PermNone {
		return nil, ErrNoPerms
	}
	if !perms.IsSubsetOf(knownPerms) {
		return nil, ErrUnknownPerm
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	grant := a.newGrantLocked(bufferID, perms, nil)
	return grant, nil
}

// Forward derives a child grant from parent. The derived permissions must be
// a subset of the parent permissions: read-only upstreams can never produce
// writable grants, and reexport or mapping rights cannot be gained. The
// parent must itself hold PermReexport, which is also enforced as a required
// (but not escalatable) bit for every non-root derivation.
func (a *Authority) Forward(parent *Grant, perms Perm) (*Grant, error) {
	if perms == PermNone {
		return nil, ErrNoPerms
	}
	if !perms.IsSubsetOf(knownPerms) {
		return nil, ErrUnknownPerm
	}
	if parent == nil {
		return nil, ErrUnknownGrant
	}

	parent.mu.RLock()
	live := parent.live
	parentPerms := parent.perms
	bufferID := parent.bufferID
	parent.mu.RUnlock()

	if !live {
		return nil, ErrGrantRevoked
	}
	if !parentPerms.Contains(PermReexport) {
		return nil, ErrReexportDenied
	}
	if !perms.IsSubsetOf(parentPerms) {
		return nil, ErrPermEscalation
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.grants[parent.id]; !ok {
		return nil, ErrUnknownGrant
	}
	child := a.newGrantLocked(bufferID, perms, parent)
	return child, nil
}

func (a *Authority) newGrantLocked(bufferID string, perms Perm, parent *Grant) *Grant {
	grant := &Grant{
		id:       a.nextID,
		bufferID: bufferID,
		perms:    perms,
		parent:   parent,
		live:     true,
	}
	a.nextID++
	a.grants[grant.id] = grant
	if parent != nil {
		parent.children = append(parent.children, grant)
	}
	return grant
}

// Grant looks up a grant by id.
func (a *Authority) Grant(id int64) (*Grant, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	grant, ok := a.grants[id]
	return grant, ok
}

// Check verifies that a live grant holds every requested permission.
func (a *Authority) Check(grant *Grant, want Perm) error {
	if grant == nil {
		return ErrUnknownGrant
	}
	a.mu.RLock()
	_, known := a.grants[grant.id]
	a.mu.RUnlock()
	if !known {
		return ErrUnknownGrant
	}
	grant.mu.RLock()
	defer grant.mu.RUnlock()
	if !grant.live {
		return ErrGrantRevoked
	}
	if !grant.perms.Contains(want) {
		return ErrPermEscalation
	}
	return nil
}

// RevokeReport describes the effects of one revocation.
type RevokeReport struct {
	// RootID is the grant whose revocation triggered the cascade.
	RootID int64
	// RevokedIDs contains the root and every descendant grant id, in
	// pre-order traversal.
	RevokedIDs []int64
	// ReclaimedTokens lists transfers that were waiting to be claimed.
	ReclaimedTokens []string
	// ClosedFDs lists file descriptors the authority closed while reclaiming
	// transfers.
	ClosedFDs []int
}

// Revoke invalidates grant and every derived grant beneath it, then reclaims
// every pending transfer reachable from the revoked subtree. It is safe to
// call repeatedly; later calls return an empty report.
func (a *Authority) Revoke(grant *Grant) (*RevokeReport, error) {
	if grant == nil {
		return nil, ErrUnknownGrant
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.grants[grant.id]; !ok {
		return nil, ErrUnknownGrant
	}

	report := &RevokeReport{RootID: grant.id}
	revokedRoots := make(map[*Grant]bool)
	grant.subtree(func(node *Grant) {
		node.mu.RLock()
		live := node.live
		node.mu.RUnlock()
		if live {
			node.markRevoked()
			report.RevokedIDs = append(report.RevokedIDs, node.id)
			revokedRoots[node] = true
		}
	})
	if len(report.RevokedIDs) == 0 {
		return report, nil
	}

	for token, transfer := range a.transfers {
		if !transfer.isPending() {
			continue
		}
		if !revokedRoots[transfer.grant] {
			continue
		}
		transfer.reclaim()
		report.ReclaimedTokens = append(report.ReclaimedTokens, token)
		report.ClosedFDs = append(report.ClosedFDs, transfer.fd)
		delete(a.transfers, token)
	}
	return report, nil
}
