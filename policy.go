package netsec

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"slices"
	"strings"
)

// OID is the certificate policy object identifier type used by crypto/x509.
type OID = x509.OID

// Certificate is the crypto/x509 certificate type.
type Certificate = x509.Certificate

// PolicyMapping is the crypto/x509 policy mapping type.
type PolicyMapping = x509.PolicyMapping

// AnyPolicyOID is the special anyPolicy OID (2.5.29.32.0).
var AnyPolicyOID = mustOIDFromInts([]uint64{2, 5, 29, 32, 0})

// AnyPolicy is an alias for AnyPolicyOID.
var AnyPolicy = AnyPolicyOID

// anyPolicyOID is kept under the name used by crypto/x509's policy engine.
var anyPolicyOID = AnyPolicyOID

var anyPolicyKey = oidKey(AnyPolicyOID)

func mustOIDFromInts(ints []uint64) OID {
	oid, err := x509.OIDFromInts(ints)
	if err != nil {
		panic(err)
	}
	return oid
}

func mustNewOIDFromInts(ints []uint64) OID {
	return mustOIDFromInts(ints)
}

func oidKey(oid OID) string {
	der, err := oid.MarshalBinary()
	if err != nil || len(der) == 0 {
		return oid.String()
	}
	return string(der)
}

func oidFromASN1(oid asn1.ObjectIdentifier) (OID, bool) {
	parsed, err := x509.ParseOID(oid.String())
	return parsed, err == nil
}

// VerifyOptions controls certification-path policy validation. The unexported
// fields mirror crypto/x509's internal policy-test options. The exported fields
// provide the same controls to importers of this package.
type VerifyOptions struct {
	// CertificatePolicies is the initial user policy set. If it is empty, the
	// initial user policy set is anyPolicy, which accepts every valid policy.
	CertificatePolicies []OID

	// InitialPolicySet and UserPolicySet are accepted aliases for
	// CertificatePolicies.
	InitialPolicySet []OID
	UserPolicySet    []OID

	// Exported policy controls.
	InhibitPolicyMapping  bool
	RequireExplicitPolicy bool
	InhibitAnyPolicy      bool

	// NIST-style aliases for the exported policy controls.
	InitialPolicyMappingInhibit bool
	InitialExplicitPolicy       bool
	InitialAnyPolicyInhibit     bool

	// Lower-case aliases retained for compatibility with crypto/x509 test
	// helpers that exercise the internal policy engine.
	inhibitPolicyMapping  bool
	requireExplicitPolicy bool
	inhibitAnyPolicy      bool
}

// PolicyOptions is an alias for VerifyOptions.
type PolicyOptions = VerifyOptions

// Options is an alias for VerifyOptions.
type Options = VerifyOptions

func (o VerifyOptions) inhibitsPolicyMapping() bool {
	return o.InhibitPolicyMapping || o.InitialPolicyMappingInhibit || o.inhibitPolicyMapping
}

func (o VerifyOptions) requiresExplicitPolicy() bool {
	return o.RequireExplicitPolicy || o.InitialExplicitPolicy || o.requireExplicitPolicy
}

func (o VerifyOptions) inhibitsAnyPolicy() bool {
	return o.InhibitAnyPolicy || o.InitialAnyPolicyInhibit || o.inhibitAnyPolicy
}

func (o VerifyOptions) userPolicySet() []OID {
	switch {
	case len(o.CertificatePolicies) != 0:
		return o.CertificatePolicies
	case len(o.InitialPolicySet) != 0:
		return o.InitialPolicySet
	default:
		return o.UserPolicySet
	}
}

type policyGraphNode struct {
	validPolicy       OID
	expectedPolicySet []OID
	parents           map[*policyGraphNode]struct{}
	children          map[*policyGraphNode]struct{}
}

func newPolicyGraphNode(valid OID, parents []*policyGraphNode) *policyGraphNode {
	n := &policyGraphNode{
		validPolicy:       valid,
		expectedPolicySet: []OID{valid},
		parents:           make(map[*policyGraphNode]struct{}, len(parents)),
		children:          make(map[*policyGraphNode]struct{}),
	}
	for _, p := range parents {
		p.children[n] = struct{}{}
		n.parents[p] = struct{}{}
	}
	return n
}

type policyGraph struct {
	strata []map[string]*policyGraphNode
	// parentIndex maps an OID to nodes in the previous stratum whose
	// expectedPolicySet contains that OID.
	parentIndex map[string][]*policyGraphNode
	depth       int
}

func newPolicyGraph() *policyGraph {
	root := &policyGraphNode{
		validPolicy:       AnyPolicyOID,
		expectedPolicySet: []OID{AnyPolicyOID},
		parents:           map[*policyGraphNode]struct{}{},
		children:          map[*policyGraphNode]struct{}{},
	}
	return &policyGraph{
		strata: []map[string]*policyGraphNode{{anyPolicyKey: root}},
	}
}

func (pg *policyGraph) insert(n *policyGraphNode) {
	pg.strata[pg.depth][oidKey(n.validPolicy)] = n
}

func (pg *policyGraph) parentsWithExpected(expected OID) []*policyGraphNode {
	if pg.depth == 0 {
		return nil
	}
	return pg.parentIndex[oidKey(expected)]
}

func (pg *policyGraph) parentWithAnyPolicy() *policyGraphNode {
	if pg.depth == 0 {
		return nil
	}
	return pg.strata[pg.depth-1][anyPolicyKey]
}

func (pg *policyGraph) parentNodes() map[string]*policyGraphNode {
	if pg.depth == 0 {
		return nil
	}
	return pg.strata[pg.depth-1]
}

func (pg *policyGraph) leaves() map[string]*policyGraphNode {
	return pg.strata[pg.depth]
}

func (pg *policyGraph) leafWithPolicy(policy OID) *policyGraphNode {
	return pg.strata[pg.depth][oidKey(policy)]
}

func (pg *policyGraph) deleteLeaf(policy OID) {
	n := pg.strata[pg.depth][oidKey(policy)]
	if n == nil {
		return
	}
	for p := range n.parents {
		delete(p.children, n)
	}
	for c := range n.children {
		delete(c.parents, n)
	}
	delete(pg.strata[pg.depth], oidKey(policy))
}

func (pg *policyGraph) validPolicyNodes() []*policyGraphNode {
	var nodes []*policyGraphNode
	for depth := pg.depth; depth >= 0; depth-- {
		for _, n := range pg.strata[depth] {
			if n.validPolicy.Equal(AnyPolicyOID) {
				continue
			}
			if len(n.parents) != 1 {
				continue
			}
			for p := range n.parents {
				if p.validPolicy.Equal(AnyPolicyOID) {
					nodes = append(nodes, n)
				}
			}
		}
	}
	return nodes
}

func (pg *policyGraph) prune() {
	for depth := pg.depth - 1; depth > 0; depth-- {
		for _, n := range pg.strata[depth] {
			if len(n.children) != 0 {
				continue
			}
			for p := range n.parents {
				delete(p.children, n)
			}
			delete(pg.strata[depth], oidKey(n.validPolicy))
		}
	}
}

func (pg *policyGraph) incrDepth() {
	pg.parentIndex = make(map[string][]*policyGraphNode)
	for _, n := range pg.strata[pg.depth] {
		for _, expected := range n.expectedPolicySet {
			key := oidKey(expected)
			pg.parentIndex[key] = append(pg.parentIndex[key], n)
		}
	}
	pg.depth++
	pg.strata = append(pg.strata, map[string]*policyGraphNode{})
}

// Evaluation is the result of a policy validation. Policies contains the
// user-constrained valid policy set. If Valid is false, Policies is empty.
type Evaluation struct {
	Valid             bool
	Policies          []OID
	AuthorityPolicies []OID
}

// policiesValid reports whether chain satisfies the policy processing rules of
// RFC 5280 and RFC 9618. chain must be ordered leaf first and trust anchor last.
//
// This name and signature intentionally match crypto/x509's internal policy
// validation helper so path-validation code can exercise the same semantics.
func policiesValid(chain []*Certificate, opts VerifyOptions) bool {
	return evaluatePolicies(chain, opts).Valid
}

func evaluatePolicies(chain []*Certificate, opts VerifyOptions) Evaluation {
	if len(chain) == 0 {
		return Evaluation{}
	}
	if len(chain) == 1 {
		return Evaluation{Valid: true}
	}

	// n is the number of certificates in the path below the trust anchor.
	n := len(chain) - 1

	pg := newPolicyGraph()
	var inhibitAnyPolicy, explicitPolicy, policyMapping int
	if !opts.inhibitsAnyPolicy() {
		inhibitAnyPolicy = n + 1
	}
	if !opts.requiresExplicitPolicy() {
		explicitPolicy = n + 1
	}
	if !opts.inhibitsPolicyMapping() {
		policyMapping = n + 1
	}

	initialUserPolicySet := map[string]OID{}
	for _, policy := range opts.userPolicySet() {
		initialUserPolicySet[oidKey(policy)] = policy
	}
	if len(initialUserPolicySet) == 0 {
		initialUserPolicySet[anyPolicyKey] = AnyPolicyOID
	}

	for i := n - 1; i >= 0; i-- {
		cert := chain[i]
		issuedToItself := isSelfIssued(cert)

		// 6.1.3 (e), as updated by RFC 9618.
		if len(certificatePolicies(cert)) == 0 {
			pg = nil
		}

		// 6.1.3 (f), as updated by RFC 9618.
		if explicitPolicy == 0 && pg == nil {
			return Evaluation{}
		}

		if pg != nil {
			pg.incrDepth()

			certPolicySet := map[string]bool{}
			for _, policy := range certificatePolicies(cert) {
				key := oidKey(policy)
				certPolicySet[key] = true

				// anyPolicy is processed separately in step (2).
				if policy.Equal(AnyPolicyOID) {
					continue
				}

				// 6.1.3 (d) (1) (i), as updated by RFC 9618.
				parents := pg.parentsWithExpected(policy)
				if len(parents) == 0 {
					// 6.1.3 (d) (1) (ii), as updated by RFC 9618.
					if anyParent := pg.parentWithAnyPolicy(); anyParent != nil {
						parents = []*policyGraphNode{anyParent}
					}
				}
				if len(parents) != 0 {
					pg.insert(newPolicyGraphNode(policy, parents))
				}
			}

			// 6.1.3 (d) (2), as updated by RFC 9618.
			//
			// The anyPolicy assertion is only allowed to synthesize nodes while
			// the inhibitAnyPolicy counter is positive. Once the counter reaches
			// zero, an anyPolicy assertion at this depth must not create new
			// concrete policy nodes. Nodes created at earlier depths are left
			// untouched.
			if certPolicySet[anyPolicyKey] &&
				(inhibitAnyPolicy > 0 || (i > 0 && issuedToItself)) {
				type missingNode struct {
					policy  OID
					parents []*policyGraphNode
				}
				missing := map[string]*missingNode{}
				leaves := pg.leaves()
				for _, p := range pg.parentNodes() {
					for _, expected := range p.expectedPolicySet {
						key := oidKey(expected)
						if leaves[key] != nil {
							continue
						}
						m := missing[key]
						if m == nil {
							m = &missingNode{policy: expected}
							missing[key] = m
						}
						m.parents = append(m.parents, p)
					}
				}
				for _, m := range missing {
					pg.insert(newPolicyGraphNode(m.policy, m.parents))
				}
			}

			// 6.1.3 (d) (3), as updated by RFC 9618.
			pg.prune()

			if i != 0 {
				// 6.1.4 (b), as updated by RFC 9618.
				if len(cert.PolicyMappings) > 0 {
					mappings := map[string][]OID{}
					issuerPolicies := map[string]OID{}
					for _, mapping := range cert.PolicyMappings {
						if policyMapping > 0 {
							if mapping.IssuerDomainPolicy.Equal(AnyPolicyOID) ||
								mapping.SubjectDomainPolicy.Equal(AnyPolicyOID) {
								return Evaluation{}
							}
							key := oidKey(mapping.IssuerDomainPolicy)
							issuerPolicies[key] = mapping.IssuerDomainPolicy
							mappings[key] = append(mappings[key], mapping.SubjectDomainPolicy)
						} else {
							// 6.1.4 (b) (3) (i), as updated by RFC 9618.
							pg.deleteLeaf(mapping.IssuerDomainPolicy)

							// 6.1.4 (b) (3) (ii), as updated by RFC 9618.
							pg.prune()
						}
					}

					for key, subjectPolicies := range mappings {
						issuerPolicy := issuerPolicies[key]

						// 6.1.4 (b) (1), as updated by RFC 9618.
						if matching := pg.leafWithPolicy(issuerPolicy); matching != nil {
							matching.expectedPolicySet = subjectPolicies
						} else if matching := pg.leafWithPolicy(AnyPolicyOID); matching != nil {
							// 6.1.4 (b) (2), as updated by RFC 9618.
							newNode := newPolicyGraphNode(issuerPolicy, []*policyGraphNode{matching})
							newNode.expectedPolicySet = subjectPolicies
							pg.insert(newNode)
						}
					}
				}
			}
		}

		if i != 0 {
			// 6.1.4 (h)
			if !issuedToItself {
				if explicitPolicy > 0 {
					explicitPolicy--
				}
				if policyMapping > 0 {
					policyMapping--
				}
				if inhibitAnyPolicy > 0 {
					inhibitAnyPolicy--
				}
			}

			// 6.1.4 (i)
			if v, set := constraintValue(cert.RequireExplicitPolicy, cert.RequireExplicitPolicyZero); set && v < explicitPolicy {
				explicitPolicy = v
			}
			if v, set := constraintValue(cert.InhibitPolicyMapping, cert.InhibitPolicyMappingZero); set && v < policyMapping {
				policyMapping = v
			}
			// 6.1.4 (j)
			if v, set := constraintValue(cert.InhibitAnyPolicy, cert.InhibitAnyPolicyZero); set && v < inhibitAnyPolicy {
				inhibitAnyPolicy = v
			}
		}
	}

	// 6.1.5 (a)
	if explicitPolicy > 0 {
		explicitPolicy--
	}

	// 6.1.5 (b)
	if chain[0].RequireExplicitPolicyZero {
		explicitPolicy = 0
	}

	// 6.1.5 (g) (1)-(3), as updated by RFC 9618.
	var validPolicyNodeSet []*policyGraphNode
	if pg != nil {
		validPolicyNodeSet = pg.validPolicyNodes()
		if currentAny := pg.leafWithPolicy(AnyPolicyOID); currentAny != nil {
			validPolicyNodeSet = append(validPolicyNodeSet, currentAny)
		}
	}

	authorityConstrainedPolicySet := map[string]OID{}
	for _, n := range validPolicyNodeSet {
		authorityConstrainedPolicySet[oidKey(n.validPolicy)] = n.validPolicy
	}

	userConstrainedPolicySet := make(map[string]OID, len(authorityConstrainedPolicySet))
	for key, policy := range authorityConstrainedPolicySet {
		userConstrainedPolicySet[key] = policy
	}

	// 6.1.5 (g) (6), as updated by RFC 9618.
	if _, anyInitial := initialUserPolicySet[anyPolicyKey]; len(initialUserPolicySet) != 1 || !anyInitial {
		for key := range userConstrainedPolicySet {
			if _, ok := initialUserPolicySet[key]; !ok {
				delete(userConstrainedPolicySet, key)
			}
		}
		if _, ok := authorityConstrainedPolicySet[anyPolicyKey]; ok {
			for key, policy := range initialUserPolicySet {
				userConstrainedPolicySet[key] = policy
			}
		}
	}

	if explicitPolicy == 0 && len(userConstrainedPolicySet) == 0 {
		return Evaluation{}
	}

	return Evaluation{
		Valid:             true,
		Policies:          sortedOIDs(userConstrainedPolicySet),
		AuthorityPolicies: sortedOIDs(authorityConstrainedPolicySet),
	}
}

func certificatePolicies(cert *Certificate) []OID {
	if len(cert.Policies) != 0 {
		return cert.Policies
	}
	if len(cert.PolicyIdentifiers) == 0 {
		return nil
	}
	policies := make([]OID, 0, len(cert.PolicyIdentifiers))
	for _, id := range cert.PolicyIdentifiers {
		if oid, ok := oidFromASN1(id); ok {
			policies = append(policies, oid)
		}
	}
	return policies
}

func constraintValue(value int, explicitZero bool) (int, bool) {
	if value > 0 || explicitZero {
		return value, true
	}
	return 0, false
}

func isSelfIssued(cert *Certificate) bool {
	if len(cert.RawIssuer) != 0 || len(cert.RawSubject) != 0 {
		return bytes.Equal(cert.RawIssuer, cert.RawSubject)
	}
	return cert.Issuer.String() == cert.Subject.String()
}

func sortedOIDs(set map[string]OID) []OID {
	policies := make([]OID, 0, len(set))
	for _, policy := range set {
		policies = append(policies, policy)
	}
	slices.SortFunc(policies, func(a, b OID) int {
		return strings.Compare(oidKey(a), oidKey(b))
	})
	return policies
}
