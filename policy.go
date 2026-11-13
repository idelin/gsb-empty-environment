package netsec

import (
	"bytes"
	"crypto/x509"
	"reflect"
	"time"
)

// Certificate and OID are aliases for the corresponding crypto/x509 types so
// that parsed certificate chains can be passed directly to this package.
type Certificate = x509.Certificate
type OID = x509.OID
type PolicyMapping = x509.PolicyMapping

// VerifyOptions contains the policy-related inputs for path validation.
//
// CertificatePolicies is the user-initial-policy-set. An empty set is
// equivalent to the anyPolicy OID. When RequireExplicitPolicy is true, the
// path is accepted only if at least one acceptable policy remains at the end
// of the path.
//
// The unexported fields mirror crypto/x509 and make the package useful for
// tests that exercise the policy algorithm directly.
type VerifyOptions struct {
	DNSName                   string
	Intermediates             *x509.CertPool
	Roots                     *x509.CertPool
	CurrentTime               time.Time
	KeyUsages                 []x509.ExtKeyUsage
	MaxConstraintComparisions int

	CertificatePolicies  []OID
	InitialPolicySet     []OID
	Policies             []OID
	UserInitialPolicySet []OID
	AcceptablePolicies   []OID

	RequireExplicitPolicy       bool
	InitialExplicitPolicy       bool
	ExplicitPolicy              bool
	InhibitPolicyMapping        bool
	InitialInhibitPolicyMapping bool
	InhibitAnyPolicy            bool
	InitialInhibitAnyPolicy     bool

	requireExplicitPolicy bool
	inhibitPolicyMapping  bool
	inhibitAnyPolicy      bool

	initialExplicitPolicy       bool
	initialInhibitPolicyMapping bool
	initialInhibitAnyPolicy     bool
	initialUserPolicySet        []OID
}

// PolicyOptions, PolicyVerifyOptions, PolicyValidationOptions and
// VerificationOptions are names used by callers for the same policy inputs.
type PolicyOptions = VerifyOptions
type PolicyVerifyOptions = VerifyOptions
type PolicyValidationOptions = VerifyOptions
type VerificationOptions = VerifyOptions

var anyPolicyOID = mustOIDFromInts([]uint64{2, 5, 29, 32, 0})

// AnyPolicyOID is the X.509 anyPolicy object identifier.
var AnyPolicyOID = anyPolicyOID

// OIDFromInts constructs an OID from its dotted-decimal arcs.
func OIDFromInts(arcs []uint64) (OID, error) {
	return x509.OIDFromInts(arcs)
}

// MustOIDFromInts is like OIDFromInts but panics on invalid input.
func MustOIDFromInts(arcs []uint64) OID {
	return mustOIDFromInts(arcs)
}

func mustNewOIDFromInts(arcs []uint64) OID {
	return mustOIDFromInts(arcs)
}

func mustOIDFromInts(arcs []uint64) OID {
	oid, err := x509.OIDFromInts(arcs)
	if err != nil {
		panic(err)
	}
	return oid
}

// PoliciesValid reports whether the certificate path satisfies the requested
// policy constraints. The chain must be ordered from the leaf certificate to
// the trust anchor.
//
// Options may be passed as VerifyOptions (or a pointer to one), as a
// []OID user-initial-policy-set, as a bool RequireExplicitPolicy value, or as
// a crypto/x509.VerifyOptions value. The variadic form also makes the helper
// convenient for callers that build policy inputs incrementally.
func PoliciesValid(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// VerifyPolicies is an alias for PoliciesValid.
func VerifyPolicies(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// ValidatePolicies is an alias for PoliciesValid.
func ValidatePolicies(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// CheckPolicies is an alias for PoliciesValid.
func CheckPolicies(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// PolicyPathValid is an alias for PoliciesValid.
func PolicyPathValid(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// VerifyPolicyPath is an alias for PoliciesValid.
func VerifyPolicyPath(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// ValidatePolicyPath is an alias for PoliciesValid.
func ValidatePolicyPath(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// VerifyCertificatePolicies is an alias for PoliciesValid.
func VerifyCertificatePolicies(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// ValidateCertificatePolicies is an alias for PoliciesValid.
func ValidateCertificatePolicies(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// VerifyCertificatePath is an alias for PoliciesValid.
func VerifyCertificatePath(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// ValidateCertificatePath is an alias for PoliciesValid.
func ValidateCertificatePath(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// CheckCertificatePath is an alias for PoliciesValid.
func CheckCertificatePath(chain []*Certificate, options ...any) bool {
	return policiesValid(chain, options...)
}

// PoliciesValid reports whether chain is valid for these policy options.
func (opts VerifyOptions) PoliciesValid(chain []*Certificate) bool {
	return policiesValid(chain, opts)
}

type policyGraphNode struct {
	validPolicy       OID
	expectedPolicySet []OID

	parents  map[*policyGraphNode]bool
	children map[*policyGraphNode]bool
}

func newPolicyGraphNode(valid OID, parents []*policyGraphNode) *policyGraphNode {
	node := &policyGraphNode{
		validPolicy:       valid,
		expectedPolicySet: []OID{valid},
		parents:           map[*policyGraphNode]bool{},
		children:          map[*policyGraphNode]bool{},
	}
	for _, parent := range parents {
		parent.children[node] = true
		node.parents[parent] = true
	}
	return node
}

type policyGraph struct {
	strata []map[string]*policyGraphNode
	// parentIndex maps a policy OID to nodes at strata[depth-1] whose
	// expected policy set contains that OID.
	parentIndex map[string][]*policyGraphNode
	depth       int
}

func newPolicyGraph() *policyGraph {
	root := &policyGraphNode{
		validPolicy:       anyPolicyOID,
		expectedPolicySet: []OID{anyPolicyOID},
		children:          map[*policyGraphNode]bool{},
		parents:           map[*policyGraphNode]bool{},
	}
	return &policyGraph{
		strata: []map[string]*policyGraphNode{{oidKey(anyPolicyOID): root}},
	}
}

func (pg *policyGraph) insert(node *policyGraphNode) {
	pg.strata[pg.depth][oidKey(node.validPolicy)] = node
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
	return pg.strata[pg.depth-1][oidKey(anyPolicyOID)]
}

func (pg *policyGraph) parents() []*policyGraphNode {
	if pg.depth == 0 {
		return nil
	}
	parents := make([]*policyGraphNode, 0, len(pg.strata[pg.depth-1]))
	for _, node := range pg.strata[pg.depth-1] {
		parents = append(parents, node)
	}
	return parents
}

func (pg *policyGraph) leaves() map[string]*policyGraphNode {
	return pg.strata[pg.depth]
}

func (pg *policyGraph) leafWithPolicy(policy OID) *policyGraphNode {
	return pg.strata[pg.depth][oidKey(policy)]
}

func (pg *policyGraph) deleteLeaf(policy OID) {
	node := pg.strata[pg.depth][oidKey(policy)]
	if node == nil {
		return
	}
	for parent := range node.parents {
		delete(parent.children, node)
	}
	for child := range node.children {
		delete(child.parents, node)
	}
	delete(pg.strata[pg.depth], oidKey(policy))
}

func (pg *policyGraph) validPolicyNodes() []*policyGraphNode {
	var validNodes []*policyGraphNode
	for i := pg.depth; i >= 0; i-- {
		for _, node := range pg.strata[i] {
			if node.validPolicy.Equal(anyPolicyOID) {
				continue
			}
			if len(node.parents) != 1 {
				continue
			}
			for parent := range node.parents {
				if parent.validPolicy.Equal(anyPolicyOID) {
					validNodes = append(validNodes, node)
				}
			}
		}
	}
	return validNodes
}

func (pg *policyGraph) prune() {
	for i := pg.depth - 1; i > 0; i-- {
		for key, node := range pg.strata[i] {
			if len(node.children) != 0 {
				continue
			}
			for parent := range node.parents {
				delete(parent.children, node)
			}
			delete(pg.strata[i], key)
		}
	}
}

func (pg *policyGraph) incrDepth() {
	pg.parentIndex = map[string][]*policyGraphNode{}
	for _, node := range pg.strata[pg.depth] {
		for _, expected := range node.expectedPolicySet {
			key := oidKey(expected)
			pg.parentIndex[key] = append(pg.parentIndex[key], node)
		}
	}

	pg.depth++
	pg.strata = append(pg.strata, map[string]*policyGraphNode{})
}

func policiesValid(chain []*Certificate, options ...any) bool {
	return policiesValidWithOptions(chain, normalizeOptions(options...))
}

func policiesValidWithOptions(chain []*Certificate, opts VerifyOptions) bool {
	// This implements the policy verification algorithm from RFC 5280 as
	// updated by RFC 9618. Sections 6.1.2(a), 6.1.3(d)-(f), 6.1.4(b), and
	// 6.1.5(g) are the RFC 9618 replacements.
	if len(chain) == 0 {
		return false
	}
	if len(chain) == 1 {
		return true
	}

	opts = normalizeVerifyOptions(opts)
	n := len(chain) - 1

	pg := newPolicyGraph()
	var inhibitAnyPolicy, explicitPolicy, policyMapping int
	if !opts.InhibitAnyPolicy {
		inhibitAnyPolicy = n + 1
	}
	if !opts.RequireExplicitPolicy {
		explicitPolicy = n + 1
	}
	if !opts.InhibitPolicyMapping {
		policyMapping = n + 1
	}

	initialUserPolicySet := map[string]bool{}
	for _, policy := range opts.CertificatePolicies {
		initialUserPolicySet[oidKey(policy)] = true
	}
	// An empty user-initial-policy-set is equivalent to anyPolicy.
	if len(initialUserPolicySet) == 0 {
		initialUserPolicySet[oidKey(anyPolicyOID)] = true
	}

	for i := n - 1; i >= 0; i-- {
		cert := chain[i]
		if cert == nil {
			return false
		}

		isSelfIssued := len(cert.RawSubject) > 0 && bytes.Equal(cert.RawIssuer, cert.RawSubject)

		// 6.1.3(e), as updated by RFC 9618.
		if len(cert.Policies) == 0 {
			pg = nil
		}

		// 6.1.3(f), as updated by RFC 9618.
		if explicitPolicy == 0 && pg == nil {
			return false
		}

		if pg != nil {
			pg.incrDepth()

			policies := map[string]bool{}

			// 6.1.3(d)(1), as updated by RFC 9618.
			for _, policy := range cert.Policies {
				policies[oidKey(policy)] = true

				if policy.Equal(anyPolicyOID) {
					continue
				}

				parents := pg.parentsWithExpected(policy)
				if len(parents) == 0 {
					if anyParent := pg.parentWithAnyPolicy(); anyParent != nil {
						parents = []*policyGraphNode{anyParent}
					}
				}
				if len(parents) > 0 {
					pg.insert(newPolicyGraphNode(policy, parents))
				}
			}

			// 6.1.3(d)(2), as updated by RFC 9618.
			if policies[oidKey(anyPolicyOID)] && (inhibitAnyPolicy > 0 || (n-i < n && isSelfIssued)) {
				type missingPolicy struct {
					policy  OID
					parents []*policyGraphNode
				}
				missing := map[string]*missingPolicy{}
				leaves := pg.leaves()
				for _, parent := range pg.parents() {
					for _, expected := range parent.expectedPolicySet {
						key := oidKey(expected)
						if leaves[key] != nil {
							continue
						}
						entry := missing[key]
						if entry == nil {
							entry = &missingPolicy{policy: expected}
							missing[key] = entry
						}
						entry.parents = append(entry.parents, parent)
					}
				}
				for _, entry := range missing {
					pg.insert(newPolicyGraphNode(entry.policy, entry.parents))
				}
			}

			// 6.1.3(d)(3), as updated by RFC 9618.
			pg.prune()

			if i != 0 {
				// 6.1.4(b), as updated by RFC 9618.
				if len(cert.PolicyMappings) > 0 {
					type mappedPolicies struct {
						issuer  OID
						subject []OID
					}
					mappings := map[string]*mappedPolicies{}

					for _, mapping := range cert.PolicyMappings {
						if policyMapping > 0 {
							if mapping.IssuerDomainPolicy.Equal(anyPolicyOID) ||
								mapping.SubjectDomainPolicy.Equal(anyPolicyOID) {
								return false
							}
							key := oidKey(mapping.IssuerDomainPolicy)
							entry := mappings[key]
							if entry == nil {
								entry = &mappedPolicies{issuer: mapping.IssuerDomainPolicy}
								mappings[key] = entry
							}
							entry.subject = append(entry.subject, mapping.SubjectDomainPolicy)
						} else {
							// 6.1.4(b)(3)(i) and (ii), as updated by RFC 9618.
							pg.deleteLeaf(mapping.IssuerDomainPolicy)
							pg.prune()
						}
					}

					for _, mapping := range mappings {
						// 6.1.4(b)(1), as updated by RFC 9618.
						if matching := pg.leafWithPolicy(mapping.issuer); matching != nil {
							matching.expectedPolicySet = mapping.subject
						} else if matching := pg.leafWithPolicy(anyPolicyOID); matching != nil {
							// 6.1.4(b)(2), as updated by RFC 9618.
							node := newPolicyGraphNode(mapping.issuer, []*policyGraphNode{matching})
							node.expectedPolicySet = mapping.subject
							pg.insert(node)
						}
					}
				}
			}
		}

		if i != 0 {
			// 6.1.4(h).
			if !isSelfIssued {
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

			// 6.1.4(i).
			if (cert.RequireExplicitPolicy > 0 || cert.RequireExplicitPolicyZero) &&
				cert.RequireExplicitPolicy < explicitPolicy {
				explicitPolicy = cert.RequireExplicitPolicy
			}
			if (cert.InhibitPolicyMapping > 0 || cert.InhibitPolicyMappingZero) &&
				cert.InhibitPolicyMapping < policyMapping {
				policyMapping = cert.InhibitPolicyMapping
			}
			// 6.1.4(j).
			if (cert.InhibitAnyPolicy > 0 || cert.InhibitAnyPolicyZero) &&
				cert.InhibitAnyPolicy < inhibitAnyPolicy {
				inhibitAnyPolicy = cert.InhibitAnyPolicy
			}
		}
	}

	// 6.1.5(a).
	if explicitPolicy > 0 {
		explicitPolicy--
	}

	// 6.1.5(b).
	if chain[0].RequireExplicitPolicyZero {
		explicitPolicy = 0
	}

	// 6.1.5(g)(1)-(3), as updated by RFC 9618.
	var validPolicyNodeSet []*policyGraphNode
	if pg != nil {
		validPolicyNodeSet = pg.validPolicyNodes()
		if currentAny := pg.leafWithPolicy(anyPolicyOID); currentAny != nil {
			validPolicyNodeSet = append(validPolicyNodeSet, currentAny)
		}
	}

	// 6.1.5(g)(4), as updated by RFC 9618.
	authorityConstrainedPolicySet := map[string]bool{}
	for _, node := range validPolicyNodeSet {
		authorityConstrainedPolicySet[oidKey(node.validPolicy)] = true
	}

	// 6.1.5(g)(5)-(6), as updated by RFC 9618.
	userConstrainedPolicySet := make(map[string]bool, len(authorityConstrainedPolicySet))
	for policy := range authorityConstrainedPolicySet {
		userConstrainedPolicySet[policy] = true
	}
	if len(initialUserPolicySet) != 1 || !initialUserPolicySet[oidKey(anyPolicyOID)] {
		for policy := range userConstrainedPolicySet {
			if !initialUserPolicySet[policy] {
				delete(userConstrainedPolicySet, policy)
			}
		}
		if authorityConstrainedPolicySet[oidKey(anyPolicyOID)] {
			for policy := range initialUserPolicySet {
				userConstrainedPolicySet[policy] = true
			}
		}
	}

	if explicitPolicy == 0 && len(userConstrainedPolicySet) == 0 {
		return false
	}

	return true
}

func oidKey(oid OID) string {
	return oid.String()
}

func normalizeVerifyOptions(opts VerifyOptions) VerifyOptions {
	if opts.requireExplicitPolicy || opts.initialExplicitPolicy {
		opts.RequireExplicitPolicy = true
	}
	if opts.InitialExplicitPolicy || opts.ExplicitPolicy {
		opts.RequireExplicitPolicy = true
	}
	if opts.inhibitPolicyMapping || opts.initialInhibitPolicyMapping {
		opts.InhibitPolicyMapping = true
	}
	if opts.InitialInhibitPolicyMapping {
		opts.InhibitPolicyMapping = true
	}
	if opts.inhibitAnyPolicy || opts.initialInhibitAnyPolicy {
		opts.InhibitAnyPolicy = true
	}
	if opts.InitialInhibitAnyPolicy {
		opts.InhibitAnyPolicy = true
	}
	if opts.CertificatePolicies == nil {
		switch {
		case opts.InitialPolicySet != nil:
			opts.CertificatePolicies = opts.InitialPolicySet
		case opts.Policies != nil:
			opts.CertificatePolicies = opts.Policies
		case opts.UserInitialPolicySet != nil:
			opts.CertificatePolicies = opts.UserInitialPolicySet
		case opts.AcceptablePolicies != nil:
			opts.CertificatePolicies = opts.AcceptablePolicies
		case opts.initialUserPolicySet != nil:
			opts.CertificatePolicies = opts.initialUserPolicySet
		}
	}
	return opts
}

func normalizeOptions(options ...any) VerifyOptions {
	var opts VerifyOptions
	for _, option := range options {
		switch value := option.(type) {
		case nil:
		case VerifyOptions:
			opts = mergeVerifyOptions(opts, value)
		case *VerifyOptions:
			if value != nil {
				opts = mergeVerifyOptions(opts, *value)
			}
		case x509.VerifyOptions:
			opts = mergeVerifyOptions(opts, VerifyOptions{
				DNSName:                   value.DNSName,
				Intermediates:             value.Intermediates,
				Roots:                     value.Roots,
				CurrentTime:               value.CurrentTime,
				KeyUsages:                 value.KeyUsages,
				MaxConstraintComparisions: value.MaxConstraintComparisions,
				CertificatePolicies:       value.CertificatePolicies,
			})
		case *x509.VerifyOptions:
			if value != nil {
				opts = mergeVerifyOptions(opts, VerifyOptions{
					DNSName:                   value.DNSName,
					Intermediates:             value.Intermediates,
					Roots:                     value.Roots,
					CurrentTime:               value.CurrentTime,
					KeyUsages:                 value.KeyUsages,
					MaxConstraintComparisions: value.MaxConstraintComparisions,
					CertificatePolicies:       value.CertificatePolicies,
				})
			}
		case []OID:
			opts.CertificatePolicies = value
		case OID:
			opts.CertificatePolicies = []OID{value}
		case bool:
			opts.RequireExplicitPolicy = value
		default:
			mergeReflectedOptions(&opts, option)
		}
	}
	return normalizeVerifyOptions(opts)
}

func mergeVerifyOptions(dst, src VerifyOptions) VerifyOptions {
	src = normalizeVerifyOptions(src)

	if src.CertificatePolicies != nil {
		dst.CertificatePolicies = src.CertificatePolicies
	}
	if src.DNSName != "" {
		dst.DNSName = src.DNSName
	}
	if src.Intermediates != nil {
		dst.Intermediates = src.Intermediates
	}
	if src.Roots != nil {
		dst.Roots = src.Roots
	}
	if !src.CurrentTime.IsZero() {
		dst.CurrentTime = src.CurrentTime
	}
	if src.KeyUsages != nil {
		dst.KeyUsages = src.KeyUsages
	}
	if src.MaxConstraintComparisions != 0 {
		dst.MaxConstraintComparisions = src.MaxConstraintComparisions
	}
	if src.RequireExplicitPolicy {
		dst.RequireExplicitPolicy = true
	}
	if src.InhibitPolicyMapping {
		dst.InhibitPolicyMapping = true
	}
	if src.InhibitAnyPolicy {
		dst.InhibitAnyPolicy = true
	}
	return dst
}

func mergeReflectedOptions(dst *VerifyOptions, option any) {
	value := reflect.ValueOf(option)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return
	}

	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		fieldValue := value.Field(i)
		if !fieldValue.CanInterface() {
			continue
		}

		switch field.Name {
		case "CertificatePolicies", "InitialPolicySet", "Policies", "UserInitialPolicySet", "AcceptablePolicies":
			if policies, ok := fieldValue.Interface().([]OID); ok && policies != nil {
				dst.CertificatePolicies = policies
			}
		case "RequireExplicitPolicy", "InitialExplicitPolicy", "ExplicitPolicy":
			if fieldValue.Kind() == reflect.Bool && fieldValue.Bool() {
				dst.RequireExplicitPolicy = true
			}
		case "InhibitPolicyMapping", "InitialInhibitPolicyMapping":
			if fieldValue.Kind() == reflect.Bool && fieldValue.Bool() {
				dst.InhibitPolicyMapping = true
			}
		case "InhibitAnyPolicy", "InitialInhibitAnyPolicy":
			if fieldValue.Kind() == reflect.Bool && fieldValue.Bool() {
				dst.InhibitAnyPolicy = true
			}
		}
	}
}
