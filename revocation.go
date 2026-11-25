package evidence

import (
	"strings"
	"time"
)

// RevocationKind identifies what a revocation record applies to.
type RevocationKind string

const (
	RevokeProbe       RevocationKind = "probe"
	RevokeRouteOrigin RevocationKind = "route_origin"
	RevokeRouteProof  RevocationKind = "route_proof"
	RevokeAttribution RevocationKind = "attribution"
)

// Revocation records when a probe, route origin, route proof or attribution
// was revoked.  RevokedAt is the effective event time, not merely the time the
// record was learned.  Re-validation at issuance time ignores records with
// RevokedAt strictly after the issuance instant.
type Revocation struct {
	Kind      RevocationKind `json:"kind"`
	Subject   string         `json:"subject"`
	RevokedAt time.Time      `json:"revoked_at"`
	Reason    string         `json:"reason,omitempty"`
}

// AppliesAt reports whether the revocation was effective at asOf.  A zero
// RevokedAt is treated as always effective (fail closed).
func (r Revocation) AppliesAt(asOf time.Time) bool {
	if strings.TrimSpace(r.Subject) == "" {
		return false
	}
	if r.RevokedAt.IsZero() {
		return true
	}
	return !r.RevokedAt.After(asOf)
}

func (r Revocation) appliesTo(probeID, origin string, routeProof, identityProof Digest,
	version, attributionID string) bool {
	subject := strings.TrimSpace(r.Subject)
	switch r.Kind {
	case RevokeProbe:
		return subject == probeID
	case RevokeRouteOrigin:
		return NormalizeRouteOrigin(subject) == NormalizeRouteOrigin(origin)
	case RevokeRouteProof:
		if !routeProof.IsZero() {
			if subject == routeProof.Hex() || subject == "sha256:"+routeProof.Hex() {
				return true
			}
		}
		// A route proof revocation may also be expressed by origin.
		return NormalizeRouteOrigin(subject) == NormalizeRouteOrigin(origin)
	case RevokeAttribution:
		if subject == attributionID {
			return true
		}
		if version != "" && (subject == version+"/"+attributionID ||
			subject == version+":"+attributionID) {
			return true
		}
		if !identityProof.IsZero() {
			return subject == identityProof.Hex() || subject == "sha256:"+identityProof.Hex()
		}
		return false
	default:
		return false
	}
}

// RevokedAt returns the first revocation effective at asOf that applies to the
// witness's selected attribution.
func revocationFor(w Witness, a Attribution, revocations []Revocation, asOf time.Time) (Revocation, bool) {
	routeProof := a.RouteProof
	if routeProof.IsZero() {
		routeProof = w.RouteProof
	}
	identityProof := a.Proof
	if identityProof.IsZero() {
		identityProof = w.IdentityProof
	}
	for _, r := range revocations {
		if !r.AppliesAt(asOf) {
			continue
		}
		if r.appliesTo(w.ProbeID, a.RouteOrigin, routeProof, identityProof,
			w.IdentityVersion, a.ID) {
			return r, true
		}
	}
	return Revocation{}, false
}
