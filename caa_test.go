package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeResolver struct {
	mu      sync.Mutex
	results map[string]LookupResult
	errs    map[string]error
	lookup  func(context.Context, string) (LookupResult, error)
	calls   []string
}

func (f *fakeResolver) LookupCAA(ctx context.Context, name string) (LookupResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	if f.lookup != nil {
		return f.lookup(ctx, name)
	}
	if err := f.errs[name]; err != nil {
		return LookupResult{}, err
	}
	return f.results[name], nil
}

func (f *fakeResolver) calledNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type referralFakeResolver struct {
	*fakeResolver
	via      map[string]LookupResult
	viaErr   error
	viaCalls []string
}

func (f *referralFakeResolver) LookupCAAVia(ctx context.Context, name string, referral Delegation) (LookupResult, error) {
	f.viaCalls = append(f.viaCalls, name)
	if f.viaErr != nil {
		return LookupResult{}, f.viaErr
	}
	return f.via[name], nil
}

func record(tag, value string) Record {
	return Record{Tag: tag, Value: value}
}

func testPolicy(issuers ...string) Policy {
	return Policy{
		IssuerDomainNames: issuers,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func check(t *testing.T, resolver Resolver, request Request, policy Policy) (Decision, error) {
	t.Helper()
	if policy.Logger == nil {
		policy.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return NewChecker(resolver, policy).Check(context.Background(), request)
}

func TestParentDomainRestriction(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issue", "other-ca.example")}},
	}}

	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("issuance was allowed despite parent-domain CAA restriction")
	}
	if decision.RelevantName != "example.com" {
		t.Fatalf("RelevantName = %q, want example.com", decision.RelevantName)
	}
	wantCalls := []string{"www.example.com", "example.com"}
	if got := resolver.calledNames(); !equalStrings(got, wantCalls) {
		t.Fatalf("query sequence = %v, want %v", got, wantCalls)
	}
}

func TestStopsAtFirstNonEmptyRRset(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {Records: []Record{record("issue", "good-ca.example")}},
		"example.com":     {Records: []Record{record("issue", "other-ca.example")}},
	}}

	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("issuance denied: %s", decision.Reason)
	}
	if decision.RelevantName != "www.example.com" {
		t.Fatalf("RelevantName = %q, want www.example.com", decision.RelevantName)
	}
	if got := resolver.calledNames(); !equalStrings(got, []string{"www.example.com"}) {
		t.Fatalf("queries = %v, want only the child query", got)
	}
}

func TestAnswerNameIsUsedForAudit(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {
			Records:    []Record{record("issue", "good-ca.example")},
			AnswerName: "alias.other.test",
		},
		"example.com": {Records: []Record{record("issue", "other-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("issuance denied: %s", decision.Reason)
	}
	if decision.RelevantName != "alias.other.test" {
		t.Fatalf("RelevantName = %q, want alias.other.test", decision.RelevantName)
	}
}

func TestEmptyRRsetContinuesToParent(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {Records: []Record{}},
		"example.com":     {Records: []Record{record("issue", "other-ca.example")}},
	}}

	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("empty child RRset incorrectly stopped the parent climb")
	}
	if decision.RelevantName != "example.com" {
		t.Fatalf("RelevantName = %q, want example.com", decision.RelevantName)
	}
}

func TestNoCAAPermitsIssuance(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{}}
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("issuance denied without any CAA records: %s", decision.Reason)
	}
	if decision.Reason != "no CAA records found" {
		t.Fatalf("Reason = %q", decision.Reason)
	}
	wantCalls := []string{"www.example.com", "example.com", "com"}
	if got := resolver.calledNames(); !equalStrings(got, wantCalls) {
		t.Fatalf("query sequence = %v, want %v", got, wantCalls)
	}
}

func TestIssueWildcardSemantics(t *testing.T) {
	tests := []struct {
		name     string
		records  []Record
		wildcard bool
		allowed  bool
	}{
		{
			name:     "issuewild blocks wildcard when it does not match",
			records:  []Record{record("issue", "good-ca.example"), record("issuewild", "other-ca.example")},
			wildcard: true,
			allowed:  false,
		},
		{
			name:     "issuewild does not affect exact name",
			records:  []Record{record("issue", "good-ca.example"), record("issuewild", "other-ca.example")},
			wildcard: false,
			allowed:  true,
		},
		{
			name:     "issue applies to wildcard when issuewild is absent",
			records:  []Record{record("issue", "good-ca.example")},
			wildcard: true,
			allowed:  true,
		},
		{
			name:     "empty issuewild forbids wildcard",
			records:  []Record{record("issue", "good-ca.example"), record("issuewild", "")},
			wildcard: true,
			allowed:  false,
		},
		{
			name:     "empty issuewild does not affect exact name without issue",
			records:  []Record{record("issuewild", "")},
			wildcard: false,
			allowed:  true,
		},
		{
			name:     "matching issuewild permits wildcard",
			records:  []Record{record("issue", "other-ca.example"), record("issuewild", "good-ca.example")},
			wildcard: true,
			allowed:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{results: map[string]LookupResult{
				"example.com": {Records: test.records},
			}}
			name := "example.com"
			if test.wildcard {
				name = "*.example.com"
			}
			decision, err := check(t, resolver, Request{Name: name, Wildcard: test.wildcard}, testPolicy("good-ca.example"))
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v (reason: %s)", decision.Allowed, test.allowed, decision.Reason)
			}
		})
	}
}

func TestLegitimateSubdomainAuthorization(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"sub.example.com": {Records: []Record{record("issue", "good-ca.example")}},
		"example.com":     {Records: []Record{record("issue", "other-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "sub.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("explicit subdomain authorization was denied: %s", decision.Reason)
	}
	if decision.RelevantName != "sub.example.com" {
		t.Fatalf("RelevantName = %q", decision.RelevantName)
	}
}

func TestWildcardUsesParentPolicy(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
		allowed bool
	}{
		{
			name:    "parent issue restriction applies",
			records: []Record{record("issue", "other-ca.example")},
			allowed: false,
		},
		{
			name:    "parent issue authorizes wildcard without issuewild",
			records: []Record{record("issue", "good-ca.example")},
			allowed: true,
		},
		{
			name:    "parent issuewild authorizes wildcard",
			records: []Record{record("issuewild", "good-ca.example")},
			allowed: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{results: map[string]LookupResult{
				"example.com": {Records: test.records},
			}}
			decision, err := check(t, resolver, Request{Name: "*.www.example.com"}, testPolicy("good-ca.example"))
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v (reason: %s)", decision.Allowed, test.allowed, decision.Reason)
			}
			if decision.RelevantName != "example.com" {
				t.Fatalf("RelevantName = %q", decision.RelevantName)
			}
		})
	}
}

func TestIssueValueParsing(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
		policy  Policy
		allowed bool
	}{
		{
			name:    "empty issue forbids all issuers",
			records: []Record{record("issue", "")},
			allowed: false,
		},
		{
			name:    "semicolon-only issue forbids all issuers",
			records: []Record{record("issue", ";")},
			allowed: false,
		},
		{
			name:    "malformed issue is treated as empty issuer",
			records: []Record{record("issue", "not a domain")},
			allowed: false,
		},
		{
			name:    "malformed issue is additive with a valid issue",
			records: []Record{record("issue", "bad issuer"), record("issue", "good-ca.example")},
			allowed: true,
		},
		{
			name:    "empty issuer is additive with a valid issue",
			records: []Record{record("issue", ";"), record("issue", "good-ca.example")},
			allowed: true,
		},
		{
			name: "unsupported parameter record is additive with a plain issue",
			records: []Record{
				record("issue", "good-ca.example; unknown=x"),
				record("issue", "good-ca.example"),
			},
			allowed: true,
		},
		{
			name:    "multiple issue records are additive",
			records: []Record{record("issue", "other-ca.example"), record("issue", "good-ca.example")},
			allowed: true,
		},
		{
			name:    "unknown issuer parameter does not authorize",
			records: []Record{record("issue", "good-ca.example; accounturi=https://acme.example/acct/1")},
			allowed: false,
		},
		{
			name:    "understood issuer parameter authorizes",
			records: []Record{record("issue", "good-ca.example; accounturi=https://acme.example/acct/1")},
			policy: Policy{
				IssuerDomainNames: []string{"good-ca.example"},
				Parameters: ParameterPolicy{
					"accounturi": func(value string) bool {
						return value == "https://acme.example/acct/1"
					},
				},
			},
			allowed: true,
		},
		{
			name:    "issuer parameter value mismatch does not authorize",
			records: []Record{record("issue", "good-ca.example; accounturi=https://acme.example/acct/2")},
			policy: Policy{
				IssuerDomainNames: []string{"good-ca.example"},
				Parameters: ParameterPolicy{
					"accounturi": func(value string) bool {
						return value == "https://acme.example/acct/1"
					},
				},
			},
			allowed: false,
		},
		{
			name:    "issuer matching is case insensitive and accepts trailing dot",
			records: []Record{record("issue", "Good-CA.Example.")},
			policy:  testPolicy("good-ca.example."),
			allowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{results: map[string]LookupResult{
				"example.com": {Records: test.records},
			}}
			policy := test.policy
			if policy.IssuerDomainNames == nil {
				policy = testPolicy("good-ca.example")
			}
			if policy.Logger == nil {
				policy.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			}
			decision, err := check(t, resolver, Request{Name: "example.com"}, policy)
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v (reason: %s)", decision.Allowed, test.allowed, decision.Reason)
			}
		})
	}
}

func TestUnknownTagsAndIodef(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{
			record("iodef", "mailto:security@example.com"),
			record("future", "ignored"),
		}},
	}}
	policy := testPolicy("good-ca.example")
	policy.Logger = logger

	decision, err := check(t, resolver, Request{Name: "example.com"}, policy)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("unknown noncritical tags should not deny: %s", decision.Reason)
	}
	if len(decision.UnknownTags) != 1 || decision.UnknownTags[0].Tag != "future" {
		t.Fatalf("UnknownTags = %#v", decision.UnknownTags)
	}
	if len(decision.IODef) != 1 || decision.IODef[0] != "mailto:security@example.com" {
		t.Fatalf("IODef = %#v", decision.IODef)
	}
	if !strings.Contains(logs.String(), "unrecognized CAA property") || !strings.Contains(logs.String(), "future") {
		t.Fatalf("unknown tag was not logged: %s", logs.String())
	}
}

func TestUnknownCriticalTagDenies(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{{Flags: IssuerCriticalFlag, Tag: "future", Value: "x"}}},
	}}
	decision, err := check(t, resolver, Request{Name: "example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("unknown critical tag did not deny issuance")
	}

	policy := testPolicy("good-ca.example")
	policy.AllowUnknownCritical = true
	decision, err = check(t, resolver, Request{Name: "example.com"}, policy)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("AllowUnknownCritical did not override denial: %s", decision.Reason)
	}
}

func TestUnknownOnlyRRsetStopsParentClimb(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {Records: []Record{record("future", "x")}},
		"example.com":     {Records: []Record{record("issue", "other-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("unknown-only relevant RRset should permit: %s", decision.Reason)
	}
	if decision.RelevantName != "www.example.com" {
		t.Fatalf("RelevantName = %q", decision.RelevantName)
	}
	if got := resolver.calledNames(); !equalStrings(got, []string{"www.example.com"}) {
		t.Fatalf("queries = %v", got)
	}
}

func TestCNAMEChain(t *testing.T) {
	t.Run("target CAA authorizes", func(t *testing.T) {
		resolver := &fakeResolver{results: map[string]LookupResult{
			"www.example.com":  {CNAME: "alias.other.test"},
			"alias.other.test": {Records: []Record{record("issue", "good-ca.example")}},
			"example.com":      {Records: []Record{record("issue", "other-ca.example")}},
		}}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if !decision.Allowed {
			t.Fatalf("target CAA should authorize: %s", decision.Reason)
		}
		if decision.RelevantName != "alias.other.test" {
			t.Fatalf("RelevantName = %q", decision.RelevantName)
		}
		wantCalls := []string{"www.example.com", "alias.other.test"}
		if got := resolver.calledNames(); !equalStrings(got, wantCalls) {
			t.Fatalf("queries = %v, want %v", got, wantCalls)
		}
	})

	t.Run("empty target climbs original parent", func(t *testing.T) {
		resolver := &fakeResolver{results: map[string]LookupResult{
			"www.example.com":  {CNAME: "alias.other.test"},
			"alias.other.test": {},
			"example.com":      {Records: []Record{record("issue", "other-ca.example")}},
		}}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if decision.Allowed {
			t.Fatal("parent restriction after empty CNAME target was bypassed")
		}
		wantCalls := []string{"www.example.com", "alias.other.test", "example.com"}
		if got := resolver.calledNames(); !equalStrings(got, wantCalls) {
			t.Fatalf("queries = %v, want %v", got, wantCalls)
		}
	})

	t.Run("loop is rejected", func(t *testing.T) {
		resolver := &fakeResolver{results: map[string]LookupResult{
			"www.example.com": {CNAME: "a.other.test"},
			"a.other.test":    {CNAME: "www.example.com"},
		}}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err == nil || !errors.Is(err, ErrAliasLoop) {
			t.Fatalf("error = %v, want ErrAliasLoop", err)
		}
		if decision.Allowed {
			t.Fatal("CNAME loop allowed issuance")
		}
	})
}

func TestDNAMEChain(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.sub.example.com": {DNAME: &DNAME{Owner: "sub.example.com", Target: "other.test"}},
		"www.other.test":      {Records: []Record{record("issue", "good-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "www.sub.example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("DNAME target CAA should authorize: %s", decision.Reason)
	}
	if decision.RelevantName != "www.other.test" {
		t.Fatalf("RelevantName = %q", decision.RelevantName)
	}
}

func TestDNAMEAtOwnerDoesNotAliasItself(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {DNAME: &DNAME{Owner: "example.com", Target: "other.test"}},
	}}
	decision, err := check(t, resolver, Request{Name: "example.com"}, testPolicy("good-ca.example"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("DNAME at owner was treated as an alias: %s", decision.Reason)
	}
	if decision.RelevantName != "" {
		t.Fatalf("RelevantName = %q, want no relevant set", decision.RelevantName)
	}
}

func TestDelegation(t *testing.T) {
	t.Run("referral is followed", func(t *testing.T) {
		base := &fakeResolver{results: map[string]LookupResult{
			"www.example.com": {Delegation: &Delegation{Zone: "example.com", NameServers: []string{"ns.example.com"}}},
		}}
		resolver := &referralFakeResolver{
			fakeResolver: base,
			via: map[string]LookupResult{
				"www.example.com": {Records: []Record{record("issue", "good-ca.example")}},
			},
		}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if !decision.Allowed {
			t.Fatalf("delegated answer should authorize: %s", decision.Reason)
		}
		if len(decision.Delegations) != 1 || decision.Delegations[0].Zone != "example.com" {
			t.Fatalf("Delegations = %#v", decision.Delegations)
		}
		if !equalStrings(resolver.viaCalls, []string{"www.example.com"}) {
			t.Fatalf("via calls = %v", resolver.viaCalls)
		}
	})

	t.Run("unsupported referral fails closed", func(t *testing.T) {
		resolver := &fakeResolver{results: map[string]LookupResult{
			"www.example.com": {Delegation: &Delegation{Zone: "example.com", NameServers: []string{"ns.example.com"}}},
		}}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err == nil || !errors.Is(err, ErrDelegationUnsupported) {
			t.Fatalf("error = %v, want ErrDelegationUnsupported", err)
		}
		if decision.Allowed {
			t.Fatal("unsupported referral allowed issuance")
		}
	})

	t.Run("delegation loop fails closed", func(t *testing.T) {
		base := &fakeResolver{results: map[string]LookupResult{
			"www.example.com": {Delegation: &Delegation{Zone: "example.com", NameServers: []string{"ns.example.com"}}},
		}}
		resolver := &referralFakeResolver{
			fakeResolver: base,
			via: map[string]LookupResult{
				"www.example.com": {Delegation: &Delegation{Zone: "example.com", NameServers: []string{"ns.example.com"}}},
			},
		}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err == nil || !errors.Is(err, ErrDelegationLoop) {
			t.Fatalf("error = %v, want ErrDelegationLoop", err)
		}
		if decision.Allowed {
			t.Fatal("delegation loop allowed issuance")
		}
	})

	t.Run("bogus referral fails closed before following", func(t *testing.T) {
		base := &fakeResolver{results: map[string]LookupResult{
			"www.example.com": {
				Delegation: &Delegation{Zone: "example.com", NameServers: []string{"ns.example.com"}},
				DNSSEC:     DNSSECBogus,
			},
		}}
		resolver := &referralFakeResolver{
			fakeResolver: base,
			via: map[string]LookupResult{
				"www.example.com": {Records: []Record{record("issue", "good-ca.example")}, DNSSEC: DNSSECSecure},
			},
		}
		decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
		if err == nil || !errors.Is(err, ErrLookupFailed) {
			t.Fatalf("error = %v, want ErrLookupFailed", err)
		}
		if decision.Allowed {
			t.Fatal("bogus referral allowed issuance")
		}
		if len(resolver.viaCalls) != 0 {
			t.Fatalf("bogus referral was followed: %v", resolver.viaCalls)
		}
	})
}

func TestDNSSECPolicy(t *testing.T) {
	records := []Record{record("issue", "good-ca.example")}
	tests := []struct {
		name      string
		status    DNSSECStatus
		mode      DNSSECMode
		allowed   bool
		wantError bool
	}{
		{name: "permissive accepts insecure", status: DNSSECInsecure, mode: DNSSECPermissive, allowed: true},
		{name: "permissive accepts unknown", status: DNSSECUnknown, mode: DNSSECPermissive, allowed: true},
		{name: "bogus always denies", status: DNSSECBogus, mode: DNSSECPermissive, allowed: false, wantError: true},
		{name: "secure accepted when required", status: DNSSECSecure, mode: DNSSECRequireSecure, allowed: true},
		{name: "insecure denied when secure required", status: DNSSECInsecure, mode: DNSSECRequireSecure, allowed: false, wantError: true},
		{name: "unknown denied when insecure accepted", status: DNSSECUnknown, mode: DNSSECAcceptInsecure, allowed: false, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{results: map[string]LookupResult{
				"example.com": {Records: records, DNSSEC: test.status},
			}}
			policy := testPolicy("good-ca.example")
			policy.DNSSECMode = test.mode
			decision, err := check(t, resolver, Request{Name: "example.com"}, policy)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v (reason: %s)", decision.Allowed, test.allowed, decision.Reason)
			}
		})
	}
}

func TestDNSSECBogusAtAncestorDenies(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {DNSSEC: DNSSECSecure},
		"example.com": {
			Records: []Record{record("issue", "good-ca.example")},
			DNSSEC:  DNSSECBogus,
		},
	}}
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err == nil || !errors.Is(err, ErrLookupFailed) {
		t.Fatalf("error = %v, want ErrLookupFailed", err)
	}
	if decision.Allowed {
		t.Fatal("bogus ancestor response allowed issuance")
	}
}

func TestDNSSECAggregatesAliasQueries(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"www.example.com": {CNAME: "alias.other.test", DNSSEC: DNSSECBogus},
		"alias.other.test": {
			Records: []Record{record("issue", "good-ca.example")},
			DNSSEC:  DNSSECSecure,
		},
	}}
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
	if err == nil || !errors.Is(err, ErrLookupFailed) {
		t.Fatalf("error = %v, want ErrLookupFailed", err)
	}
	if decision.Allowed {
		t.Fatal("bogus alias response allowed issuance")
	}
}

func TestLookupFailuresFailClosed(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "servfail", err: &DNSRcodeError{Name: "www.example.com", Rcode: 2}},
		{name: "network", err: errors.New("network unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeResolver{errs: map[string]error{"www.example.com": test.err}}
			decision, err := check(t, resolver, Request{Name: "www.example.com"}, testPolicy("good-ca.example"))
			if err == nil || !errors.Is(err, ErrLookupFailed) {
				t.Fatalf("error = %v, want ErrLookupFailed", err)
			}
			if decision.Allowed {
				t.Fatal("lookup failure allowed issuance")
			}
		})
	}
}

func TestQueryTimeoutFailsClosed(t *testing.T) {
	resolver := &fakeResolver{lookup: func(ctx context.Context, name string) (LookupResult, error) {
		<-ctx.Done()
		return LookupResult{}, ctx.Err()
	}}
	policy := testPolicy("good-ca.example")
	policy.QueryTimeout = 10 * time.Millisecond
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, policy)
	if err == nil || !errors.Is(err, ErrLookupFailed) {
		t.Fatalf("error = %v, want ErrLookupFailed", err)
	}
	if decision.Allowed {
		t.Fatal("query timeout allowed issuance")
	}
}

func TestLateEmptyResultFailsClosed(t *testing.T) {
	resolver := &fakeResolver{lookup: func(ctx context.Context, name string) (LookupResult, error) {
		time.Sleep(25 * time.Millisecond)
		return LookupResult{}, nil
	}}
	policy := testPolicy("good-ca.example")
	policy.QueryTimeout = 5 * time.Millisecond
	decision, err := check(t, resolver, Request{Name: "www.example.com"}, policy)
	if err == nil || !errors.Is(err, ErrLookupFailed) {
		t.Fatalf("error = %v, want ErrLookupFailed", err)
	}
	if decision.Allowed {
		t.Fatal("late empty response after timeout allowed issuance")
	}
}

func TestPolicyValidation(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issue", "good-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "example.com"}, testPolicy("bad identity"))
	if err == nil || !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("error = %v, want ErrInvalidPolicy", err)
	}
	if decision.Allowed {
		t.Fatal("invalid configuration allowed issuance")
	}

	policy := testPolicy("good-ca.example")
	policy.DNSSECMode = DNSSECMode(99)
	decision, err = check(t, resolver, Request{Name: "example.com"}, policy)
	if err == nil || !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("error = %v, want ErrInvalidPolicy", err)
	}
	if decision.Allowed {
		t.Fatal("invalid DNSSEC mode allowed issuance")
	}
}

func TestMissingIssuerIdentityFailsClosed(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issue", "good-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "example.com"}, testPolicy())
	if err == nil || !errors.Is(err, ErrNoIssuerIdentity) {
		t.Fatalf("error = %v, want ErrNoIssuerIdentity", err)
	}
	if decision.Allowed {
		t.Fatal("missing issuer identity allowed restricted issuance")
	}
}

func TestAuthorize(t *testing.T) {
	allowed := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issue", "good-ca.example")}},
	}}
	checker := NewChecker(allowed, testPolicy("good-ca.example"))
	if err := checker.Authorize(context.Background(), Request{Name: "example.com"}); err != nil {
		t.Fatalf("Authorize returned error for permitted issuance: %v", err)
	}

	denied := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issue", "other-ca.example")}},
	}}
	checker = NewChecker(denied, testPolicy("good-ca.example"))
	err := checker.Authorize(context.Background(), Request{Name: "example.com"})
	if err == nil || !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("error = %v, want ErrNotAuthorized", err)
	}
}

func TestInvalidRequests(t *testing.T) {
	for _, name := range []string{"", ".", "www.*.example.com", "exa mple.com", "example..com", "tést.example"} {
		t.Run(name, func(t *testing.T) {
			resolver := &fakeResolver{}
			decision, err := check(t, resolver, Request{Name: name}, testPolicy("good-ca.example"))
			if err == nil || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
			if decision.Allowed {
				t.Fatal("invalid request allowed issuance")
			}
			if len(resolver.calledNames()) != 0 {
				t.Fatalf("resolver was called for invalid request: %v", resolver.calledNames())
			}
		})
	}
}

func TestWildcardRequestNameNormalization(t *testing.T) {
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{record("issuewild", "good-ca.example")}},
	}}
	decision, err := check(t, resolver, Request{Name: "*.Example.COM."}, testPolicy("GOOD-CA.EXAMPLE"))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed || !decision.Wildcard {
		t.Fatalf("wildcard request not normalized: allowed=%v wildcard=%v reason=%s",
			decision.Allowed, decision.Wildcard, decision.Reason)
	}
}

func TestMalformedUnknownTagIsIgnored(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	resolver := &fakeResolver{results: map[string]LookupResult{
		"example.com": {Records: []Record{{Tag: "bad-tag", Value: "x"}}},
	}}
	policy := testPolicy("good-ca.example")
	policy.Logger = logger
	decision, err := check(t, resolver, Request{Name: "example.com"}, policy)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("malformed noncritical tag should be ignored: %s", decision.Reason)
	}
	if len(decision.UnknownTags) != 1 {
		t.Fatalf("UnknownTags = %#v", decision.UnknownTags)
	}
	if !strings.Contains(logs.String(), "bad-tag") {
		t.Fatalf("malformed unknown tag was not logged: %s", logs.String())
	}
}

func TestParseIssueValue(t *testing.T) {
	tests := []struct {
		value     string
		issuer    string
		hasIssuer bool
		params    int
		malformed bool
	}{
		{value: "", hasIssuer: false},
		{value: "ca.example", issuer: "ca.example", hasIssuer: true},
		{value: " CA.EXAMPLE. ", issuer: "ca.example", hasIssuer: true},
		{value: "ca.example;", issuer: "ca.example", hasIssuer: true},
		{value: "ca.example; accounturi=https://acme.example/acct/1", issuer: "ca.example", hasIssuer: true, params: 1},
		{value: "ca.example; bad", hasIssuer: false, malformed: true},
		{value: "bad issuer", hasIssuer: false, malformed: true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			parsed, malformed := parseIssueValue(test.value)
			if parsed.hasIssuer != test.hasIssuer || parsed.issuer != test.issuer ||
				len(parsed.parameters) != test.params || malformed != test.malformed {
				t.Fatalf("parseIssueValue(%q) = %#v, malformed=%v", test.value, parsed, malformed)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
