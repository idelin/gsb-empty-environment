package netsec

import "fmt"

// Revocation is an explicit REVOKE. If Immediate is true it fences plans that
// are already waiting to execute and transactions that began before the
// revoke. Without Immediate the revoke is visible only to the next
// transaction.
type Revocation struct {
	Principal  PrincipalID
	Role       string
	ObjectID   ObjectID
	Operations OperationSet
	Immediate  bool
}

// Revoke applies an explicit revocation and advances policyEpoch.
func (h *Host) Revoke(rev Revocation) error {
	if rev.Principal == "" && rev.Role == "" {
		return fmt.Errorf("netsec: revocation needs a principal or role")
	}
	ops := rev.Operations
	if ops.IsZero() {
		ops = OpsAll
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.snapshot.Clone()

	switch {
	case rev.Principal != "" && rev.Role != "":
		// REVOKE role FROM principal: remove role membership.
		principal := next.Principals[rev.Principal]
		principal.Roles = removeString(principal.Roles, rev.Role)
		next.Principals[rev.Principal] = principal
	case rev.Role != "":
		role := next.Roles[rev.Role]
		role.Grants = revokeGrants(role.Grants, rev.ObjectID, ops)
		next.Roles[rev.Role] = role
	default:
		principal := next.Principals[rev.Principal]
		principal.Grants = revokeGrants(principal.Grants, rev.ObjectID, ops)
		next.Principals[rev.Principal] = principal
	}
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	h.snapshot = next
	h.sequence++
	if rev.Immediate {
		h.fence = append(h.fence, immediateRevocation{
			Sequence:   h.sequence,
			Principal:  rev.Principal,
			ObjectID:   rev.ObjectID,
			Operations: ops,
		})
	}
	return nil
}

// RevokeImmediate is a convenience wrapper for an immediate REVOKE.
func (h *Host) RevokeImmediate(principal PrincipalID, objectID ObjectID, ops OperationSet) error {
	return h.Revoke(Revocation{
		Principal:  principal,
		ObjectID:   objectID,
		Operations: ops,
		Immediate:  true,
	})
}

// AddPrincipal adds or replaces a principal's active flag and roles.
func (h *Host) AddPrincipal(principal PrincipalID, active bool, roles ...string) error {
	if principal == "" {
		return fmt.Errorf("netsec: empty principal")
	}
	return h.Mutate(func(s *SecuritySnapshot) error {
		state := s.Principals[principal]
		state.Active = active
		state.Roles = append([]string(nil), roles...)
		if state.Grants == nil {
			state.Grants = make(map[ObjectID]OperationSet)
		}
		s.Principals[principal] = state
		return nil
	})
}

// AddRole adds a role and its parent edges.
func (h *Host) AddRole(role string, parents ...string) error {
	if role == "" {
		return fmt.Errorf("netsec: empty role")
	}
	return h.Mutate(func(s *SecuritySnapshot) error {
		state := s.Roles[role]
		state.Parents = append([]string(nil), parents...)
		if state.Grants == nil {
			state.Grants = make(map[ObjectID]OperationSet)
		}
		s.Roles[role] = state
		return nil
	})
}

// AddObject creates an object generation. Reusing the same ID after a drop
// allocates the next schema version.
func (h *Host) AddObject(id ObjectID, name string, rlsDigest string) error {
	if id == "" {
		return fmt.Errorf("netsec: empty object ID")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.snapshot.Objects[id]; exists {
		return fmt.Errorf("netsec: object %s already exists", id)
	}
	next := h.snapshot.Clone()
	version := h.nextVersion[id]
	if version == 0 {
		version = 1
	}
	next.Objects[id] = ObjectState{ObjectID: id, Name: name, SchemaVersion: version, RLSDigest: rlsDigest}
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	h.nextVersion[id] = version + 1
	h.snapshot = next
	return nil
}

// DropObject removes an object and advances its next generation.
func (h *Host) DropObject(id ObjectID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	obj, ok := h.snapshot.Objects[id]
	if !ok {
		return fmt.Errorf("netsec: object %s does not exist", id)
	}
	next := h.snapshot.Clone()
	delete(next.Objects, id)
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	h.nextVersion[id] = obj.SchemaVersion + 1
	h.snapshot = next
	return nil
}

// RecreateObject recreates an object ID under a new schema generation. The
// name may be identical to the old object; identity and invalidation do not
// depend on the name.
func (h *Host) RecreateObject(id ObjectID, name string, rlsDigest string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.snapshot.Objects[id]; exists {
		return fmt.Errorf("netsec: object %s already exists", id)
	}
	next := h.snapshot.Clone()
	version := h.nextVersion[id]
	if version == 0 {
		version = 1
	}
	next.Objects[id] = ObjectState{ObjectID: id, Name: name, SchemaVersion: version, RLSDigest: rlsDigest}
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	h.nextVersion[id] = version + 1
	h.snapshot = next
	return nil
}

// SetRLSDigest changes row-level policy for an object. The RLS digest and
// policyEpoch both change, so previously compiled plans miss.
func (h *Host) SetRLSDigest(id ObjectID, digest string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	obj, ok := h.snapshot.Objects[id]
	if !ok {
		return fmt.Errorf("netsec: object %s does not exist", id)
	}
	next := h.snapshot.Clone()
	obj.RLSDigest = digest
	next.Objects[id] = obj
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	h.snapshot = next
	return nil
}

// TightenRLS is an alias for SetRLSDigest.
func (h *Host) TightenRLS(id ObjectID, digest string) error {
	return h.SetRLSDigest(id, digest)
}

// Grant adds direct privileges for a principal.
func (h *Host) Grant(principal PrincipalID, objectID ObjectID, ops OperationSet) error {
	if principal == "" {
		return fmt.Errorf("netsec: empty principal")
	}
	return h.Mutate(func(s *SecuritySnapshot) error {
		state := s.Principals[principal]
		state.Active = true
		if state.Grants == nil {
			state.Grants = make(map[ObjectID]OperationSet)
		}
		state.Grants[objectID] |= ops
		s.Principals[principal] = state
		return nil
	})
}

// GrantRole adds privileges to a role.
func (h *Host) GrantRole(role string, objectID ObjectID, ops OperationSet) error {
	if role == "" {
		return fmt.Errorf("netsec: empty role")
	}
	return h.Mutate(func(s *SecuritySnapshot) error {
		state := s.Roles[role]
		if state.Grants == nil {
			state.Grants = make(map[ObjectID]OperationSet)
		}
		state.Grants[objectID] |= ops
		s.Roles[role] = state
		return nil
	})
}

// SetPolicyEpoch explicitly sets the global policy epoch.
func (h *Host) SetPolicyEpoch(epoch uint64) {
	h.mu.Lock()
	h.snapshot.PolicyEpoch = epoch
	h.mu.Unlock()
}

// Mutate applies a catalog change to a clone and advances the policy epoch.
// The callback must not call back into the host.
func (h *Host) Mutate(fn func(*SecuritySnapshot) error) error {
	if fn == nil {
		return fmt.Errorf("netsec: nil mutation")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.snapshot.Clone()
	if err := fn(next); err != nil {
		return err
	}
	next.PolicyEpoch = h.snapshot.PolicyEpoch + 1
	for id, obj := range next.Objects {
		if h.nextVersion[id] < obj.SchemaVersion {
			h.nextVersion[id] = obj.SchemaVersion
		}
	}
	h.snapshot = next
	return nil
}

// ReplaceSnapshot atomically installs a new snapshot. The host copies it.
func (h *Host) ReplaceSnapshot(snapshot *SecuritySnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("netsec: nil snapshot")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := snapshot.Clone()
	h.snapshot = next
	for id, obj := range next.Objects {
		if h.nextVersion[id] < obj.SchemaVersion {
			h.nextVersion[id] = obj.SchemaVersion
		}
	}
	return nil
}

func revokeGrants(in map[ObjectID]OperationSet, objectID ObjectID, ops OperationSet) map[ObjectID]OperationSet {
	if in == nil {
		return nil
	}
	if objectID == "" {
		for id, granted := range in {
			in[id] = granted &^ ops
		}
		return in
	}
	in[objectID] &^= ops
	return in
}

func removeString(in []string, value string) []string {
	out := in[:0]
	for _, item := range in {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}
