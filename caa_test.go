package netsec

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type testResolver struct {
	caa     map[string][]CAARecord
	cname   map[string]string
	queries []string
}

func newTestResolver() *testResolver {
	return &testResolver{
		caa:   make(map[string][]CAARecord),
		cname: make(map[string]string),
	}
}

func (r *testResolver) LookupCAA(_ context.Context, fqdn string) ([]CAARecord, error) {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	r.queries = append(r.queries, "CAA "+fqdn)
	return r.caa[fqdn], nil
}

func (r *testResolver) LookupCNAME(_ context.Context, fqdn string) (string, error) {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	r.queries = append(r.queries, "CNAME "+fqdn)
	return r.cname[fqdn], nil
}

func issue(value string) CAARecord {
	return CAARecord{Tag: TagIssue, Value: value}
}

func issueWild(value string) CAARecord {
	return CAARecord{Tag: TagIssueWild, Value: value}
}

func TestAuthorizeUsesNearestRRSetWithoutMerging(t *testing.T) {
	resolver := newTestResolver()
	resolver.caa["example.com"] = []CAARecord{issue("ca-parent.example")}
	resolver.caa["inner.example.com"] = []CAARecord{issue("ca-child.example")}

	tests := []struct {
		name   string
		domain string
		issuer string
		want   error
	}{
		{
			name:   "climbs multiple labels to parent policy",
			domain: "a.b.missing.example.com",
			issuer: "ca-parent.example",
		},
		{
			name:   "nearest child policy is used instead of parent",
			domain: "host.inner.example.com",
			issuer: "ca-child.example",
		},
		{
			name:   "nearest child policy excludes parent CA",
			domain: "host.inner.example.com",
			issuer: "ca-parent.example",
			want:   ErrNotAuthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Authorize(context.Background(), resolver, tt.issuer, tt.domain, false)
			checkError(t, err, tt.want)
		})
	}
}

func TestAuthorizeFollowsCNAMEChainToPolicy(t *testing.T) {
	resolver := newTestResolver()
	resolver.cname["www.example.com"] = "edge.cdn.example.net"
	resolver.cname["edge.cdn.example.net"] = "policy.cdn.example.net"
	resolver.caa["policy.cdn.example.net"] = []CAARecord{issue("ca-allowed.example")}
	// A policy at the original parent must not override the target RRset.
	resolver.caa["example.com"] = []CAARecord{issue("ca-parent.example")}

	err := Authorize(context.Background(), resolver, "ca-allowed.example", "www.example.com", false)
	if err != nil {
		t.Fatalf("authorize through CNAME chain: %v", err)
	}

	err = Authorize(context.Background(), resolver, "ca-parent.example", "www.example.com", false)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("parent CA should not be authorized by target policy: %v", err)
	}

	result, err := Lookup(context.Background(), resolver, "www.example.com", false)
	if err != nil {
		t.Fatalf("lookup through CNAME chain: %v", err)
	}
	if !result.Found || result.Domain != "policy.cdn.example.net" {
		t.Fatalf("unexpected lookup result: %+v", result)
	}
}

func TestAuthorizeCNAMEWithoutPolicyClimbsOriginalHierarchy(t *testing.T) {
	resolver := newTestResolver()
	resolver.cname["alias.example.com"] = "empty.cdn.other.net"
	resolver.caa["example.com"] = []CAARecord{issue("ca-original.example")}
	// RFC 8659 only climbs the FQDN being processed, not the CNAME target's
	// hierarchy. This record must therefore not be consulted.
	resolver.caa["other.net"] = []CAARecord{issue("ca-target-parent.example")}

	err := Authorize(context.Background(), resolver, "ca-original.example", "alias.example.com", false)
	if err != nil {
		t.Fatalf("authorize using original hierarchy: %v", err)
	}
	err = Authorize(context.Background(), resolver, "ca-target-parent.example", "alias.example.com", false)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("CNAME target parent must not be consulted: %v", err)
	}
}

func TestAuthorizeWildcardIssuewildOverridesIssue(t *testing.T) {
	resolver := newTestResolver()
	resolver.caa["shop.example.com"] = []CAARecord{
		issue("ca-issue.example"),
		issueWild("ca-wild.example"),
	}

	tests := []struct {
		name     string
		domain   string
		wildcard bool
		issuer   string
		want     error
	}{
		{
			name:     "issuewild permits wildcard",
			domain:   "shop.example.com",
			wildcard: true,
			issuer:   "ca-wild.example",
		},
		{
			name:     "issue does not leak into wildcard when issuewild exists",
			domain:   "shop.example.com",
			wildcard: true,
			issuer:   "ca-issue.example",
			want:     ErrNotAuthorized,
		},
		{
			name:     "issuewild prefix implies wildcard",
			domain:   "*.shop.example.com",
			wildcard: false,
			issuer:   "ca-wild.example",
		},
		{
			name:     "issuewild does not affect non-wildcard",
			domain:   "shop.example.com",
			wildcard: false,
			issuer:   "ca-wild.example",
			want:     ErrNotAuthorized,
		},
		{
			name:     "issue permits non-wildcard",
			domain:   "shop.example.com",
			wildcard: false,
			issuer:   "ca-issue.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Authorize(context.Background(), resolver, tt.issuer, tt.domain, tt.wildcard)
			checkError(t, err, tt.want)
		})
	}
}

func TestAuthorizeWildcardFallsBackToIssue(t *testing.T) {
	resolver := newTestResolver()
	resolver.caa["example.com"] = []CAARecord{issue("ca-issue.example")}

	if err := Authorize(context.Background(), resolver, "ca-issue.example", "*.example.com", false); err != nil {
		t.Fatalf("wildcard should fall back to issue: %v", err)
	}
}

func TestAuthorizeEmptyIssueValueDenies(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		records  []CAARecord
		wildcard bool
	}{
		{
			name:    "empty issue issuer",
			domain:  "empty.example.com",
			records: []CAARecord{issue(";")},
		},
		{
			name:    "blank issue value",
			domain:  "empty.example.com",
			records: []CAARecord{issue("")},
		},
		{
			name:     "empty issuewild issuer overrides permissive issue",
			domain:   "empty.example.com",
			records:  []CAARecord{issue("ca-issue.example"), issueWild(";")},
			wildcard: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newTestResolver()
			resolver.caa[tt.domain] = tt.records
			err := Authorize(context.Background(), resolver, "ca-issue.example", tt.domain, tt.wildcard)
			if !errors.Is(err, ErrNotAuthorized) {
				t.Fatalf("empty issuer must deny issuance: %v", err)
			}
		})
	}
}

func TestAuthorizeEmptyIssueIsAdditive(t *testing.T) {
	resolver := newTestResolver()
	resolver.caa["example.com"] = []CAARecord{
		issue(";"),
		issue("ca-allowed.example"),
	}

	if err := Authorize(context.Background(), resolver, "ca-allowed.example", "example.com", false); err != nil {
		t.Fatalf("non-empty issue record should still grant authority: %v", err)
	}
}

func TestAuthorizeCriticalFlags(t *testing.T) {
	tests := []struct {
		name    string
		records []CAARecord
		want    error
	}{
		{
			name: "unknown critical property rejects",
			records: []CAARecord{
				issue("ca-allowed.example"),
				{Flags: CriticalFlag, Tag: "future", Value: "ignored"},
			},
			want: ErrUnrecognizedCritical,
		},
		{
			name: "unknown non-critical property is ignored",
			records: []CAARecord{
				issue("ca-allowed.example"),
				{Tag: "future", Value: "ignored"},
			},
		},
		{
			name: "known critical property is processed",
			records: []CAARecord{
				{Flags: CriticalFlag, Tag: TagIssue, Value: "ca-allowed.example"},
			},
		},
		{
			name: "critical iodef is recognized",
			records: []CAARecord{
				{Flags: CriticalFlag, Tag: TagIODef, Value: "mailto:security@example.com"},
				issue("ca-allowed.example"),
			},
		},
		{
			name: "unknown critical property rejects an otherwise empty policy",
			records: []CAARecord{
				{Flags: CriticalFlag, Tag: "future", Value: "ignored"},
			},
			want: ErrUnrecognizedCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newTestResolver()
			resolver.caa["example.com"] = tt.records
			err := Authorize(context.Background(), resolver, "ca-allowed.example", "example.com", false)
			checkError(t, err, tt.want)
		})
	}
}

func TestAuthorizeNoPolicyOrOnlyIODefPermits(t *testing.T) {
	tests := []struct {
		name    string
		records []CAARecord
	}{
		{name: "no CAA anywhere"},
		{
			name: "only iodef imposes no issuance restriction",
			records: []CAARecord{
				{Tag: TagIODef, Value: "mailto:security@example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newTestResolver()
			if tt.records != nil {
				resolver.caa["example.com"] = tt.records
			}
			if err := Authorize(context.Background(), resolver, "ca-any.example", "example.com", false); err != nil {
				t.Fatalf("issuance should be permitted: %v", err)
			}
		})
	}
}

func TestAuthorizeMatchesIssuerWithParametersAndCase(t *testing.T) {
	resolver := newTestResolver()
	resolver.caa["example.com"] = []CAARecord{
		issue(`"CA.Example; accounturi=https://acme.example/account/1"`),
	}

	if err := Authorize(context.Background(), resolver, "ca.example.", "example.com", false); err != nil {
		t.Fatalf("issuer comparison should be case-insensitive and ignore parameters: %v", err)
	}
}

func TestAuthorizeRejectsCNAMECycle(t *testing.T) {
	resolver := newTestResolver()
	resolver.cname["a.example.com"] = "b.example.com"
	resolver.cname["b.example.com"] = "a.example.com"

	err := Authorize(context.Background(), resolver, "ca.example", "a.example.com", false)
	if !errors.Is(err, ErrCNAMEChain) {
		t.Fatalf("CNAME cycle should be rejected: %v", err)
	}
}

func TestAuthorizeValidatesInputs(t *testing.T) {
	tests := []struct {
		name     string
		resolver Resolver
		issuer   string
		domain   string
		want     error
	}{
		{
			name:   "nil resolver",
			issuer: "ca.example",
			domain: "example.com",
			want:   ErrNilResolver,
		},
		{
			name:     "empty domain",
			resolver: newTestResolver(),
			issuer:   "ca.example",
			want:     ErrInvalidDomain,
		},
		{
			name:     "bare wildcard",
			resolver: newTestResolver(),
			issuer:   "ca.example",
			domain:   "*",
			want:     ErrInvalidDomain,
		},
		{
			name:     "empty issuer",
			resolver: newTestResolver(),
			domain:   "example.com",
			want:     ErrInvalidIssuer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Authorize(context.Background(), tt.resolver, tt.issuer, tt.domain, false)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want error matching %v", err, tt.want)
			}
		})
	}
}

func TestResolverMayReportEmptyAnswer(t *testing.T) {
	resolver := newTestResolver()
	result, err := Lookup(context.Background(), resolver, "missing.example.com", false)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.Found || result.Domain != "" || len(result.Records) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got, want := resolver.queries, []string{
		"CAA missing.example.com", "CNAME missing.example.com",
		"CAA example.com", "CNAME example.com",
		"CAA com", "CNAME com",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("query sequence:\n got %v\nwant %v", got, want)
	}
}

func checkError(t *testing.T, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("unexpected error: %v", got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("got %v, want error matching %v", got, want)
	}
}
