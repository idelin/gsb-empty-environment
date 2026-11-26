package mountview

import (
	"errors"
	"sort"
)

// PolicyKey identifies a sub-interval of an immutable object.
type PolicyKey struct {
	Object   ObjectID
	Interval Interval
}

// CeilingRule is the maximum rwx mask that may ever be issued for a key.
type CeilingRule struct {
	Ceiling Perm
}

// Policy is an in-memory, monotonically tightenable protection policy.
type Policy struct {
	ceilings map[PolicyKey]CeilingRule
}

// NewPolicy creates an empty policy (deny-by-default: absent keys have no
// rights unless a host explicitly adds a ceiling).
func NewPolicy() *Policy {
	return &Policy{ceilings: map[PolicyKey]CeilingRule{}}
}

// SetCeiling installs or tightens a ceiling. Tightening can only remove
// rights; a request that would widen an existing ceiling is rejected.
func (p *Policy) SetCeiling(key PolicyKey, ceiling Perm) error {
	if existing, ok := p.ceilings[key]; ok {
		if ceiling.RelaxedBy(existing.Ceiling) {
			return errors.New("mountview: policy may only be tightened, never relaxed")
		}
	}
	p.ceilings[key] = CeilingRule{Ceiling: ceiling}
	return nil
}

// Ceiling returns the effective ceiling for an object/interval. Exact-key
// rules take precedence; otherwise the tightest overlapping rule applies, and
// with no overlap the mask is deny-all.
func (p *Policy) Ceiling(object ObjectID, iv Interval) Perm {
	if rule, ok := p.ceilings[PolicyKey{Object: object, Interval: iv}]; ok {
		return rule.Ceiling
	}
	result := Perm{Read: true, Write: true, Execute: true}
	found := false
	for key, rule := range p.ceilings {
		if key.Object != object || !key.Interval.Overlaps(iv) {
			continue
		}
		found = true
		result = intersectPerm(result, rule.Ceiling)
	}
	if !found {
		return Perm{}
	}
	return result
}

// CheckPerm reports whether effective is within every ceiling overlapping the
// requested object/interval. A may-set of masks is checked element-wise.
func (p *Policy) CheckPerm(object ObjectID, iv Interval, effective Perm) bool {
	return !effective.RelaxedBy(p.Ceiling(object, iv))
}

func intersectPerm(a, b Perm) Perm {
	return Perm{
		Read:    a.Read && b.Read,
		Write:   a.Write && b.Write,
		Execute: a.Execute && b.Execute,
	}
}

// Credential is a recoverable sub-interval credential issued only when the
// target is fully determined across all compatible gap-completion histories.
type Credential struct {
	ID       uint64
	Path     string
	Object   ObjectID
	Interval Interval
	Perm     Perm
	Revoked  bool
}

// CredentialStore issues and revokes credentials at (object, interval)
// granularity. Tightening policy never clears the whole tree: only credentials
// whose key matches the invalidation target are affected.
type CredentialStore struct {
	policy *Policy
	nextID uint64
	items  map[uint64]*Credential
	byKey  map[PolicyKey][]uint64
}

// NewCredentialStore creates a store bound to a policy.
func NewCredentialStore(policy *Policy) *CredentialStore {
	return &CredentialStore{
		policy: policy,
		items:  map[uint64]*Credential{},
		byKey:  map[PolicyKey][]uint64{},
	}
}

// Issue validates a recovery result against the policy and, only when it is
// Recoverable and within the ceiling, returns a fresh credential.
func (s *CredentialStore) Issue(res *TargetResult) (*Credential, error) {
	if res == nil {
		return nil, errors.New("mountview: nil recovery result")
	}
	if res.Verdict != Recoverable {
		return nil, errors.New("mountview: cannot issue credential for verdict " + res.Verdict.String())
	}
	if len(res.Objects) != 1 || len(res.Intervals) != 1 || len(res.Perms) != 1 {
		return nil, errors.New("mountview: indeterminate result cannot be certified")
	}
	key := PolicyKey{Object: res.Objects[0], Interval: res.Intervals[0]}
	if !s.policy.CheckPerm(key.Object, key.Interval, res.Perms[0]) {
		return nil, errors.New("mountview: effective mask relaxes the policy ceiling")
	}
	s.nextID++
	cred := &Credential{
		ID:       s.nextID,
		Path:     res.Path,
		Object:   key.Object,
		Interval: key.Interval,
		Perm:     res.Perms[0],
	}
	s.items[cred.ID] = cred
	s.byKey[key] = append(s.byKey[key], cred.ID)
	return cred, nil
}

// RevokeKey invalidates credentials bound to exactly one (object, interval)
// key. All other keys, including sibling mount branches, remain valid.
func (s *CredentialStore) RevokeKey(key PolicyKey) int {
	ids := s.byKey[key]
	n := 0
	for _, id := range ids {
		if cred := s.items[id]; cred != nil && !cred.Revoked {
			cred.Revoked = true
			n++
		}
	}
	s.byKey[key] = nil
	return n
}

// RevokeOverlapping invalidates credentials whose interval overlaps the given
// key, supporting sub-interval policy tightening without touching unrelated
// objects or distant ranges.
func (s *CredentialStore) RevokeOverlapping(key PolicyKey) int {
	n := 0
	for ck, ids := range s.byKey {
		if ck.Object != key.Object || !ck.Interval.Overlaps(key.Interval) {
			continue
		}
		for _, id := range ids {
			if cred := s.items[id]; cred != nil && !cred.Revoked {
				cred.Revoked = true
				n++
			}
		}
		s.byKey[ck] = nil
	}
	return n
}

// Active returns the currently valid credentials in stable id order.
func (s *CredentialStore) Active() []*Credential {
	ids := make([]uint64, 0, len(s.items))
	for id, cred := range s.items {
		if !cred.Revoked {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*Credential, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.items[id])
	}
	return out
}

// Get returns a credential by id.
func (s *CredentialStore) Get(id uint64) (*Credential, bool) {
	cred, ok := s.items[id]
	return cred, ok
}
