package netsec

import (
	"testing"
	"time"
)

const (
	rootPurl   = "pkg:golang/github.com/acme/app@v2.0.0"
	rootDigest = "1111111111111111111111111111111111111111111111111111111111111111"
	trustedID  = "https://accounts.google.com/expected-build-identity"
	slsaV1     = "https://slsa.dev/provenance/v1"
	directDep  = "pkg:golang/github.com/acme/lib@v1.0.0"
	transDep   = "pkg:golang/github.com/acme/util@v0.9.0"
)

func goodSnapshot() Snapshot {
	return Snapshot{
		Packages: []PackageNode{
			{Purl: "pkg:golang/github.com/acme/app"},
		},
		Versions: []VersionNode{
			{Purl: rootPurl, Digests: map[string]string{"sha256": rootDigest}},
			{Purl: directDep, Digests: map[string]string{"sha256": "2222"}},
			{Purl: transDep, Digests: map[string]string{"sha256": "3333"}},
		},
		Attestations: []AttestationEdge{
			{
				VersionPurl:    rootPurl,
				PredicateType:  slsaV1,
				Attestor:       trustedID,
				SubjectDigests: map[string]string{"sha256": rootDigest},
			},
		},
		Dependencies: []DependencyEdge{
			{FromPurl: rootPurl, ToPurl: directDep},
			{FromPurl: directDep, ToPurl: transDep},
		},
	}
}

func goodPolicy() Policy {
	return Policy{TrustedAttestors: []string{trustedID}}
}

func TestEvaluateHappyPathAllows(t *testing.T) {
	decision := Evaluate(rootPurl, goodSnapshot(), goodPolicy())
	if !decision.Allowed {
		t.Fatalf("expected allow, got %+v", decision)
	}
}

// Spelling variants of the same purl (type/namespace case, qualifier key
// case and order, percent-encoding case) must resolve to the same nodes.
func TestEvaluatePurlCaseVariantStillMatches(t *testing.T) {
	variant := "pkg:GOLANG/GITHUB.COM/acme/app@v2.0.0?OS=linux&arch=ARM64"
	snap := goodSnapshot()
	canonical := "pkg:golang/github.com/acme/app@v2.0.0?arch=ARM64&os=linux"
	snap.Versions[0].Purl = canonical
	snap.Attestations[0].VersionPurl = canonical
	snap.Dependencies[0].FromPurl = canonical

	decision := Evaluate(variant, snap, goodPolicy())
	if !decision.Allowed {
		t.Fatalf("purl spelling variant should match canonical node, got %+v", decision)
	}
}

// A same-name package from a different source (different qualifier/version)
// must not borrow the real artifact's provenance.
func TestEvaluateSameNameDifferentSourceNoBorrowedProvenance(t *testing.T) {
	snap := goodSnapshot()
	decision := Evaluate("pkg:golang/github.com/acme/app@v2.0.0?repo=attacker-fork", snap, goodPolicy())
	if decision.Allowed || decision.Reason != reasonVersionNotFound {
		t.Fatalf("attacker fork purl must not find the real version node, got %+v", decision)
	}
}

// Attestor identities compare by exact equality; prefix lookalikes and
// sub-domain impostors must be rejected.
func TestEvaluateAttestorPrefixImpostorRejected(t *testing.T) {
	impostors := []string{
		trustedID + "/evil",
		"https://accounts.google.com/expected-build-identity-evil",
		"https://accounts.google.com.evil.com/expected-build-identity",
		"",
	}
	for _, impostor := range impostors {
		snap := goodSnapshot()
		snap.Attestations[0].Attestor = impostor
		decision := Evaluate(rootPurl, snap, goodPolicy())
		if decision.Allowed || decision.Reason != reasonUntrustedAttestor {
			t.Errorf("attestor %q must not be treated as trusted, got %+v", impostor, decision)
		}
	}
}

// A trusted-looking attestation pinned to a different subject digest proves
// nothing for this artifact: same-name different-source forgery rejected.
func TestEvaluateDigestMismatchRejected(t *testing.T) {
	snap := goodSnapshot()
	snap.Attestations[0].SubjectDigests = map[string]string{
		"sha256": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}
	decision := Evaluate(rootPurl, snap, goodPolicy())
	if decision.Allowed || decision.Reason != reasonDigestMismatch {
		t.Fatalf("different subject digest must deny, got %+v", decision)
	}
}

func TestEvaluateMissingDigestClaimsRejected(t *testing.T) {
	snap := goodSnapshot()
	snap.Attestations[0].SubjectDigests = map[string]string{"sha512": rootDigest}
	decision := Evaluate(rootPurl, snap, goodPolicy())
	if decision.Allowed || decision.Reason != reasonDigestMismatch {
		t.Fatalf("missing claimed algorithm must deny, got %+v", decision)
	}
}

// Any node in the transitive dependency closure hitting the vulnerability
// blacklist denies, including deep transitive dependencies.
func TestEvaluateTransitiveDependencyBlacklistRejects(t *testing.T) {
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{{Purl: transDep}}

	decision := Evaluate(rootPurl, goodSnapshot(), pol)
	if decision.Allowed || decision.Reason != reasonBlacklisted {
		t.Fatalf("transitive blacklist hit must deny, got %+v", decision)
	}
}

// Blacklist lookups normalize purls too, so spelling variants cannot bypass.
func TestEvaluateBlacklistSpellingVariantStillHits(t *testing.T) {
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{
		{Purl: "pkg:GOLANG/GITHUB.COM/acme/util@v0.9.0"},
	}
	decision := Evaluate(rootPurl, goodSnapshot(), pol)
	if decision.Allowed || decision.Reason != reasonBlacklisted {
		t.Fatalf("normalized blacklist variant must still hit, got %+v", decision)
	}
}

// A version-less blacklist entry matches every version of the package.
func TestEvaluateVersionlessBlacklistMatchesAllVersions(t *testing.T) {
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{
		{Purl: "pkg:golang/github.com/acme/util"},
	}
	decision := Evaluate(rootPurl, goodSnapshot(), pol)
	if decision.Allowed || decision.Reason != reasonBlacklisted {
		t.Fatalf("version-less blacklist entry must match, got %+v", decision)
	}
}

// A different version on the blacklist must not blacklist the used version.
func TestEvaluateBlacklistedOtherVersionAllows(t *testing.T) {
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{
		{Purl: "pkg:golang/github.com/acme/util@v9.9.9"},
	}
	decision := Evaluate(rootPurl, goodSnapshot(), pol)
	if !decision.Allowed {
		t.Fatalf("non-matching blacklist version must allow, got %+v", decision)
	}
}

// Dependency cycles must be reported but must never make traversal loop
// forever: the walk terminates and still evaluates every reachable node.
func TestEvaluateDependencyCycleTerminatesAndReports(t *testing.T) {
	snap := goodSnapshot()
	snap.Dependencies = append(snap.Dependencies, DependencyEdge{
		FromPurl: transDep, ToPurl: directDep,
	})

	done := make(chan Decision, 1)
	go func() { done <- Evaluate(rootPurl, snap, goodPolicy()) }()

	select {
	case decision := <-done:
		if !decision.Allowed {
			t.Fatalf("cycle alone must not deny, got %+v", decision)
		}
		if len(decision.Cycles) == 0 {
			t.Fatal("expected at least one reported cycle")
		}
		found := false
		for _, cycle := range decision.Cycles {
			if len(cycle) >= 2 && cycle[0] == directDep {
				found = true
			}
		}
		if !found {
			t.Fatalf("cycle should start at %q, got %+v", directDep, decision.Cycles)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dependency walk did not terminate on cyclic graph")
	}
}

// Blacklist check still works inside a cyclic graph (no infinite loop and
// no missed hit).
func TestEvaluateBlacklistHitWithinCycle(t *testing.T) {
	snap := goodSnapshot()
	snap.Dependencies = append(snap.Dependencies, DependencyEdge{
		FromPurl: transDep, ToPurl: directDep,
	})
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{{Purl: transDep}}

	done := make(chan Decision, 1)
	go func() { done <- Evaluate(rootPurl, snap, pol) }()

	select {
	case decision := <-done:
		if decision.Allowed || decision.Reason != reasonBlacklisted {
			t.Fatalf("blacklist hit inside cycle must deny, got %+v", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dependency walk did not terminate on cyclic graph")
	}
}

func TestEvaluateRejectsInvalidInputs(t *testing.T) {
	cases := map[string]string{
		"empty purl":         "",
		"missing version":    "pkg:golang/github.com/acme/app",
		"bad percent escape": "pkg:golang/github.com/acme/ap%2p@v1",
	}
	for name, purl := range cases {
		decision := Evaluate(purl, goodSnapshot(), goodPolicy())
		if decision.Allowed || decision.Reason != reasonInvalidInput {
			t.Errorf("%s: expected invalid input denial, got %+v", name, decision)
		}
	}
}

func TestEvaluateRequiresSlsaProvenancePredicate(t *testing.T) {
	snap := goodSnapshot()
	snap.Attestations[0].PredicateType = "https://example.com/non-slsa/v1"
	decision := Evaluate(rootPurl, snap, goodPolicy())
	if decision.Allowed || decision.Reason != reasonNoProvenance {
		t.Fatalf("non-SLSA predicate must deny, got %+v", decision)
	}
}

func TestEvaluateRootItselfBlacklistedRejects(t *testing.T) {
	pol := goodPolicy()
	pol.VulnerabilityBlacklist = []BlacklistEntry{{Purl: rootPurl}}
	decision := Evaluate(rootPurl, goodSnapshot(), pol)
	if decision.Allowed || decision.Reason != reasonBlacklisted {
		t.Fatalf("blacklisted root must deny, got %+v", decision)
	}
}
