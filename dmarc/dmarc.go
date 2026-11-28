// Package dmarc implements DMARC (RFC 7489) policy discovery, identifier
// alignment, and disposition selection.
package dmarc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"

	"prescreen.local/ps-517b39ba66/psl"
)

// Status is a DMARC evaluation result.
type Status string

const (
	None      Status = "none"
	Pass      Status = "pass"
	Fail      Status = "fail"
	TempError Status = "temperror"
	PermError Status = "permerror"

	ResultNone      = None
	ResultPass      = Pass
	ResultFail      = Fail
	ResultTempError = TempError
	ResultPermError = PermError
)

// Disposition is the action selected for a failed DMARC evaluation.
type Disposition string

const (
	DispositionNone       Disposition = "none"
	DispositionQuarantine Disposition = "quarantine"
	DispositionReject     Disposition = "reject"
)

// Resolver is the DNS interface needed for DMARC policy discovery.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// DefaultResolver is used when no resolver is supplied.
var DefaultResolver Resolver = net.DefaultResolver

// SPFResult is the subset of an SPF result needed for DMARC alignment.
type SPFResult struct {
	Status string `json:"status"`
	Domain string `json:"domain"`
}

// DKIMResult is the subset of a DKIM result needed for DMARC alignment.
type DKIMResult struct {
	Status string `json:"status"`
	Domain string `json:"domain"`
}

// Options controls one DMARC evaluation.
type Options struct {
	FromDomain string
	SPF        SPFResult
	DKIM       []DKIMResult
	Resolver   Resolver
	// PctSeed makes pct sampling deterministic. It defaults to FromDomain.
	PctSeed string
}

// Result contains the DMARC decision and its supporting alignment data.
type Result struct {
	Status         Status      `json:"status"`
	FromDomain     string      `json:"from_domain"`
	OrgDomain      string      `json:"org_domain,omitempty"`
	RecordDomain   string      `json:"record_domain,omitempty"`
	Record         *Record     `json:"record,omitempty"`
	SPFAligned     bool        `json:"spf_aligned"`
	DKIMAligned    bool        `json:"dkim_aligned"`
	AppliedPolicy  Policy      `json:"applied_policy,omitempty"`
	Disposition    Disposition `json:"disposition"`
	Pct            int         `json:"pct"`
	Sampled        bool        `json:"sampled"`
	Reason         string      `json:"reason,omitempty"`
	SPFStatus      string      `json:"spf_status,omitempty"`
	SPFDomain      string      `json:"spf_domain,omitempty"`
	DKIMDomainUsed string      `json:"dkim_domain_used,omitempty"`
}

// Evaluate applies the DMARC policy for opts.FromDomain.
func Evaluate(ctx context.Context, opts Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fromDomain := normalizeDomain(opts.FromDomain)
	result := Result{
		FromDomain:  fromDomain,
		OrgDomain:   psl.OrganizationalDomain(fromDomain),
		SPFStatus:   strings.ToLower(strings.TrimSpace(opts.SPF.Status)),
		SPFDomain:   normalizeDomain(opts.SPF.Domain),
		Disposition: DispositionNone,
	}
	if fromDomain == "" {
		result.Status = PermError
		result.Reason = "empty From domain"
		return result, errors.New("dmarc: empty From domain")
	}
	if result.OrgDomain == "" {
		result.OrgDomain = fromDomain
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = DefaultResolver
	}

	record, recordDomain, err := discover(ctx, fromDomain, result.OrgDomain, resolver)
	if err != nil {
		var dnsErr *dnsLookupError
		if errors.As(err, &dnsErr) {
			result.Status = TempError
			result.Reason = err.Error()
			return result, nil
		}
		result.Status = PermError
		result.Reason = err.Error()
		return result, nil
	}
	if record == nil {
		result.Status = None
		result.Disposition = DispositionNone
		result.Reason = "no DMARC record"
		return result, nil
	}
	result.Record = record
	result.RecordDomain = recordDomain
	result.Pct = record.Pct
	result.Sampled = true

	for _, item := range opts.DKIM {
		if !strings.EqualFold(strings.TrimSpace(item.Status), "pass") {
			continue
		}
		domain := normalizeDomain(item.Domain)
		if IsAligned(domain, fromDomain, record.ADKIM) {
			result.DKIMAligned = true
			result.DKIMDomainUsed = domain
			break
		}
	}
	if result.SPFStatus == "pass" && IsAligned(result.SPFDomain, fromDomain, record.ASPF) {
		result.SPFAligned = true
	}
	if result.SPFAligned || result.DKIMAligned {
		result.Status = Pass
		result.AppliedPolicy = PolicyNone
		result.Disposition = DispositionNone
		result.Reason = "aligned authenticated identifier"
		return result, nil
	}

	result.Status = Fail
	result.AppliedPolicy = policyFor(record, fromDomain, recordDomain)
	if result.AppliedPolicy == PolicyNone {
		result.Disposition = DispositionNone
		result.Reason = "DMARC policy is none"
		return result, nil
	}
	seed := opts.PctSeed
	if seed == "" {
		seed = fromDomain
	}
	if record.Pct < 100 {
		result.Sampled = sampled(seed, record.Pct)
	}
	if !result.Sampled {
		result.Disposition = DispositionNone
		result.Reason = "not selected by pct sampling"
		return result, nil
	}
	result.Disposition = Disposition(result.AppliedPolicy)
	result.Reason = "no aligned authenticated identifier"
	return result, nil
}

// IsAligned reports whether authenticated and header-from domains align under
// strict or relaxed DMARC alignment.
func IsAligned(authenticated, fromDomain string, mode Alignment) bool {
	authenticated = normalizeDomain(authenticated)
	fromDomain = normalizeDomain(fromDomain)
	if authenticated == "" || fromDomain == "" {
		return false
	}
	if authenticated == fromDomain {
		return true
	}
	if mode == AlignmentStrict {
		return false
	}
	authOrg := psl.OrganizationalDomain(authenticated)
	fromOrg := psl.OrganizationalDomain(fromDomain)
	if authOrg == "" || fromOrg == "" {
		return false
	}
	return authOrg == fromOrg
}

// Align is an alias for IsAligned.
func Align(authenticated, fromDomain string, mode Alignment) bool {
	return IsAligned(authenticated, fromDomain, mode)
}

func policyFor(record *Record, fromDomain, recordDomain string) Policy {
	if recordDomain != "" && fromDomain != recordDomain && isSubdomain(fromDomain, recordDomain) {
		if record.SubdomainPolicy != "" {
			return record.SubdomainPolicy
		}
	}
	return record.Policy
}

func isSubdomain(domain, parent string) bool {
	return strings.HasSuffix(domain, "."+parent)
}

func sampled(seed string, pct int) bool {
	if pct >= 100 {
		return true
	}
	if pct <= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(seed))
	value := binary.BigEndian.Uint32(sum[:4]) % 100
	return int(value) < pct
}

type dnsLookupError struct {
	err error
}

func (e *dnsLookupError) Error() string { return "dmarc: temporary DNS error: " + e.err.Error() }
func (e *dnsLookupError) Unwrap() error { return e.err }

func discover(ctx context.Context, fromDomain, orgDomain string, resolver Resolver) (*Record, string, error) {
	names := []string{"_dmarc." + fromDomain}
	if orgDomain != "" && orgDomain != fromDomain {
		names = append(names, "_dmarc."+orgDomain)
	}
	for _, name := range names {
		records, err := resolver.LookupTXT(ctx, name)
		if err != nil {
			if isTemporary(err) {
				return nil, "", &dnsLookupError{err: err}
			}
			if isNotFound(err) {
				continue
			}
			return nil, "", err
		}
		var found []string
		for _, text := range records {
			if isDMARCRecord(text) {
				found = append(found, text)
			}
		}
		if len(found) > 1 {
			return nil, "", fmt.Errorf("dmarc: multiple records at %s", name)
		}
		if len(found) == 1 {
			record, err := ParseRecord(found[0])
			if err != nil {
				return nil, "", fmt.Errorf("dmarc: invalid record at %s: %w", name, err)
			}
			return record, strings.TrimPrefix(name, "_dmarc."), nil
		}
	}
	return nil, "", nil
}

// Lookup discovers the effective DMARC record for domain and returns it with
// the domain at which it was published.
func Lookup(ctx context.Context, domain string, resolver Resolver) (*Record, string, error) {
	if resolver == nil {
		resolver = DefaultResolver
	}
	domain = normalizeDomain(domain)
	if domain == "" {
		return nil, "", errors.New("dmarc: empty domain")
	}
	org := psl.OrganizationalDomain(domain)
	if org == "" {
		org = domain
	}
	return discover(ctx, domain, org, resolver)
}

func isDMARCRecord(text string) bool {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(text), "v=dmarc1") {
		return false
	}
	if len(text) == 7 {
		return false
	}
	return true
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if at := strings.LastIndexByte(domain, '@'); at >= 0 {
		domain = domain[at+1:]
	}
	return strings.TrimSuffix(domain, ".")
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nxdomain") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "not found")
}

func isTemporary(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout")
}
