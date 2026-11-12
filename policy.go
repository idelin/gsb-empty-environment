package netsec

import "fmt"

// Snapshot is a point-in-time GUAC graph slice used by the release gate.
type Snapshot struct {
	// Packages are package (coordinate) nodes keyed by a version-independent
	// normalized purl: pkg:type/namespace/name.
	Packages []PackageNode

	// Versions are package version nodes keyed by the full normalized purl
	// (version and qualifiers included).
	Versions []VersionNode

	// Attestations are SLSA/attestation edges attached to version nodes.
	Attestations []AttestationEdge

	// Dependencies are directed edges from a version node to a version node
	// it depends on.
	Dependencies []DependencyEdge
}

// PackageNode identifies a package without a version.
type PackageNode struct {
	Purl string
}

// VersionNode identifies one concrete, digest-pinned artifact.
type VersionNode struct {
	// Purl is the full package URL; version must not be omitted.
	Purl string
	// Digests maps algorithm to lower-case hex digest, e.g.
	// "sha256" -> "9f2d...".
	Digests map[string]string
}

// AttestationEdge is an attestation statement stored as a graph edge.
type AttestationEdge struct {
	// VersionPurl is the version node the edge is attached to.
	VersionPurl string
	// PredicateType is the in-toto predicate type, e.g.
	// "https://slsa.dev/provenance/v1".
	PredicateType string
	// Attestor is the identity that signed the attestation (exact string,
	// e.g. an OIDC subject). Prefixes are never accepted.
	Attestor string
	// SubjectDigests maps algorithm to the digest claimed for the subject
	// artifact. Every digest must match the version node digest exactly.
	SubjectDigests map[string]string
}

// DependencyEdge links one version node to a dependency version node.
type DependencyEdge struct {
	FromPurl string
	ToPurl   string
}

// BlacklistEntry flags a package or a specific version as vulnerable.
// Version-less entries match every version of the package.
type BlacklistEntry struct {
	Purl string
}

// Policy carries the gate configuration.
type Policy struct {
	// TrustedAttestors are the only attestor identities accepted; matching
	// is exact equality, never a prefix match.
	TrustedAttestors []string

	// VulnerabilityBlacklist rejects any dependency whose purl matches an
	// entry; entries may be version-less package coordinates.
	VulnerabilityBlacklist []BlacklistEntry
}

// Decision reports the gate result.
type Decision struct {
	Allowed bool
	// Reason is machine-readable when denied, empty when allowed.
	Reason string
	// Detail is a human-readable explanation.
	Detail string
	// Cycles lists dependency cycles detected while walking the closure;
	// they never block traversal (the walk terminates), but are surfaced
	// for visibility.
	Cycles [][]string
}

const (
	reasonInvalidInput      = "INVALID_INPUT"
	reasonVersionNotFound   = "VERSION_NOT_FOUND"
	reasonNoProvenance      = "MISSING_SLSA_PROVENANCE"
	reasonUntrustedAttestor = "UNTRUSTED_ATTESTOR"
	reasonDigestMismatch    = "SUBJECT_DIGEST_MISMATCH"
	reasonBlacklisted       = "VULNERABILITY_BLACKLIST_HIT"
)

// slsaProvenancePredicates are the accepted SLSA provenance predicate types.
var slsaProvenancePredicates = map[string]bool{
	"https://slsa.dev/provenance/v0.1": true,
	"https://slsa.dev/provenance/v0.2": true,
	"https://slsa.dev/provenance/v1":   true,
}

// Evaluate runs the release-gate policy against a graph snapshot.
//
// All purls are normalized before any comparison, so purl spelling variants
// (type/namespace case, percent-encoding case, qualifier key case and order)
// resolve to the same node, while distinct names, qualifiers or versions do
// not.
func Evaluate(artifactPurl string, snap Snapshot, pol Policy) Decision {
	target, err := parsePurl(artifactPurl, true)
	if err != nil {
		return deny(reasonInvalidInput, err.Error())
	}
	targetKey := target.String()

	versions, err := indexVersions(snap.Versions)
	if err != nil {
		return deny(reasonInvalidInput, err.Error())
	}
	if _, ok := versions[targetKey]; !ok {
		return deny(reasonVersionNotFound, "no version node in snapshot for "+targetKey)
	}

	if d := checkProvenance(targetKey, versions[targetKey], snap.Attestations, pol); !d.Allowed {
		return d
	}

	deps, err := indexDependencies(snap.Dependencies)
	if err != nil {
		return deny(reasonInvalidInput, err.Error())
	}
	blacklist, err := indexBlacklist(pol.VulnerabilityBlacklist)
	if err != nil {
		return deny(reasonInvalidInput, err.Error())
	}

	return checkDependencyClosure(targetKey, deps, blacklist)
}

func checkProvenance(targetKey string, version VersionNode, edges []AttestationEdge, pol Policy) Decision {
	trusted := make(map[string]bool, len(pol.TrustedAttestors))
	for _, identity := range pol.TrustedAttestors {
		trusted[identity] = true
	}

	var sawProvenance bool
	for _, edge := range edges {
		edgeKey, err := NormalizePurl(edge.VersionPurl)
		if err != nil {
			return deny(reasonInvalidInput, "attestation edge has invalid purl: "+err.Error())
		}
		if edgeKey != targetKey || !slsaProvenancePredicates[edge.PredicateType] {
			continue
		}
		sawProvenance = true

		// Exact identity comparison only: "evil.example.com/trusted" must
		// not match trusted "trusted.example.com" or share a prefix.
		if !trusted[edge.Attestor] {
			continue
		}
		if !digestsEqual(version.Digests, edge.SubjectDigests) {
			continue
		}
		return allow()
	}

	if !sawProvenance {
		return deny(reasonNoProvenance, "no SLSA provenance edge on "+targetKey)
	}
	// Provenance exists but none is both trusted and digest-pinned to this
	// artifact. Report the digest mismatch distinctly when a trusted
	// attestor signed for another digest, otherwise the attestor is untrusted.
	for _, edge := range edges {
		edgeKey, _ := NormalizePurl(edge.VersionPurl)
		if edgeKey == targetKey && slsaProvenancePredicates[edge.PredicateType] &&
			trusted[edge.Attestor] && !digestsEqual(version.Digests, edge.SubjectDigests) {
			return deny(reasonDigestMismatch,
				"trusted provenance subject digest does not match artifact "+targetKey)
		}
	}
	return deny(reasonUntrustedAttestor,
		"no attestation on "+targetKey+" is signed by an exact trusted identity")
}

func checkDependencyClosure(root string, deps map[string][]string, blacklist map[string]map[string]bool) Decision {
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := map[string]int{root: unvisited}
	var stack []string
	var path []string
	var cycles [][]string

	stack = append(stack, root)
	for len(stack) > 0 {
		node := stack[len(stack)-1]

		switch state[node] {
		case unvisited:
			state[node] = onStack
			path = append(path, node)

			if hit := blacklistHit(node, blacklist); hit != "" {
				return deny(reasonBlacklisted, "dependency closure hits blacklisted purl "+hit)
			}

			for _, next := range deps[node] {
				if state[next] == unvisited {
					state[next] = unvisited
					stack = append(stack, next)
				} else if state[next] == onStack {
					cycles = append(cycles, extractCycle(path, next))
				}
			}

		case onStack:
			state[node] = done
			path = path[:len(path)-1]
			stack = stack[:len(stack)-1]

		default:
			stack = stack[:len(stack)-1]
		}
	}

	return Decision{Allowed: true, Cycles: cycles}
}

func extractCycle(path []string, backTarget string) []string {
	for i, node := range path {
		if node == backTarget {
			cycle := make([]string, 0, len(path)-i+1)
			cycle = append(cycle, path[i:]...)
			cycle = append(cycle, backTarget)
			return cycle
		}
	}
	return []string{backTarget, backTarget}
}

func blacklistHit(normalizedPurl string, blacklist map[string]map[string]bool) string {
	parsed, err := parsePurl(normalizedPurl, true)
	if err != nil {
		return ""
	}
	versions, ok := blacklist[parsed.coordinates()]
	if !ok {
		return ""
	}
	if versions[""] {
		return parsed.coordinates()
	}
	if versions[normalizedPurl] {
		return normalizedPurl
	}
	return ""
}

func indexVersions(nodes []VersionNode) (map[string]VersionNode, error) {
	indexed := make(map[string]VersionNode, len(nodes))
	for _, node := range nodes {
		key, err := NormalizePurl(node.Purl)
		if err != nil {
			return nil, fmt.Errorf("version node %q: %w", node.Purl, err)
		}
		indexed[key] = node
	}
	return indexed, nil
}

func indexDependencies(edges []DependencyEdge) (map[string][]string, error) {
	indexed := make(map[string][]string)
	for _, edge := range edges {
		from, err := NormalizePurl(edge.FromPurl)
		if err != nil {
			return nil, fmt.Errorf("dependency from %q: %w", edge.FromPurl, err)
		}
		to, err := NormalizePurl(edge.ToPurl)
		if err != nil {
			return nil, fmt.Errorf("dependency to %q: %w", edge.ToPurl, err)
		}
		indexed[from] = append(indexed[from], to)
	}
	return indexed, nil
}

func indexBlacklist(entries []BlacklistEntry) (map[string]map[string]bool, error) {
	indexed := make(map[string]map[string]bool)
	for _, entry := range entries {
		parsed, err := parsePurl(entry.Purl, false)
		if err != nil {
			return nil, fmt.Errorf("blacklist purl %q: %w", entry.Purl, err)
		}
		coords := parsed.coordinates()
		if indexed[coords] == nil {
			indexed[coords] = map[string]bool{}
		}
		// Empty version means a package-level entry matching every version;
		// otherwise only the exact versioned purl is blacklisted.
		versionKey := ""
		if parsed.Version != "" {
			versionKey = parsed.String()
		}
		indexed[coords][versionKey] = true
	}
	return indexed, nil
}

// digestsEqual requires every digest claimed by the artifact to be present
// in the attestation subject with the identical algorithm and value. Extra
// subject algorithms do not weaken the check, but a missing or different
// digest fails.
func digestsEqual(artifact, subject map[string]string) bool {
	if len(artifact) == 0 {
		return false
	}
	for algo, value := range artifact {
		subjectValue, ok := subject[algo]
		if !ok || subjectValue != value {
			return false
		}
	}
	return true
}

func allow() Decision {
	return Decision{Allowed: true}
}

func deny(reason, detail string) Decision {
	return Decision{Allowed: false, Reason: reason, Detail: detail}
}
