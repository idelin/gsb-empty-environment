package delegation

import "time"

// GrantRef uniquely identifies one generation of a grant.
// Re-issuing a grant under the same ID produces a new generation,
// so children always pin the exact generation they delegate from.
type GrantRef struct {
	ID         string
	Generation uint64
}

func (r GrantRef) String() string {
	return r.ID + "#" + uitoa(r.Generation)
}

// Grant is a single authorization in the delegation graph.
// A grant with no Parents is a root authorization; otherwise every
// parent must exist, be valid, and strictly dominate the child in
// actions, resources and validity period (attenuation).
type Grant struct {
	ID         string
	Generation uint64
	Issuer     string
	Subject    string
	Actions    []string
	Resources  []string
	NotBefore  time.Time
	NotAfter   time.Time
	Parents    []GrantRef
	// RevokedAt is nil while the grant is active. Once set, the grant
	// (and therefore its whole descendant subtree) is invalid for any
	// query time >= RevokedAt. History is never rewritten, so queries
	// before RevokedAt still evaluate against the pre-revocation state.
	RevokedAt *time.Time
}

// Ref returns the identity of this grant generation.
func (g *Grant) Ref() GrantRef {
	return GrantRef{ID: g.ID, Generation: g.Generation}
}

// revokedAt reports whether the grant is revoked at time t.
func (g *Grant) revokedAt(t time.Time) bool {
	return g.RevokedAt != nil && !g.RevokedAt.After(t)
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
