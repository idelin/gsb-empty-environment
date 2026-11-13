package netsec

import (
	"errors"
	"sort"
)

// ErrUnknownRole is returned when a subject references an unknown role.
var ErrUnknownRole = errors.New("netsec: unknown role")

// RoleGraph maps each role to the roles it transitively inherits. A subject
// is authorized if any role in its transitive closure satisfies a policy.
type RoleGraph struct {
	// parents[role] lists the directly inherited roles.
	parents map[string][]string
	// epoch is bumped on every structural change. It is folded into subject
	// classes, so a migration immediately invalidates previously built
	// partitions even if the closure digest happens to collide.
	epoch int64
}

// NewRoleGraph creates an empty role graph.
func NewRoleGraph() *RoleGraph {
	return &RoleGraph{parents: map[string][]string{}}
}

// Epoch returns the current role-graph version.
func (g *RoleGraph) Epoch() int64 { return g.epoch }

// AddRole registers a role with the given directly inherited parent roles.
// Re-adding the same role with an identical parent set is a no-op; any change
// advances the graph epoch.
func (g *RoleGraph) AddRole(role string, parents ...string) error {
	if role == "" {
		return errors.New("netsec: empty role name")
	}
	current := g.parents[role]
	desired := append([]string{}, parents...)
	if _, exists := g.parents[role]; exists && equalStrings(current, desired) {
		return nil
	}
	g.parents[role] = desired
	g.epoch++
	return nil
}

// Closure returns the transitive set of roles held by the given role,
// including the role itself, in canonical order.
func (g *RoleGraph) Closure(role string) ([]string, error) {
	if _, ok := g.parents[role]; !ok {
		return nil, ErrUnknownRole
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(r string) {
		if seen[r] {
			return
		}
		seen[r] = true
		for _, p := range g.parents[r] {
			walk(p)
		}
	}
	walk(role)
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out, nil
}

// classDigest identifies the authorization equivalence class of a role
// closure. It binds the exact closure and the graph epoch so that any role
// migration produces a new, non-matching class.
func (g *RoleGraph) classDigest(role string) (string, error) {
	closure, err := g.Closure(role)
	if err != nil {
		return "", err
	}
	return digest("roleclass", itoa(g.epoch), joinParts(closure)), nil
}

// Subject is a low-privilege querying principal.
type Subject struct {
	ID   string
	Role string
}

// SubjectClass returns the role-closure digest for the subject's role.
func (h *Host) SubjectClass(role string) (string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.roles.classDigest(role)
}

// Predicate decides whether a row is visible to a subject under a policy.
// roles is the subject's transitive role closure (computed under the fixed
// role snapshot), so inherited permissions are honored without re-reading
// mutable graph state. Implementations must be pure.
type Predicate func(row Row, subject Subject, roles []string) bool

// Policy is a row-level security policy attached to one source table.
type Policy struct {
	SourceID string
	Name     string
	// Body is the canonical description of Predicate used for the digest.
	// Two policies with the same body share a digest; editing the body (or the
	// predicate) advances the epoch.
	Body      string
	Predicate Predicate
	epoch     int64
}

// Epoch returns the policy's current epoch.
func (p *Policy) Epoch() int64 { return p.epoch }

// Digest returns the content digest of the policy body.
func (p *Policy) Digest() string {
	return digest("policy", p.SourceID, p.Name, p.Body)
}

// PolicySnapshot is the fixed policy context captured for one refresh.
type PolicySnapshot struct {
	SourceID     string
	PolicyDigest string
	Epoch        int64
	Predicate    Predicate
	Subject      Subject
	Roles        []string
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "|" + p
	}
	return out
}
