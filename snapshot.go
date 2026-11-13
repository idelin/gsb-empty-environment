package netsec

import (
	"fmt"
	"sort"
)

// PrincipalState is the authorization state of one principal.
type PrincipalState struct {
	Active bool
	Roles  []string
	Grants map[ObjectID]OperationSet
}

// RoleState contains role membership edges and grants made to the role.
type RoleState struct {
	Parents []string
	Grants  map[ObjectID]OperationSet
}

// ObjectState is one object generation. Name is informational; identity and
// invalidation use ObjectID plus SchemaVersion.
type ObjectState struct {
	ObjectID      ObjectID
	Name          string
	SchemaVersion uint64
	RLSDigest     string
}

// Aliases make the snapshot easy to consume from catalog adapters.
type PrincipalInfo = PrincipalState
type RoleInfo = RoleState
type ObjectInfo = ObjectState

// SecuritySnapshot is an immutable-by-convention authorization snapshot. A
// snapshot is cloned whenever the host changes policy, and transactions keep
// the clone they began with.
type SecuritySnapshot struct {
	PolicyEpoch uint64
	Principals  map[PrincipalID]PrincipalState
	Roles       map[string]RoleState
	Objects     map[ObjectID]ObjectState
}

// PolicySnapshot is an alias for SecuritySnapshot.
type PolicySnapshot = SecuritySnapshot

// NewSnapshot creates an empty snapshot at the given policy epoch.
func NewSnapshot(epoch uint64) *SecuritySnapshot {
	return &SecuritySnapshot{
		PolicyEpoch: epoch,
		Principals:  make(map[PrincipalID]PrincipalState),
		Roles:       make(map[string]RoleState),
		Objects:     make(map[ObjectID]ObjectState),
	}
}

// Clone returns a deep copy that can be mutated without affecting the source
// or any transaction pinned to it.
func (s *SecuritySnapshot) Clone() *SecuritySnapshot {
	if s == nil {
		return nil
	}
	out := &SecuritySnapshot{
		PolicyEpoch: s.PolicyEpoch,
		Principals:  make(map[PrincipalID]PrincipalState, len(s.Principals)),
		Roles:       make(map[string]RoleState, len(s.Roles)),
		Objects:     make(map[ObjectID]ObjectState, len(s.Objects)),
	}
	for id, p := range s.Principals {
		out.Principals[id] = PrincipalState{
			Active: p.Active,
			Roles:  append([]string(nil), p.Roles...),
			Grants: cloneGrants(p.Grants),
		}
	}
	for name, r := range s.Roles {
		out.Roles[name] = RoleState{
			Parents: append([]string(nil), r.Parents...),
			Grants:  cloneGrants(r.Grants),
		}
	}
	for id, o := range s.Objects {
		out.Objects[id] = o
	}
	return out
}

func cloneGrants(in map[ObjectID]OperationSet) map[ObjectID]OperationSet {
	if in == nil {
		return nil
	}
	out := make(map[ObjectID]OperationSet, len(in))
	for id, ops := range in {
		out[id] = ops
	}
	return out
}

// RoleClosure computes the transitive, sorted role closure of a principal.
func (s *SecuritySnapshot) RoleClosure(pid PrincipalID) ([]string, string, error) {
	if s == nil {
		return nil, "", fmt.Errorf("%w: nil snapshot", ErrPrincipalInactive)
	}
	principal, ok := s.Principals[pid]
	if !ok || !principal.Active {
		return nil, "", fmt.Errorf("%w: %s", ErrPrincipalInactive, pid)
	}
	seen := make(map[string]struct{})
	queue := append([]string(nil), principal.Roles...)
	for len(queue) > 0 {
		role := queue[0]
		queue = queue[1:]
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		if state, ok := s.Roles[role]; ok {
			queue = append(queue, state.Parents...)
		}
	}
	roles := make([]string, 0, len(seen))
	for role := range seen {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles, DigestRoleClosure(roles), nil
}

// EffectiveGrants unions direct grants with every grant in the principal's
// role closure. Unknown roles contribute no grants.
func (s *SecuritySnapshot) EffectiveGrants(pid PrincipalID) (map[ObjectID]OperationSet, error) {
	roles, _, err := s.RoleClosure(pid)
	if err != nil {
		return nil, err
	}
	grants := cloneGrants(s.Principals[pid].Grants)
	if grants == nil {
		grants = make(map[ObjectID]OperationSet)
	}
	for _, role := range roles {
		for id, ops := range s.Roles[role].Grants {
			grants[id] |= ops
		}
	}
	return grants, nil
}

// PermissionFor returns the effective operations for a principal on one object.
func (s *SecuritySnapshot) PermissionFor(pid PrincipalID, objectID ObjectID) (OperationSet, bool, error) {
	grants, err := s.EffectiveGrants(pid)
	if err != nil {
		return 0, false, err
	}
	_, exists := s.Objects[objectID]
	return grants[objectID], exists, nil
}

// CompileInput resolves a query against this snapshot and builds the full
// authorization-relevant compile input. It fails closed on any missing object
// or insufficient privilege.
func (s *SecuritySnapshot) CompileInput(q Query, pid PrincipalID) (CompileInput, error) {
	if s == nil {
		return CompileInput{}, fmt.Errorf("%w: nil snapshot", ErrPrincipalInactive)
	}
	principal, ok := s.Principals[pid]
	if !ok || !principal.Active {
		return CompileInput{}, fmt.Errorf("%w: %s", ErrPrincipalInactive, pid)
	}
	roles, roleDigest, err := s.RoleClosure(pid)
	if err != nil {
		return CompileInput{}, err
	}
	grants, err := s.EffectiveGrants(pid)
	if err != nil {
		return CompileInput{}, err
	}

	deps := make([]ObjectDependency, 0, len(q.Objects))
	index := make(map[ObjectID]int, len(q.Objects))
	for _, ref := range q.Objects {
		obj, ok := s.Objects[ref.ObjectID]
		if !ok {
			return CompileInput{}, fmt.Errorf("%w: object %s does not exist", ErrPermissionDenied, ref.ObjectID)
		}
		granted := grants[ref.ObjectID]
		if !granted.Contains(ref.Operations) {
			return CompileInput{}, &PermissionError{
				Principal: pid,
				ObjectID:  ref.ObjectID,
				Needed:    ref.Operations,
				Granted:   granted,
				Reason:    "snapshot review",
			}
		}
		if at, ok := index[ref.ObjectID]; ok {
			deps[at].Operations |= ref.Operations
			continue
		}
		index[ref.ObjectID] = len(deps)
		deps = append(deps, ObjectDependency{
			ObjectID:      ref.ObjectID,
			SchemaVersion: obj.SchemaVersion,
			Operations:    ref.Operations,
			RLSDigest:     obj.RLSDigest,
		})
	}

	paramDigest := q.ParamTypeDigest
	if paramDigest == "" {
		paramDigest = DigestParamTypes(q.ParamTypes)
	}
	in := CompileInput{
		QueryASTDigest:    q.ASTDigest,
		ParamTypes:        append([]ParamType(nil), q.ParamTypes...),
		ParamTypeDigest:   paramDigest,
		PrincipalID:       pid,
		Roles:             roles,
		RoleClosureDigest: roleDigest,
		Objects:           deps,
		PolicyEpoch:       s.PolicyEpoch,
	}
	if err := in.Validate(); err != nil {
		return CompileInput{}, err
	}
	return in, nil
}

// Revalidate checks every plan dependency against the snapshot. A changed
// principal, role closure, policy epoch, object generation, RLS digest or
// privilege set is a miss, never an inherited allow decision.
func (s *SecuritySnapshot) Revalidate(in CompileInput) error {
	if s == nil {
		return &PlanMissError{Reason: "nil snapshot"}
	}
	norm, err := in.normalize()
	if err != nil {
		return &PlanMissError{Reason: err.Error()}
	}
	if norm.PolicyEpoch != s.PolicyEpoch {
		return &PlanMissError{Reason: fmt.Sprintf("policy epoch changed: plan %d snapshot %d", norm.PolicyEpoch, s.PolicyEpoch)}
	}
	principal, ok := s.Principals[norm.PrincipalID]
	if !ok || !principal.Active {
		return &PlanMissError{Reason: "principal is no longer active"}
	}
	roles, roleDigest, err := s.RoleClosure(norm.PrincipalID)
	if err != nil {
		return &PlanMissError{Reason: err.Error()}
	}
	if norm.RoleClosureDigest != roleDigest {
		return &PlanMissError{Reason: "role closure changed"}
	}
	if len(norm.Roles) > 0 && !sameStringSet(norm.Roles, roles) {
		return &PlanMissError{Reason: "role closure membership changed"}
	}
	grants, err := s.EffectiveGrants(norm.PrincipalID)
	if err != nil {
		return &PlanMissError{Reason: err.Error()}
	}
	for _, dep := range norm.Objects {
		obj, ok := s.Objects[dep.ObjectID]
		if !ok {
			return &PlanMissError{Reason: fmt.Sprintf("object %s no longer exists", dep.ObjectID)}
		}
		if obj.SchemaVersion != dep.SchemaVersion {
			return &PlanMissError{Reason: fmt.Sprintf("object %s schema version changed: plan %d catalog %d",
				dep.ObjectID, dep.SchemaVersion, obj.SchemaVersion)}
		}
		if obj.RLSDigest != dep.RLSDigest {
			return &PlanMissError{Reason: fmt.Sprintf("object %s row-level policy changed", dep.ObjectID)}
		}
		granted := grants[dep.ObjectID]
		if !granted.Contains(dep.Operations) {
			return &PlanMissError{Reason: fmt.Sprintf("object %s privileges no longer satisfy %s", dep.ObjectID, dep.Operations)}
		}
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := sortedStrings(a)
	bs := sortedStrings(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
