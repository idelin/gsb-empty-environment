package netsec

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	oidExtensionSubjectAltName  = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidExtensionNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}
)

// GeneralSubtree is one base GeneralName in a nameConstraints extension.
// Minimum and Maximum are retained for completeness. Distance constraints
// are not used by registeredID, DNS, email, URI, or IP name matching.
type GeneralSubtree struct {
	Base    GeneralName
	Minimum int
	Maximum int
	HasMax  bool
}

// NameConstraints is the decoded RFC 5280 NameConstraints extension.
//
// The flattened fields mirror the fields exposed by crypto/x509.Certificate
// and add the registeredID lists that crypto/x509 does not model.
type NameConstraints struct {
	Critical bool

	Permitted []GeneralSubtree
	Excluded  []GeneralSubtree

	PermittedDNSDomains     []string
	ExcludedDNSDomains      []string
	PermittedEmailAddresses []string
	ExcludedEmailAddresses  []string
	PermittedURIDomains     []string
	ExcludedURIDomains      []string
	PermittedIPRanges       []*net.IPNet
	ExcludedIPRanges        []*net.IPNet
	PermittedRegisteredIDs  []asn1.ObjectIdentifier
	ExcludedRegisteredIDs   []asn1.ObjectIdentifier

	// HasUnsupported is true if a constraint form that this package does not
	// implement (otherName, x400Address, directoryName, or ediPartyName)
	// appeared in the extension. Certificate-path verification rejects such
	// constraints when the extension is critical.
	HasUnsupported bool
	Unsupported    []NameType
}

// ConstraintViolationError reports a name that an issuing CA's name
// constraints do not permit, or explicitly exclude.
type ConstraintViolationError struct {
	Cert       *x509.Certificate
	Name       GeneralName
	Constraint GeneralName
	Excluded   bool
}

func (e *ConstraintViolationError) Error() string {
	verb := "is not permitted by"
	if e.Excluded {
		verb = "is excluded by"
	}
	where := ""
	if e.Cert != nil {
		where = fmt.Sprintf(" in certificate %q", e.Cert.Subject.String())
	}
	return fmt.Sprintf("%s %q%s %s constraint %q", e.Name.Type, e.Name.String(), where, verb, e.Constraint.String())
}

// ErrConstraintViolation can be used with errors.Is.
var ErrConstraintViolation = errors.New("name constraint violation")

func (e *ConstraintViolationError) Unwrap() error { return ErrConstraintViolation }

// Policy selects name-constraint matching semantics.
type Policy int

const (
	// PolicyLegacy allows a dNSName whose entire value is a dotted-decimal
	// OID to match a registeredID constraint with that exact OID. Email and
	// URI values are still type-isolated.
	PolicyLegacy Policy = iota

	// PolicyStrict implements RFC 5280 type isolation: only a registeredID
	// GeneralName can match a registeredID constraint, and only when the OID
	// is exactly equal.
	PolicyStrict
)

// Short aliases for the two matching policies.
const (
	Legacy = PolicyLegacy
	Strict = PolicyStrict
)

// GeneralNameConstraint is an alias for GeneralSubtree.
type GeneralNameConstraint = GeneralSubtree

func (p Policy) String() string {
	switch p {
	case PolicyStrict:
		return "strict"
	case PolicyLegacy:
		return "legacy"
	default:
		return fmt.Sprintf("Policy(%d)", int(p))
	}
}

// DefaultPolicy is the compatibility policy requested by this module.
var DefaultPolicy = PolicyLegacy

// ParseNameConstraints returns the decoded nameConstraints extension of cert,
// or (nil, nil) when cert has no such extension.
func ParseNameConstraints(cert *x509.Certificate) (*NameConstraints, error) {
	if cert == nil {
		return nil, errors.New("nil certificate")
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtensionNameConstraints) {
			return parseNameConstraints(ext.Value, ext.Critical)
		}
	}
	return nil, nil
}

// ParseSubjectAltNames returns every GeneralName in cert's subjectAltName
// extension, including types that crypto/x509 does not expose (registeredID
// and otherName). If there is no subjectAltName extension it returns nil.
func ParseSubjectAltNames(cert *x509.Certificate) ([]GeneralName, error) {
	if cert == nil {
		return nil, errors.New("nil certificate")
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtensionSubjectAltName) {
			return parseGeneralNames(ext.Value)
		}
	}
	return nil, nil
}

// ParseSANs is an alias for ParseSubjectAltNames.
func ParseSANs(cert *x509.Certificate) ([]GeneralName, error) {
	return ParseSubjectAltNames(cert)
}

// ParseConstraints is an alias for ParseNameConstraints.
func ParseConstraints(cert *x509.Certificate) (*NameConstraints, error) {
	return ParseNameConstraints(cert)
}

func parseNameConstraints(der []byte, critical bool) (*NameConstraints, error) {
	tag, outer, rest, err := readDERValue(der)
	if err != nil {
		return nil, fmt.Errorf("nameConstraints: %w", err)
	}
	if tag != derSequence || len(rest) != 0 {
		return nil, fmt.Errorf("%w: nameConstraints is not a single SEQUENCE", errInvalidDER)
	}
	nc := &NameConstraints{Critical: critical}
	seenPermitted := false
	for len(outer) > 0 {
		tag, value, next, err := readDERValue(outer)
		if err != nil {
			return nil, fmt.Errorf("nameConstraints: %w", err)
		}
		outer = next
		switch tag {
		case derNameConstraintsPermitted:
			if seenPermitted {
				return nil, fmt.Errorf("%w: duplicate permittedSubtrees", errInvalidDER)
			}
			seenPermitted = true
			nc.Permitted, err = parseSubtrees(value)
			if err != nil {
				return nil, err
			}
		case derNameConstraintsExcluded:
			nc.Excluded, err = parseSubtrees(value)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: unexpected NameConstraints tag 0x%02x", errInvalidDER, tag)
		}
	}
	if len(nc.Permitted) == 0 && len(nc.Excluded) == 0 {
		return nil, errors.New("nameConstraints has no permitted or excluded subtrees")
	}
	nc.flatten()
	return nc, nil
}

func parseSubtrees(der []byte) ([]GeneralSubtree, error) {
	var out []GeneralSubtree
	for len(der) > 0 {
		tag, value, rest, err := readDERValue(der)
		if err != nil {
			return nil, fmt.Errorf("GeneralSubtree: %w", err)
		}
		if tag != derSequence {
			return nil, fmt.Errorf("%w: GeneralSubtree tag is 0x%02x", errInvalidDER, tag)
		}
		der = rest

		baseTag, baseValue, baseRest, err := readDERValue(value)
		if err != nil {
			return nil, fmt.Errorf("GeneralSubtree base: %w", err)
		}
		base, err := parseGeneralName(baseTag, baseValue)
		if err != nil {
			return nil, err
		}
		st := GeneralSubtree{Base: base}
		for len(baseRest) > 0 {
			t, v, r, err := readDERValue(baseRest)
			if err != nil {
				return nil, fmt.Errorf("GeneralSubtree distance: %w", err)
			}
			baseRest = r
			switch t {
			case derSubtreeMinimum:
				v, err := parseNonNegativeInt(v)
				if err != nil {
					return nil, fmt.Errorf("%w: invalid GeneralSubtree minimum", errInvalidDER)
				}
				st.Minimum = v
			case derSubtreeMaximum:
				v, err := parseNonNegativeInt(v)
				if err != nil {
					return nil, fmt.Errorf("%w: invalid GeneralSubtree maximum", errInvalidDER)
				}
				st.Maximum = v
				st.HasMax = true
			default:
				return nil, fmt.Errorf("%w: unexpected GeneralSubtree field 0x%02x", errInvalidDER, t)
			}
		}
		out = append(out, st)
	}
	return out, nil
}

func parseGeneralNames(der []byte) ([]GeneralName, error) {
	tag, body, rest, err := readDERValue(der)
	if err != nil {
		return nil, fmt.Errorf("subjectAltName: %w", err)
	}
	if tag != derSequence || len(rest) != 0 {
		return nil, fmt.Errorf("%w: subjectAltName is not a single SEQUENCE", errInvalidDER)
	}
	var names []GeneralName
	for len(body) > 0 {
		tag, value, next, err := readDERValue(body)
		if err != nil {
			return nil, fmt.Errorf("subjectAltName: %w", err)
		}
		body = next
		name, err := parseGeneralName(tag, value)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, errors.New("subjectAltName contains no names")
	}
	return names, nil
}

func parseNonNegativeInt(b []byte) (int, error) {
	if len(b) == 0 || len(b) > 4 {
		return 0, errors.New("invalid integer")
	}
	if b[0]&0x80 != 0 {
		return 0, errors.New("negative integer")
	}
	v := 0
	for _, c := range b {
		v = v<<8 | int(c)
	}
	return v, nil
}

func (nc *NameConstraints) flatten() {
	for _, st := range nc.Permitted {
		switch st.Base.Type {
		case NameTypeDNS:
			nc.PermittedDNSDomains = append(nc.PermittedDNSDomains, st.Base.Value)
		case NameTypeEmail:
			nc.PermittedEmailAddresses = append(nc.PermittedEmailAddresses, st.Base.Value)
		case NameTypeURI:
			nc.PermittedURIDomains = append(nc.PermittedURIDomains, st.Base.Value)
		case NameTypeIPAddress:
			if st.Base.IP != nil && st.Base.Mask != nil {
				nc.PermittedIPRanges = append(nc.PermittedIPRanges, &net.IPNet{IP: st.Base.IP, Mask: st.Base.Mask})
			}
		case NameTypeRegisteredID:
			nc.PermittedRegisteredIDs = append(nc.PermittedRegisteredIDs, st.Base.OID)
		default:
			nc.markUnsupported(st.Base.Type)
		}
	}
	for _, st := range nc.Excluded {
		switch st.Base.Type {
		case NameTypeDNS:
			nc.ExcludedDNSDomains = append(nc.ExcludedDNSDomains, st.Base.Value)
		case NameTypeEmail:
			nc.ExcludedEmailAddresses = append(nc.ExcludedEmailAddresses, st.Base.Value)
		case NameTypeURI:
			nc.ExcludedURIDomains = append(nc.ExcludedURIDomains, st.Base.Value)
		case NameTypeIPAddress:
			if st.Base.IP != nil && st.Base.Mask != nil {
				nc.ExcludedIPRanges = append(nc.ExcludedIPRanges, &net.IPNet{IP: st.Base.IP, Mask: st.Base.Mask})
			}
		case NameTypeRegisteredID:
			nc.ExcludedRegisteredIDs = append(nc.ExcludedRegisteredIDs, st.Base.OID)
		default:
			nc.markUnsupported(st.Base.Type)
		}
	}
}

func (nc *NameConstraints) markUnsupported(t NameType) {
	nc.HasUnsupported = true
	for _, existing := range nc.Unsupported {
		if existing == t {
			return
		}
	}
	nc.Unsupported = append(nc.Unsupported, t)
}

// permittedSubtrees returns the structured permitted subtrees, synthesizing
// them from the flattened fields when a caller built NameConstraints by
// setting the crypto/x509-style fields directly.
func (nc *NameConstraints) permittedSubtrees() []GeneralSubtree {
	if len(nc.Permitted) > 0 {
		return nc.Permitted
	}
	var out []GeneralSubtree
	for _, name := range nc.PermittedDNSDomains {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeDNS, Value: name}})
	}
	for _, name := range nc.PermittedEmailAddresses {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeEmail, Value: name}})
	}
	for _, name := range nc.PermittedURIDomains {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeURI, Value: name}})
	}
	for _, ip := range nc.PermittedIPRanges {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeIPAddress, IP: ip.IP, Mask: ip.Mask}})
	}
	for _, oid := range nc.PermittedRegisteredIDs {
		out = append(out, GeneralSubtree{Base: RegisteredIDName(oid)})
	}
	return out
}

func (nc *NameConstraints) excludedSubtrees() []GeneralSubtree {
	if len(nc.Excluded) > 0 {
		return nc.Excluded
	}
	var out []GeneralSubtree
	for _, name := range nc.ExcludedDNSDomains {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeDNS, Value: name}})
	}
	for _, name := range nc.ExcludedEmailAddresses {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeEmail, Value: name}})
	}
	for _, name := range nc.ExcludedURIDomains {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeURI, Value: name}})
	}
	for _, ip := range nc.ExcludedIPRanges {
		out = append(out, GeneralSubtree{Base: GeneralName{Type: NameTypeIPAddress, IP: ip.IP, Mask: ip.Mask}})
	}
	for _, oid := range nc.ExcludedRegisteredIDs {
		out = append(out, GeneralSubtree{Base: RegisteredIDName(oid)})
	}
	return out
}

// Check verifies names against nc. A name is rejected when it matches an
// excluded subtree, or when a permitted subtree of its type exists and no
// permitted subtree matches it.
func (nc *NameConstraints) Check(names []GeneralName, policy Policy) error {
	if nc == nil {
		return nil
	}
	excludedSubtrees := nc.excludedSubtrees()
	permittedSubtrees := nc.permittedSubtrees()
	for _, name := range names {
		for _, excluded := range excludedSubtrees {
			if matchGeneralName(name, excluded.Base, policy) {
				return &ConstraintViolationError{Name: name, Constraint: excluded.Base, Excluded: true}
			}
		}
	}
	for _, name := range names {
		permitted := permittedFor(permittedSubtrees, name, policy)
		if len(permitted) == 0 {
			continue
		}
		matched := false
		for _, p := range permitted {
			if matchGeneralName(name, p.Base, policy) {
				matched = true
				break
			}
		}
		if !matched {
			return &ConstraintViolationError{Name: name, Constraint: permitted[0].Base}
		}
	}
	return nil
}

func permittedFor(subtrees []GeneralSubtree, name GeneralName, policy Policy) []GeneralSubtree {
	relevant := make([]GeneralSubtree, 0, len(subtrees))
	for _, p := range subtrees {
		switch {
		case p.Base.Type == name.Type:
			relevant = append(relevant, p)
		case policy == PolicyLegacy && name.Type == NameTypeDNS && p.Base.Type == NameTypeRegisteredID:
			// Only a complete dotted-decimal value participates in the
			// compatibility path.
			if _, err := ParseOID(name.Value); err == nil {
				relevant = append(relevant, p)
			}
		}
	}
	return relevant
}

// MatchGeneralName reports whether name matches constraint under policy.
// Matching is always within the same GeneralName type, except for the
// documented PolicyLegacy DNS-to-registeredID path.
func MatchGeneralName(name, constraint GeneralName, policy Policy) bool {
	return matchGeneralName(name, constraint, policy)
}

// MatchRegisteredIDConstraint reports whether name satisfies the registeredID
// constraint oid. With no policy argument it uses DefaultPolicy.
func MatchRegisteredIDConstraint(name GeneralName, oid asn1.ObjectIdentifier, policy ...Policy) bool {
	p := DefaultPolicy
	if len(policy) > 0 {
		p = policy[0]
	}
	return matchGeneralName(name, GeneralName{Type: NameTypeRegisteredID, OID: oid}, p)
}

// RegisteredIDMatches is a convenience wrapper for a textual name and OID.
func RegisteredIDMatches(nameType NameType, name, oid string, policy ...Policy) bool {
	p := DefaultPolicy
	if len(policy) > 0 {
		p = policy[0]
	}
	constraintOID, err := ParseOID(oid)
	if err != nil {
		return false
	}
	n := GeneralName{Type: nameType, Value: name}
	switch nameType {
	case NameTypeRegisteredID:
		oidValue, err := ParseOID(name)
		if err != nil {
			return false
		}
		n.OID = oidValue
	case NameTypeEmail, NameTypeURI:
		// These forms never participate.
	}
	return matchGeneralName(n, GeneralName{Type: NameTypeRegisteredID, OID: constraintOID}, p)
}

func matchGeneralName(name, constraint GeneralName, policy Policy) bool {
	if name.Type != constraint.Type {
		if policy == PolicyLegacy && name.Type == NameTypeDNS && constraint.Type == NameTypeRegisteredID {
			oid, ok := generalNameOID(name)
			constraintOID, ok2 := generalNameOID(constraint)
			return ok && ok2 && oid.Equal(constraintOID)
		}
		return false
	}
	switch name.Type {
	case NameTypeRegisteredID:
		nameOID, ok := generalNameOID(name)
		constraintOID, ok2 := generalNameOID(constraint)
		return ok && ok2 && nameOID.Equal(constraintOID)
	case NameTypeDNS:
		ok, err := matchDomainConstraint(name.Value, constraint.Value)
		return err == nil && ok
	case NameTypeEmail:
		ok, err := matchEmailConstraint(name.Value, constraint.Value)
		return err == nil && ok
	case NameTypeURI:
		ok, err := matchURIConstraint(name.Value, constraint.Value)
		return err == nil && ok
	case NameTypeIPAddress:
		if name.IP == nil || constraint.IP == nil || constraint.Mask == nil {
			return false
		}
		ok, err := matchIPConstraint(name.IP, &net.IPNet{IP: constraint.IP, Mask: constraint.Mask})
		return err == nil && ok
	case NameTypeDirectoryName, NameTypeOtherName, NameTypeX400Address, NameTypeEDIPartyName:
		return bytes.Equal(name.Raw, constraint.Raw)
	default:
		return false
	}
}

func generalNameOID(name GeneralName) (asn1.ObjectIdentifier, bool) {
	if len(name.OID) > 0 {
		return name.OID, true
	}
	if (name.Type != NameTypeRegisteredID && name.Type != NameTypeDNS) || name.Value == "" {
		return nil, false
	}
	oid, err := ParseOID(name.Value)
	return oid, err == nil
}

func matchDomainConstraint(domain, constraint string) (bool, error) {
	if constraint == "" {
		return true, nil
	}
	domainLabels, ok := domainToReverseLabels(domain)
	if !ok {
		return false, fmt.Errorf("cannot parse domain %q", domain)
	}
	mustHaveSubdomains := constraint[0] == '.'
	if mustHaveSubdomains {
		constraint = constraint[1:]
	}
	constraintLabels, ok := domainToReverseLabels(constraint)
	if !ok {
		return false, fmt.Errorf("cannot parse domain constraint %q", constraint)
	}
	if len(domainLabels) < len(constraintLabels) ||
		(mustHaveSubdomains && len(domainLabels) == len(constraintLabels)) {
		return false, nil
	}
	for i, label := range constraintLabels {
		if !strings.EqualFold(label, domainLabels[i]) {
			return false, nil
		}
	}
	return true, nil
}

func domainToReverseLabels(domain string) ([]string, bool) {
	if domain == "" {
		return nil, false
	}
	labels := strings.Split(domain, ".")
	if len(labels) > 0 && labels[len(labels)-1] == "" {
		labels = labels[:len(labels)-1]
	}
	for _, label := range labels {
		if len(label) == 0 {
			return nil, false
		}
		for _, c := range label {
			if c < 33 || c > 126 {
				return nil, false
			}
		}
	}
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	return labels, true
}

func matchEmailConstraint(mailbox, constraint string) (bool, error) {
	local, domain, ok := splitMailbox(mailbox)
	if !ok {
		return false, fmt.Errorf("cannot parse rfc822Name %q", mailbox)
	}
	if strings.Contains(constraint, "@") {
		cl, cd, ok := splitMailbox(constraint)
		if !ok {
			return false, fmt.Errorf("cannot parse rfc822Name constraint %q", constraint)
		}
		return local == cl && strings.EqualFold(domain, cd), nil
	}
	return matchDomainConstraint(domain, constraint)
}

func splitMailbox(s string) (local, domain string, ok bool) {
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return "", "", false
	}
	if strings.ContainsAny(s[:at], "@ \t\r\n") || strings.ContainsAny(s[at+1:], "@ \t\r\n") {
		return "", "", false
	}
	return s[:at], s[at+1:], true
}

func matchURIConstraint(rawURI, constraint string) (bool, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return false, err
	}
	host := u.Host
	if host == "" {
		return false, fmt.Errorf("URI %q has no host", rawURI)
	}
	if strings.Contains(host, ":") && !strings.HasSuffix(host, "]") {
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			return false, err
		}
	}
	if ip := net.ParseIP(host); ip != nil || strings.HasPrefix(host, "[") {
		return false, fmt.Errorf("URI %q has an IP host", rawURI)
	}
	return matchDomainConstraint(host, constraint)
}

func matchIPConstraint(ip net.IP, constraint *net.IPNet) (bool, error) {
	if len(ip) != len(constraint.IP) {
		return false, nil
	}
	for i := range ip {
		if mask := constraint.Mask[i]; ip[i]&mask != constraint.IP[i]&mask {
			return false, nil
		}
	}
	return true, nil
}

// CheckNameConstraints applies the nameConstraints extension of every CA in
// chain (ordered leaf first) to every descendant certificate. It returns nil
// when the chain has no registeredID/DNS/email/URI/IP constraint violations.
//
// It accepts one []*x509.Certificate, a list of *x509.Certificate values, and
// an optional Policy. For example:
//
//	CheckNameConstraints(chain)
//	CheckNameConstraints(leaf, ca)
//	CheckNameConstraints(chain, PolicyStrict)
func CheckNameConstraints(args ...any) error {
	var chain []*x509.Certificate
	p := DefaultPolicy
	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case []*x509.Certificate:
			chain = append(chain, v...)
		case *x509.Certificate:
			chain = append(chain, v)
		case Policy:
			p = v
		case *Policy:
			if v != nil {
				p = *v
			}
		case Options:
			p = v.Policy
		default:
			return fmt.Errorf("unsupported CheckNameConstraints argument type %T", arg)
		}
	}
	if len(chain) >= 2 && !linksTo(chain[0], chain[1]) && linksTo(chain[len(chain)-1], chain[len(chain)-2]) {
		reversed := make([]*x509.Certificate, len(chain))
		for i := range chain {
			reversed[i] = chain[len(chain)-1-i]
		}
		chain = reversed
	}
	for i := 0; i < len(chain); i++ {
		for _, ca := range chain[i+1:] {
			nc, err := ParseNameConstraints(ca)
			if err != nil {
				return err
			}
			if nc == nil {
				continue
			}
			if nc.Critical && nc.HasUnsupported {
				return fmt.Errorf("critical nameConstraints in CA %q contains an unsupported GeneralName type", ca.Subject.String())
			}
			names, err := ParseSubjectAltNames(chain[i])
			if err != nil {
				return err
			}
			if err := nc.Check(names, p); err != nil {
				if ce, ok := err.(*ConstraintViolationError); ok {
					ce.Cert = chain[i]
				}
				return err
			}
		}
	}
	return nil
}

// VerifyRegisteredID checks that cert contains a name satisfying the
// registeredID constraint oid. Under PolicyLegacy a dNSName whose complete
// value is the same dotted-decimal OID also satisfies it. Email and URI names
// never do.
func VerifyRegisteredID(cert *x509.Certificate, oid asn1.ObjectIdentifier, policy ...Policy) error {
	if cert == nil {
		return errors.New("nil certificate")
	}
	p := DefaultPolicy
	if len(policy) > 0 {
		p = policy[0]
	}
	names, err := ParseSubjectAltNames(cert)
	if err != nil {
		return err
	}
	for _, name := range names {
		if matchGeneralName(name, GeneralName{Type: NameTypeRegisteredID, OID: oid}, p) {
			return nil
		}
	}
	return fmt.Errorf("certificate does not contain a name satisfying registeredID %s: %w", oid.String(), ErrConstraintViolation)
}

// CheckRegisteredID is an alias for VerifyRegisteredID.
func CheckRegisteredID(cert *x509.Certificate, oid asn1.ObjectIdentifier, policy ...Policy) error {
	return VerifyRegisteredID(cert, oid, policy...)
}

// CheckConstraints is an alias for CheckNameConstraints.
func CheckConstraints(args ...any) error {
	return CheckNameConstraints(args...)
}

// MatchConstraint is an alias for MatchGeneralName using DefaultPolicy when
// policy is omitted.
func MatchConstraint(name, constraint GeneralName, policy ...Policy) bool {
	p := DefaultPolicy
	if len(policy) > 0 {
		p = policy[0]
	}
	return matchGeneralName(name, constraint, p)
}

// MatchRegisteredID is an alias for MatchRegisteredIDConstraint.
func MatchRegisteredID(name GeneralName, oid asn1.ObjectIdentifier, policy ...Policy) bool {
	return MatchRegisteredIDConstraint(name, oid, policy...)
}
