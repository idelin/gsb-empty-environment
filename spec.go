package handoff

import (
	"fmt"
	"net/netip"
)

// Spec describes one partial handoff and its reachability constraints.
type Spec struct {
	PlanID string
	// Domain is the small verification universe, for example 10.0.0.0/24.
	Domain netip.Prefix
	// Egresses that independently observe updates.
	Egresses []EgressID
	// OldTenant and NewTenant are the two exchanging tenants.
	OldTenant TenantID
	NewTenant TenantID
	// Transfer is the address set moving from OldTenant to NewTenant.
	Transfer []netip.Prefix
	// Critical prefixes must retain an unambiguous authorized forward result.
	Critical []netip.Prefix
	// InitialClaims are installed at every egress unless InitialViews is set.
	InitialClaims []Claim
	// InitialViews optionally gives egress-specific starting RIBs.
	InitialViews map[EgressID][]Claim
	// InitialAuthz is ground truth before the cut.
	InitialAuthz *Authorization
	// Window is the default per-egress bounded reorder window. Window=1 means
	// strict per-egress order; Window=2 permits one overtake.
	Window int
	// Windows overrides Window for one egress.
	Windows map[EgressID]int
	// Policy is the admission policy used by certificates and the controller.
	Policy Policy
}

func (s *Spec) validate() error {
	if s.PlanID == "" {
		return fmt.Errorf("%w: PlanID is required", ErrInvalidSpec)
	}
	if !s.Domain.IsValid() {
		return fmt.Errorf("%w: Domain is invalid", ErrInvalidSpec)
	}
	if len(s.Egresses) == 0 {
		return fmt.Errorf("%w: at least one egress is required", ErrInvalidSpec)
	}
	seenEgress := make(map[EgressID]struct{}, len(s.Egresses))
	for _, e := range s.Egresses {
		if e == "" {
			return fmt.Errorf("%w: empty egress id", ErrInvalidSpec)
		}
		if _, ok := seenEgress[e]; ok {
			return fmt.Errorf("%w: duplicate egress %q", ErrInvalidSpec, e)
		}
		seenEgress[e] = struct{}{}
	}
	if s.OldTenant == "" || s.NewTenant == "" || s.OldTenant == s.NewTenant {
		return fmt.Errorf("%w: distinct OldTenant and NewTenant are required", ErrInvalidSpec)
	}
	if len(s.Transfer) == 0 {
		return fmt.Errorf("%w: at least one Transfer prefix is required", ErrInvalidSpec)
	}
	for _, p := range s.Transfer {
		if !p.IsValid() || !PrefixesContain(s.Domain, p) {
			return fmt.Errorf("%w: transfer prefix %s invalid or outside domain", ErrInvalidSpec, p)
		}
	}
	for _, p := range s.Critical {
		if !p.IsValid() || !PrefixesContain(s.Domain, p) {
			return fmt.Errorf("%w: critical prefix %s invalid or outside domain", ErrInvalidSpec, p)
		}
	}
	for _, c := range s.InitialClaims {
		if !c.Prefix.IsValid() || !PrefixesContain(s.Domain, c.Prefix) {
			return fmt.Errorf("%w: initial claim %+v outside domain", ErrInvalidSpec, c)
		}
	}
	for e, claims := range s.InitialViews {
		if _, ok := seenEgress[e]; !ok {
			return fmt.Errorf("%w: InitialViews has unknown egress %q", ErrInvalidSpec, e)
		}
		for _, c := range claims {
			if !c.Prefix.IsValid() || !PrefixesContain(s.Domain, c.Prefix) {
				return fmt.Errorf("%w: initial claim for %s outside domain", ErrInvalidSpec, e)
			}
		}
	}
	if s.Window < 0 {
		return fmt.Errorf("%w: Window cannot be negative", ErrInvalidSpec)
	}
	for e, w := range s.Windows {
		if _, ok := seenEgress[e]; !ok {
			return fmt.Errorf("%w: window for unknown egress %q", ErrInvalidSpec, e)
		}
		if w < 0 {
			return fmt.Errorf("%w: window for %s cannot be negative", ErrInvalidSpec, e)
		}
	}
	return nil
}

func (s *Spec) windowFor(e EgressID) int {
	if w, ok := s.Windows[e]; ok && w > 0 {
		return w
	}
	if s.Window > 0 {
		return s.Window
	}
	return 1
}

func (s *Spec) initialClaimsFor(e EgressID) []Claim {
	if s.InitialViews != nil {
		if claims, ok := s.InitialViews[e]; ok {
			return claims
		}
	}
	return s.InitialClaims
}

func (s *Spec) policy() Policy {
	// Zero Policy currently means the safe default because its only bool is
	// false; callers that want strict mode set it explicitly via NewPlanner.
	return s.Policy
}
