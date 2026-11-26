package crossmesh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// DelegationEdge is one cross-signing/issuing delegation from Issuer to
// Subject. Its authority is the exact three-dimensional product:
//
//	scope(prefixes minus excludedSubtrees) x actions x [NotBefore, NotAfter)
//
// constrained by MaxPathLen, the number of further delegation hops this edge
// may underwrite. A value of k means "after traversing this edge, at most k
// more edges may follow", matching X.509 pathLenConstraint hop accounting.
type DelegationEdge struct {
	ID               string
	Issuer           string
	Subject          string
	AllowedPrefixes  []Name
	ExcludedSubtrees []Name
	Actions          []string
	MaxPathLen       int
	NotBefore        int64
	NotAfter         int64
}

// Graph is the immutable description of a trust network. TrustedRoots are the
// only nodes whose authority needs no supporting chain.
type Graph struct {
	Nodes        []string
	Edges        []DelegationEdge
	TrustedRoots []string
}

// TimeWindow is one atomic validity interval, half-open [From, To).
type TimeWindow struct {
	From int64
	To   int64
}

// Slice is one retained three-dimensional, disjoint authorization slice.
// Scopes, Actions and Window are combined by product ("and"), never by taking
// unions across different chains per dimension.
type Slice struct {
	Cells   []ScopeCell
	Actions []string
	Window  TimeWindow
}

// NodeReport describes the post-revocation state of one affected certificate.
type NodeReport struct {
	Node     string
	Retained []Slice
	// Lost lists slices that were supported before the batch but have no
	// acyclic trusted-root chain afterwards.
	Lost              []Slice
	StillHasAuthority bool
}

// RevocationReport is the deterministic output of one CAS batch.
type RevocationReport struct {
	// DigestBefore/DigestAfter are signature-digest freezes of the full edge
	// set, enabling host-level compare-and-swap semantics.
	DigestBefore string
	DigestAfter  string
	Applied      bool
	// ConflictEdgeID is set on a failed CAS; the graph is left untouched.
	ConflictEdgeID string
	Reports        []NodeReport
	// LostSupport lists nodes that hold no authorized voxel at all afterwards.
	LostSupport []string
}

// canonicalEdge is the digest-frozen wire form of an edge.
type canonicalEdge struct {
	ID               string   `json:"id"`
	Issuer           string   `json:"issuer"`
	Subject          string   `json:"subject"`
	AllowedPrefixes  []Name   `json:"allow"`
	ExcludedSubtrees []Name   `json:"exclude"`
	Actions          []string `json:"actions"`
	MaxPathLen       int      `json:"maxPathLen"`
	NotBefore        int64    `json:"nbf"`
	NotAfter         int64    `json:"naf"`
}

// freezeDigest computes a content digest over the complete edge set. Edges are
// canonicalized deterministically so the digest depends only on semantics.
func freezeDigest(edges []DelegationEdge) string {
	ce := make([]canonicalEdge, 0, len(edges))
	for _, e := range edges {
		ce = append(ce, canonicalize(e))
	}
	sortCanonical(ce)
	buf, _ := json.Marshal(ce)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func canonicalize(e DelegationEdge) canonicalEdge {
	allow := append([]Name(nil), e.AllowedPrefixes...)
	exclude := append([]Name(nil), e.ExcludedSubtrees...)
	actions := append([]string(nil), e.Actions...)
	sortNames(allow)
	sortNames(exclude)
	sortStrings(actions)
	return canonicalEdge{
		ID:               e.ID,
		Issuer:           e.Issuer,
		Subject:          e.Subject,
		AllowedPrefixes:  allow,
		ExcludedSubtrees: exclude,
		Actions:          actions,
		MaxPathLen:       e.MaxPathLen,
		NotBefore:        e.NotBefore,
		NotAfter:         e.NotAfter,
	}
}

func sortNames(ns []Name) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Kind != ns[j].Kind {
			return ns[i].Kind < ns[j].Kind
		}
		return ns[i].Value < ns[j].Value
	})
}

func sortStrings(ss []string) {
	sort.Strings(ss)
}

func sortCanonical(ce []canonicalEdge) {
	sort.Slice(ce, func(i, j int) bool { return ce[i].ID < ce[j].ID })
}
