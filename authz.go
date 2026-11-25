package handoff

import (
	"math/big"
	"net/netip"
	"sort"
)

// TenantID identifies an authorized routing tenant.
type TenantID string

// Authorization is the ground-truth grant set at one logical cut/epoch.
type Authorization struct {
	bits   int
	grants map[TenantID]*RangeSet
}

// NewAuthorization creates an empty authorization oracle.
func NewAuthorization() *Authorization {
	return &Authorization{grants: make(map[TenantID]*RangeSet)}
}

// Grant authorizes tenant on p.
func (a *Authorization) Grant(tenant TenantID, p netip.Prefix) *Authorization {
	if a.grants == nil {
		a.grants = make(map[TenantID]*RangeSet)
	}
	bits := prefixBits(p)
	if a.bits == 0 {
		a.bits = bits
	}
	set := a.grants[tenant]
	if set == nil {
		set = NewRangeSet(bits)
		a.grants[tenant] = set
	}
	set.AddPrefix(p)
	return a
}

// Revoke removes any grant of tenant on p.
func (a *Authorization) Revoke(tenant TenantID, p netip.Prefix) *Authorization {
	if set := a.grants[tenant]; set != nil {
		set.RemovePrefix(p)
	}
	return a
}

// Authorized reports whether tenant may receive traffic to addr.
func (a *Authorization) Authorized(tenant TenantID, addr netip.Addr) bool {
	if a == nil {
		return false
	}
	set := a.grants[tenant]
	return set != nil && set.ContainsAddr(addr)
}

// AuthorizedPrefix reports whether tenant is authorized for every address in p.
func (a *Authorization) AuthorizedPrefix(tenant TenantID, p netip.Prefix) bool {
	if a == nil {
		return false
	}
	set := a.grants[tenant]
	return set != nil && set.ContainsPrefix(p)
}

// Clone returns a deep copy suitable for exhaustive state search.
func (a *Authorization) Clone() *Authorization {
	if a == nil {
		return nil
	}
	out := NewAuthorization()
	out.bits = a.bits
	for tenant, set := range a.grants {
		copied := NewRangeSet(set.bits)
		copied.ranges = append([]ipRange(nil), set.ranges...)
		for i := range copied.ranges {
			copied.ranges[i].lo = new(big.Int).Set(set.ranges[i].lo)
			copied.ranges[i].hi = new(big.Int).Set(set.ranges[i].hi)
		}
		out.grants[tenant] = copied
	}
	return out
}

// Tenants returns tenants with non-empty grant sets.
func (a *Authorization) Tenants() []TenantID {
	if a == nil {
		return nil
	}
	out := make([]TenantID, 0, len(a.grants))
	for t, rs := range a.grants {
		if rs != nil && len(rs.ranges) > 0 {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GrantedPrefixes returns canonical CIDRs currently granted to tenant.
func (a *Authorization) GrantedPrefixes(tenant TenantID) []netip.Prefix {
	if a == nil {
		return nil
	}
	set := a.grants[tenant]
	if set == nil {
		return nil
	}
	return set.ToPrefixes()
}
