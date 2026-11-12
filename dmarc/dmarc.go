// Package dmarc resolves the DMARC policy that applies to a message sender.
//
// Policy discovery follows RFC 7489 with the following semantics:
//
//   - _dmarc.<sender domain> is queried first. If it carries no usable DMARC
//     record and the sender domain is not itself the organizational domain,
//     _dmarc.<organizational domain> is queried as a fallback.
//   - On the organizational-domain fallback, the subdomain policy (sp) is
//     applied to the sender subdomain; sp defaults to p when absent.
//   - A record must start with the version tag v=DMARC1. Multiple valid DMARC
//     records at one name are treated as no record. Duplicate tags, or a pct
//     tag that is not an integer in [0,100], make a record invalid.
//   - The caller supplies a per-message random integer r in [0,99]. The
//     declared policy is applied only when r < pct (pct defaults to 100);
//     otherwise the disposition is downgraded one level:
//     reject -> quarantine -> none.
package dmarc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Policy is the DMARC disposition applied to a message.
type Policy int

const (
	// PolicyNone means no DMARC enforcement.
	PolicyNone Policy = iota
	// PolicyQuarantine means the message is treated as suspicious.
	PolicyQuarantine
	// PolicyReject means the message is refused.
	PolicyReject
)

// String returns the DMARC tag value of the policy.
func (p Policy) String() string {
	switch p {
	case PolicyQuarantine:
		return "quarantine"
	case PolicyReject:
		return "reject"
	default:
		return "none"
	}
}

func parsePolicy(value string) (Policy, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return PolicyNone, true
	case "quarantine":
		return PolicyQuarantine, true
	case "reject":
		return PolicyReject, true
	default:
		return PolicyNone, false
	}
}

// OrgDomainFunc maps a domain to its organizational (registrable) domain,
// for example "mail.example.com" -> "example.com". Callers typically adapt
// publicsuffix.EffectiveDomainPlusOne to this signature.
type OrgDomainFunc func(domain string) string

// Resolver performs DNS TXT lookups. *net.Resolver implements Resolver.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Result is the resolved DMARC policy for one message.
type Result struct {
	// Policy is the disposition after pct sampling.
	Policy Policy
	// RecordPolicy is the policy declared by the record before sampling.
	RecordPolicy Policy
	// Pct is the pct tag of the record, defaulting to 100.
	Pct int
	// Domain is the owner of the record that was used.
	Domain string
	// FromOrgDomain is true when the record came from the
	// organizational-domain fallback rather than the sender domain.
	FromOrgDomain bool
	// RecordPresent reports whether a usable DMARC record was discovered.
	RecordPresent bool
	// SampledIn reports whether r < Pct.
	SampledIn bool
}

type dmarcRecord struct {
	p   Policy
	sp  Policy
	pct int
}

// Lookup resolves the DMARC policy applicable to senderDomain.
//
// orgDomain maps any domain to its organizational domain; r is a per-message
// random integer in [0,99] used for pct sampling. A missing DMARC record is
// not an error: the returned Result has RecordPresent set to false and a
// PolicyNone disposition. Hard DNS errors other than "no such host" are
// returned.
func Lookup(ctx context.Context, resolver Resolver, orgDomain OrgDomainFunc, senderDomain string, r int) (*Result, error) {
	if r < 0 || r > 99 {
		return nil, fmt.Errorf("dmarc: random value r must be in [0,99], got %d", r)
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if orgDomain == nil {
		return nil, errors.New("dmarc: organizational domain function is required")
	}
	sender, err := normalizeDomain(senderDomain)
	if err != nil {
		return nil, err
	}
	org, err := normalizeDomain(orgDomain(sender))
	if err != nil {
		return nil, fmt.Errorf("dmarc: invalid organizational domain: %w", err)
	}

	record, domain, fromOrg, err := discoverRecord(ctx, resolver, sender, org)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return &Result{Policy: PolicyNone, RecordPresent: false}, nil
	}

	recordPolicy := record.p
	if fromOrg {
		recordPolicy = record.sp
	}
	sampledIn := r < record.pct
	policy := recordPolicy
	if !sampledIn {
		policy = downgrade(recordPolicy)
	}
	return &Result{
		Policy:        policy,
		RecordPolicy:  recordPolicy,
		Pct:           record.pct,
		Domain:        domain,
		FromOrgDomain: fromOrg,
		RecordPresent: true,
		SampledIn:     sampledIn,
	}, nil
}

// discoverRecord implements the two-level lookup: sender domain first, then
// the organizational domain when the sender is one of its subdomains.
func discoverRecord(ctx context.Context, resolver Resolver, sender, org string) (*dmarcRecord, string, bool, error) {
	record, err := queryRecord(ctx, resolver, "_dmarc."+sender)
	if err != nil {
		return nil, "", false, err
	}
	if record != nil {
		return record, sender, false, nil
	}
	if sender == org {
		return nil, "", false, nil
	}
	if !isSubdomain(sender, org) {
		return nil, "", false, fmt.Errorf("dmarc: organizational domain %q is not a parent of sender domain %q", org, sender)
	}
	record, err = queryRecord(ctx, resolver, "_dmarc."+org)
	if err != nil {
		return nil, "", false, err
	}
	if record == nil {
		return nil, "", false, nil
	}
	return record, org, true, nil
}

// queryRecord returns the single usable DMARC record at name. An absent name,
// zero valid records, or multiple valid records all yield no record.
func queryRecord(ctx context.Context, resolver Resolver, name string) (*dmarcRecord, error) {
	txts, err := resolver.LookupTXT(ctx, name)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil, nil
		}
		return nil, err
	}
	var valid []*dmarcRecord
	for _, txt := range txts {
		if record, ok := parseRecord(txt); ok {
			valid = append(valid, record)
		}
	}
	if len(valid) != 1 {
		return nil, nil
	}
	return valid[0], nil
}

// parseRecord parses one TXT string. The version tag v=DMARC1 must be the
// first tag; duplicate tag names and an out-of-range or non-integer pct make
// the whole record invalid. Unknown tags are ignored.
func parseRecord(raw string) (*dmarcRecord, bool) {
	s := strings.TrimSpace(raw)
	if s != "v=DMARC1" && !strings.HasPrefix(s, "v=DMARC1;") {
		return nil, false
	}
	record := &dmarcRecord{pct: 100}
	seen := make(map[string]bool)
	haveP, haveSP := false, false
	for _, part := range strings.Split(s, ";") {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		name, value, found := strings.Cut(tag, "=")
		if !found {
			return nil, false
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			return nil, false
		}
		if seen[name] {
			return nil, false
		}
		seen[name] = true
		switch name {
		case "v":
			if !strings.EqualFold(value, "DMARC1") {
				return nil, false
			}
		case "p":
			policy, ok := parsePolicy(value)
			if !ok {
				return nil, false
			}
			record.p = policy
			haveP = true
		case "sp":
			policy, ok := parsePolicy(value)
			if !ok {
				return nil, false
			}
			record.sp = policy
			haveSP = true
		case "pct":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 100 {
				return nil, false
			}
			record.pct = n
		}
	}
	if !haveP {
		return nil, false
	}
	if !haveSP {
		record.sp = record.p
	}
	return record, true
}

func downgrade(p Policy) Policy {
	switch p {
	case PolicyReject:
		return PolicyQuarantine
	case PolicyQuarantine:
		return PolicyNone
	default:
		return PolicyNone
	}
}

func normalizeDomain(domain string) (string, error) {
	d := strings.TrimSpace(strings.ToLower(domain))
	if d == "" || strings.ContainsAny(d, " \t") {
		return "", fmt.Errorf("dmarc: invalid domain %q", domain)
	}
	return strings.TrimSuffix(d, "."), nil
}

func isSubdomain(sub, parent string) bool {
	return sub == parent || strings.HasSuffix(sub, "."+parent)
}
