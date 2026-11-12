package netsec

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// CriticalFlag is the CAA critical flag bit (the most significant bit).
	CriticalFlag uint8 = 1 << 7

	TagIssue     = "issue"
	TagIssueWild = "issuewild"
	TagIODef     = "iodef"
)

var (
	// ErrInvalidDomain is returned when a requested name or CNAME target is not
	// a usable DNS name.
	ErrInvalidDomain = errors.New("netsec: invalid domain name")
	// ErrInvalidIssuer is returned when the CA identifier is empty or malformed.
	ErrInvalidIssuer = errors.New("netsec: invalid issuer domain name")
	// ErrCNAMEChain is returned for a cyclic or excessively long CNAME chain.
	ErrCNAMEChain = errors.New("netsec: CNAME chain is cyclic or too long")
	// ErrUnrecognizedCritical is returned when a relevant CAA RRset contains an
	// unrecognized property with the critical flag set.
	ErrUnrecognizedCritical = errors.New("netsec: unrecognized critical CAA property")
	// ErrNotAuthorized is returned when the applicable CAA policy does not list
	// the issuing CA.
	ErrNotAuthorized = errors.New("netsec: CAA policy does not authorize issuance")
	// ErrNilResolver is returned when no DNS resolver was supplied.
	ErrNilResolver = errors.New("netsec: nil CAA resolver")
)

// CAARecord is a decoded CAA resource record. Flags is the raw CAA flags
// octet, and Tag and Value are the decoded property tag and value.
type CAARecord struct {
	Flags uint8
	Tag   string
	Value string
}

// Record is an alias for CAARecord.
type Record = CAARecord

// Resolver supplies the DNS primitives needed for RFC 8659 processing.
//
// LookupCAA must return only the CAA RRset owned by fqdn; it must not chase
// CNAMEs. LookupCNAME returns the single canonical target for fqdn, or an
// empty string when fqdn is not an alias. Both methods should return an empty
// result, rather than a not-found error, for an empty or nonexistent answer.
// The checker performs alias chasing itself so that it can enforce a cycle
// limit and preserve the RFC 8659 tree-climbing behavior.
type Resolver interface {
	LookupCAA(ctx context.Context, fqdn string) ([]CAARecord, error)
	LookupCNAME(ctx context.Context, fqdn string) (string, error)
}

// LookupResult describes the Relevant RRset selected by RFC 8659.
type LookupResult struct {
	// Found reports whether a non-empty CAA RRset was found.
	Found bool
	// Domain is the DNS name that owned the selected RRset. When the answer
	// came through a CNAME, Domain is the canonical target that supplied it.
	Domain string
	// Records is the complete selected RRset, including non-issue properties.
	Records []CAARecord
	// Wildcard reports whether this lookup was for a wildcard certificate.
	Wildcard bool
}

// Lookup selects the Relevant RRset for domain according to RFC 8659.
//
// A CNAME chain is followed before moving to the parent name. If neither the
// requested name nor its CNAME targets has CAA records, the algorithm removes
// the leftmost label and repeats. Processing stops at the first non-empty
// RRset and never queries the DNS root.
//
// A wildcard request is checked at the name with the wildcard label removed;
// the "*." prefix is accepted in domain and also sets wildcard.
func Lookup(ctx context.Context, resolver Resolver, domain string, wildcard bool) (LookupResult, error) {
	if resolver == nil {
		return LookupResult{}, ErrNilResolver
	}

	name, wildcard, err := normalizeRequestName(domain, wildcard)
	if err != nil {
		return LookupResult{}, err
	}

	for current := name; current != ""; current = parentName(current) {
		records, owner, err := lookupRRset(ctx, resolver, current)
		if err != nil {
			return LookupResult{}, err
		}
		if len(records) != 0 {
			return LookupResult{
				Found:    true,
				Domain:   owner,
				Records:  records,
				Wildcard: wildcard,
			}, nil
		}
	}

	return LookupResult{Wildcard: wildcard}, nil
}

// Authorize checks whether issuer may receive a certificate for domain.
//
// A nil error means issuance is authorized. A missing CAA RRset authorizes
// issuance, as required by RFC 8659. Denials wrap ErrNotAuthorized, and
// protocol violations wrap one of the other sentinel errors.
func Authorize(ctx context.Context, resolver Resolver, issuer, domain string, wildcard bool) error {
	issuer, err := normalizeIssuer(issuer)
	if err != nil {
		return err
	}

	result, err := Lookup(ctx, resolver, domain, wildcard)
	if err != nil {
		return err
	}
	if !result.Found {
		return nil
	}

	if tag, found := findUnknownCriticalTag(result.Records); found {
		return fmt.Errorf("%w %q in RRset at %q", ErrUnrecognizedCritical, tag, result.Domain)
	}

	applicable := applicableIssueRecords(result.Records, result.Wildcard)
	if len(applicable) == 0 {
		// An RRset containing only iodef or unrecognized non-critical
		// properties places no issuance restriction on this CA.
		return nil
	}

	for _, record := range applicable {
		if sameDNSName(issueIssuer(record.Value), issuer) {
			return nil
		}
	}

	return fmt.Errorf("%w: issuer %q is not listed for %q (policy at %q)",
		ErrNotAuthorized, issuer, domain, result.Domain)
}

// Checker is a convenience authorizer for a fixed CA identity.
type Checker struct {
	Resolver Resolver
	Issuer   string
}

// Authorize checks one requested name using the checker's resolver and CA
// identity. It is intended to be called by order authorization before issuance.
func (c Checker) Authorize(ctx context.Context, domain string, wildcard bool) error {
	return Authorize(ctx, c.Resolver, c.Issuer, domain, wildcard)
}

const maxCNAMEHops = 16

// lookupRRset implements the CAA(X) operation from RFC 8659: query CAA at X,
// chasing aliases, and return the first non-empty RRset along that alias chain.
func lookupRRset(ctx context.Context, resolver Resolver, name string) ([]CAARecord, string, error) {
	current := name
	seen := make(map[string]struct{}, maxCNAMEHops+1)

	for hops := 0; hops <= maxCNAMEHops; hops++ {
		if _, exists := seen[current]; exists {
			return nil, "", fmt.Errorf("%w: alias cycle at %q", ErrCNAMEChain, current)
		}
		seen[current] = struct{}{}

		records, err := resolver.LookupCAA(ctx, current)
		if err != nil {
			return nil, "", err
		}
		if len(records) != 0 {
			return records, current, nil
		}

		alias, err := resolver.LookupCNAME(ctx, current)
		if err != nil {
			return nil, "", err
		}
		if alias == "" {
			return nil, current, nil
		}

		current, err = canonicalName(alias)
		if err != nil {
			return nil, "", fmt.Errorf("%w: CNAME target %q", ErrInvalidDomain, alias)
		}
	}

	return nil, "", fmt.Errorf("%w: more than %d aliases from %q", ErrCNAMEChain, maxCNAMEHops, name)
}

func normalizeRequestName(raw string, wildcard bool) (string, bool, error) {
	name := strings.TrimSpace(raw)

	if strings.HasPrefix(name, "*.") {
		wildcard = true
		name = strings.TrimPrefix(name, "*.")
	} else if strings.Contains(name, "*") {
		return "", false, fmt.Errorf("%w %q", ErrInvalidDomain, raw)
	}

	name, err := canonicalName(name)
	if err != nil {
		return "", false, err
	}
	return name, wildcard, nil
}

func canonicalName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.TrimSuffix(name, ".")

	if name == "" || len(name) > 253 {
		return "", fmt.Errorf("%w %q", ErrInvalidDomain, raw)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("%w %q", ErrInvalidDomain, raw)
		}
	}
	return name, nil
}

func normalizeIssuer(raw string) (string, error) {
	name, err := canonicalName(issueIssuer(raw))
	if err != nil {
		return "", fmt.Errorf("%w %q", ErrInvalidIssuer, raw)
	}
	return name, nil
}

func parentName(name string) string {
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		return name[dot+1:]
	}
	return ""
}

func findUnknownCriticalTag(records []CAARecord) (string, bool) {
	for _, record := range records {
		if record.Flags&CriticalFlag == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(record.Tag)) {
		case TagIssue, TagIssueWild, TagIODef:
			continue
		default:
			return record.Tag, true
		}
	}
	return "", false
}

func applicableIssueRecords(records []CAARecord, wildcard bool) []CAARecord {
	if wildcard {
		if wildRecords := recordsWithTag(records, TagIssueWild); len(wildRecords) != 0 {
			// issuewild replaces, rather than augments, issue for wildcards.
			return wildRecords
		}
	}
	return recordsWithTag(records, TagIssue)
}

func recordsWithTag(records []CAARecord, tag string) []CAARecord {
	selected := make([]CAARecord, 0, len(records))
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Tag), tag) {
			selected = append(selected, record)
		}
	}
	return selected
}

// issueIssuer extracts issuer-domain-name from an issue/issuewild value.
// Parameters following the first semicolon are not part of the authorization.
// An empty issuer is deliberate: RFC 8659 treats it as authorizing no CA.
func issueIssuer(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	if semicolon := strings.IndexByte(value, ';'); semicolon >= 0 {
		value = value[:semicolon]
	}
	return strings.TrimSpace(value)
}

func sameDNSName(left, right string) bool {
	left = strings.TrimSuffix(strings.TrimSpace(left), ".")
	right = strings.TrimSuffix(strings.TrimSpace(right), ".")
	return left != "" && right != "" && strings.EqualFold(left, right)
}
