package handoff

import (
	"fmt"
	"net/netip"
	"sort"
)

// Outcome is the admission decision for one address at one egress.
type Outcome string

const (
	// OutcomeForward means exactly one authorized tenant may receive traffic.
	OutcomeForward Outcome = "forward"
	// OutcomeConflict means equal-length prefixes identify different tenants.
	OutcomeConflict Outcome = "equal_length_conflict"
	// OutcomeUnauthorized means the longest candidate is not authorized and
	// policy does not permit falling back to a shorter prefix.
	OutcomeUnauthorized Outcome = "unauthorized"
	// OutcomeNoRoute means no visible claim covers the address.
	OutcomeNoRoute Outcome = "no_route"
)

// Policy controls admission behaviour after raw longest-prefix selection.
type Policy struct {
	// NoFallbackOnUnauthorized disables removing an unauthorized longer candidate
	// and evaluating the next shorter visible prefix. This is the admission
	// behaviour that lets a pre-positioned new prefix remain hidden behind an
	// still-authorized old aggregate.
	NoFallbackOnUnauthorized bool
}

// DefaultPolicy returns the conservative reachability-preserving policy.
func DefaultPolicy() Policy { return Policy{} }

// StrictPolicy never falls back after an unauthorized longest match.
func StrictPolicy() Policy { return Policy{NoFallbackOnUnauthorized: true} }

// View is one egress-local visible RIB version.
type View struct {
	Egress  EgressID
	Version uint64
	Claims  map[Digest]Claim
	Applied map[Digest]uint64
}

// NewView creates an empty view for egress.
func NewView(egress EgressID, initial ...Claim) *View {
	v := &View{
		Egress:  egress,
		Claims:  make(map[Digest]Claim),
		Applied: make(map[Digest]uint64),
	}
	for _, c := range initial {
		c = c.Canonical()
		v.Claims[c.Digest()] = c
	}
	return v
}

// Clone returns an independent view used by exhaustive interleaving search.
func (v *View) Clone() *View {
	out := NewView(v.Egress)
	out.Version = v.Version
	for d, c := range v.Claims {
		out.Claims[d] = c
	}
	for d, ver := range v.Applied {
		out.Applied[d] = ver
	}
	return out
}

// Apply applies an exact statement atomically. Replayed statements are no-ops.
// It returns true when this copy first applied the statement.
func (v *View) Apply(s Statement) (bool, error) {
	d := s.Digest()
	if _, ok := v.Applied[d]; ok {
		return false, nil
	}
	for _, w := range s.Withdraws {
		delete(v.Claims, w)
	}
	for _, c := range s.Announces {
		c = c.Canonical()
		v.Claims[c.Digest()] = c
	}
	v.Version++
	v.Applied[d] = v.Version
	return true, nil
}

// RawMatch describes the ordinary longest-prefix result before authorization.
type RawMatch struct {
	Found    bool
	Length   int
	Tenants  []TenantID
	Claims   []Claim
	Conflict bool
}

// SelectRaw performs longest-prefix matching and equal-length conflict
// detection without consulting authorization.
func SelectRaw(view *View, addr netip.Addr) RawMatch {
	byLength := make(map[int][]Claim)
	for _, c := range view.Claims {
		if c.Covers(addr) {
			byLength[c.Prefix.Bits()] = append(byLength[c.Prefix.Bits()], c)
		}
	}
	if len(byLength) == 0 {
		return RawMatch{}
	}
	lengths := make([]int, 0, len(byLength))
	for l := range byLength {
		lengths = append(lengths, l)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lengths)))

	l := lengths[0]
	claims := byLength[l]
	tenantSet := make(map[TenantID]struct{})
	for _, c := range claims {
		tenantSet[c.Tenant] = struct{}{}
	}
	tenants := make([]TenantID, 0, len(tenantSet))
	for t := range tenantSet {
		tenants = append(tenants, t)
	}
	sort.Slice(tenants, func(i, j int) bool { return tenants[i] < tenants[j] })
	return RawMatch{
		Found:    true,
		Length:   l,
		Tenants:  tenants,
		Claims:   claims,
		Conflict: len(tenants) > 1,
	}
}

// Decision is an independently explainable egress admission result.
type Decision struct {
	Egress          EgressID
	Address         netip.Addr
	Outcome         Outcome
	Tenant          TenantID
	Prefix          netip.Prefix
	ClaimDigest     Digest
	Length          int
	ConflictTenants []TenantID
	Skipped         []SkippedCandidate
	LevelClaims     []Claim
	Raw             RawMatch
}

// SkippedCandidate records why a longer candidate was not used.
type SkippedCandidate struct {
	Tenant TenantID
	Prefix netip.Prefix
	Reason string
}

// Forward reports whether traffic may be sent.
func (d Decision) Forward() bool { return d.Outcome == OutcomeForward }

// Safe reports whether the decision can cause no unauthorized delivery.
func (d Decision) Safe() bool {
	switch d.Outcome {
	case OutcomeForward:
		return d.Tenant != ""
	default:
		return true
	}
}

// Select evaluates visible routes against actual authorization.
func Select(view *View, addr netip.Addr, authz *Authorization, policy Policy) Decision {
	d := Decision{Egress: view.Egress, Address: addr, Raw: SelectRaw(view, addr)}

	byLength := make(map[int][]Claim)
	for _, c := range view.Claims {
		if c.Covers(addr) {
			byLength[c.Prefix.Bits()] = append(byLength[c.Prefix.Bits()], c)
		}
	}
	lengths := make([]int, 0, len(byLength))
	for l := range byLength {
		lengths = append(lengths, l)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lengths)))

	for _, length := range lengths {
		claims := byLength[length]
		tenantSet := make(map[TenantID]struct{})
		for _, c := range claims {
			tenantSet[c.Tenant] = struct{}{}
		}
		tenants := make([]TenantID, 0, len(tenantSet))
		for t := range tenantSet {
			tenants = append(tenants, t)
		}
		if len(tenants) > 1 {
			d.Outcome = OutcomeConflict
			d.Length = length
			d.ConflictTenants = tenants
			d.LevelClaims = append([]Claim(nil), claims...)
			return d
		}
		tenant := tenants[0]
		claim := canonicalClaimForTenant(claims, tenant)
		d.LevelClaims = append([]Claim(nil), claims...)
		if authz.Authorized(tenant, addr) {
			d.Outcome = OutcomeForward
			d.Tenant = tenant
			d.Prefix = claim.Prefix
			d.ClaimDigest = claim.Digest()
			d.Length = length
			return d
		}
		d.Skipped = append(d.Skipped, SkippedCandidate{
			Tenant: tenant,
			Prefix: claim.Prefix,
			Reason: "tenant not authorized at cut/epoch",
		})
		if policy.NoFallbackOnUnauthorized {
			d.Outcome = OutcomeUnauthorized
			d.Tenant = tenant
			d.Prefix = claim.Prefix
			d.Length = length
			return d
		}
	}

	d.Outcome = OutcomeNoRoute
	return d
}

func canonicalClaimForTenant(claims []Claim, tenant TenantID) Claim {
	var best *Claim
	for i := range claims {
		c := claims[i].Canonical()
		if c.Tenant == tenant {
			cp := c
			if best == nil || c.Digest().String() < best.Digest().String() {
				best = &cp
			}
		}
	}
	if best != nil {
		return *best
	}
	return Claim{Tenant: tenant}
}

// RequireAuthorizedForward is a helper used by certificates and tests.
func RequireAuthorizedForward(d Decision, authz *Authorization) error {
	if !d.Forward() {
		return fmt.Errorf("not forwarding: %s", d.Outcome)
	}
	if !authz.Authorized(d.Tenant, d.Address) {
		return fmt.Errorf("forward decision to %s is unauthorized", d.Tenant)
	}
	return nil
}
